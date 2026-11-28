# System Design na Prática — Aula 02

**Plataforma de cursos e vídeo sob demanda** · 02/12 · 19h–21h

Material da segunda aula: uma plataforma de cursos que recebe um vídeo de 2 GB, transforma em streaming adaptativo e entrega por uma CDN — com upload retomável, fila com lease, HLS, cache na borda e retomada entre dispositivos. Tudo roda no seu notebook e cada decisão aparece no Grafana.

| Arquivo | O que é |
|---|---|
| [ROTEIRO.md](ROTEIRO.md) | Roteiro da aula, minuto a minuto, com as contas e os trade-offs |
| [Slides](https://claude.ai/artifact/QAr7QdoMGERjC4fAhD85vh) | A apresentação da aula, 18 slides com notas do apresentador (link privado até ser compartilhado) |
| [LABS.md](LABS.md) | Os seis laboratórios e os dois casos de investigação, passo a passo |
| `app/` | A plataforma (Go): um binário, três papéis — `api`, `worker` e `edge` |
| `ui/` | Interface do laboratório: catálogo, upload, fila, player com HUD e dois dispositivos |
| `db/` | Esquema do Postgres: catálogo, versões de vídeo, fila e progresso |
| `k6/` | Testes de carga: audiência assistindo e checkpoints de progresso |
| `observability/` | Prometheus e o dashboard do Grafana |
| `labs/` | Um `.env` por laboratório |

## Pré-requisitos

- Docker com Compose (Docker Desktop, OrbStack ou equivalente), com pelo menos 4 CPUs e 4 GB.
- `make`. Sem ele, veja "Sem make" abaixo.
- Chrome, Edge ou Firefox (o player usa Media Source Extensions).
- Portas livres: 8090 (laboratório), 3000 (Grafana), 9090 (Prometheus), 8080 (API) e 8081 (borda). Para trocar, defina `UI_PORT`, `GRAFANA_PORT`, `PROM_PORT`, `API_PORT` ou `EDGE_PORT` no `.env`. As portas são as mesmas da Aula 1: rode `make down` lá antes.

## Começando

```bash
make lab1
```

Abra **http://localhost:8090**. A primeira subida baixa as imagens, compila a aplicação e gera o vídeo de exemplo; cerca de um minuto depois a aula "Encurtador de links" aparece como `READY` no catálogo. Clique em **▶ assistir**.

O Grafana fica em http://localhost:3000 — o dashboard **Plataforma de vídeo — Aula 02** é a página inicial, sem login.

## A arquitetura

Três caminhos separados. Os dois arquivos pesados — o original que sobe e os segmentos que descem — nunca passam pela API.

```
APLICAÇÃO    navegador ──► API ──► Postgres            catálogo, busca, matrícula, progresso
                            │
INGESTÃO     navegador ══► storage raw ──► fila ──► workers ══► storage vod
             (upload multipart,            (lease)   (ffmpeg)   (HLS versionado)
              URLs assinadas)
                                                                    ║
REPRODUÇÃO   player ◄══ borda (cache + acesso assinado) ◄═══════════╝  só no MISS
```

| Peça | No laboratório | Em produção seria |
|---|---|---|
| API | `vod api` (Go) | o mesmo, com várias instâncias |
| Storage | Versity Gateway (API S3 sobre disco) | S3, GCS, R2 |
| Fila | tabela `jobs` no Postgres (`FOR UPDATE SKIP LOCKED` + lease) | SQS, Pub/Sub ou a própria tabela, até onde aguentar |
| Workers | `vod worker` + ffmpeg, 1 CPU cada | frota com autoscaling pela idade da fila, ou MediaConvert |
| Borda | `vod edge`: um ponto de presença, cache em memória | CloudFront, Fastly, Cloudflare |
| Player | hls.js com HUD | hls.js, Shaka, AVPlayer, ExoPlayer |

## Os laboratórios

| Lab | Tema | Problema que você vai ver |
|---|---|---|
| 1 | Upload direto e retomável | A conexão cai em 60%; o upload continua de onde parou, no mesmo asset |
| 2 | Fila e workers | Worker morre no meio do encode; worker "zumbi" tenta publicar; rajada vira fila |
| 3 | HLS e bitrate adaptativo | A banda cai de 8 para 2 Mb/s: com ABR a qualidade desce, sem ABR o vídeo trava |
| 4 | CDN e acesso privado | Origem a 850 ms; a autenticação entra na chave de cache e o hit ratio vai a zero |
| 5 | Progresso entre dispositivos | O celular reenvia um checkpoint antigo e a posição volta no tempo |
| 6 | Publicação e rollback | Um pacote incompleto vai ao ar; voltar à versão anterior sem invalidar a CDN |
| Casos A e B | Investigue o play | Dois sintomas, sem a causa: hipótese → experimento → evidência |

Cada `make labN` copia `labs/labN.env` para `.env` e recria os containers, mantendo dados e métricas. Para um experimento dentro do lab, edite o `.env` e rode `make apply`.

## Comandos

```bash
make lab1 … lab6      # troca de laboratório
make caso-a caso-b    # casos de investigação
make apply            # reaplica o .env
make burst N=18       # enfileira 18 publicações de uma vez
make kill-worker      # mata o worker que está processando
make pause-worker     # congela o worker ocupado por mais tempo que a lease
make fila             # a fila, direto do banco
make load SCRIPT=playback VIEWERS=50 DURATION=1m
make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=false
make logs             # api, workers e borda
make down             # para tudo, mantém os dados
make reset            # apaga tudo
```

## A API

```bash
# busca (tsvector + GIN no Postgres)
curl -s 'localhost:8080/api/courses?q=streaming'

# play: confere matrícula e devolve o manifest e o acesso temporário
curl -s -XPOST localhost:8080/api/lessons/1/playback
curl -s -XPOST -H 'X-User: bia' localhost:8080/api/lessons/1/playback   # 403: sem matrícula

# um segmento pela borda: sem cookie, 403; com cookie, olhe o X-Cache
TOKEN=$(curl -s -XPOST localhost:8080/api/lessons/1/playback | sed 's/.*"access":"\([^"]*\)".*/\1/')
curl -s -o /dev/null -D - -H "Cookie: vod_access=$TOKEN" localhost:8081/vod/a1/t1/720p/seg_001.m4s | grep -i x-cache
```

O laboratório não tem login: o cabeçalho `X-User` diz quem é o aluno (`ana` está matriculada em tudo; `bia` não tem o curso 1).

## Sem make

```bash
cp labs/lab1.env .env
docker compose --profile tools down --remove-orphans
docker compose up -d --build --wait
docker compose run --rm -e VIEWERS=50 k6 run /scripts/playback.js
```

## Limites deste laboratório

- Tudo roda numa máquina só: workers, banco, borda e gerador de carga disputam as mesmas CPUs. Os números absolutos não valem para produção; as curvas e as proporções, sim.
- A borda é um ponto de presença só, com cache em memória. Não há roteamento geográfico nem segunda origem: a distância até a origem e a banda do aluno são simuladas.
- O vídeo de exemplo tem 90 segundos e bitrate constante, para o tamanho dos segmentos bater com as contas da aula (um segmento de 6 s a 5 Mb/s ≈ 3,75 MB).
- O acesso assinado impede o play sem matrícula, mas não impede quem tem acesso de copiar os segmentos. Proteção de conteúdo (DRM) fica fora do escopo.
- A fila é uma tabela do Postgres. É uma escolha defensável para esse volume — e é o ponto de partida da discussão sobre quando trocar.
