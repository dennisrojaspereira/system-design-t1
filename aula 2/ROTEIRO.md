# Roteiro — Aula 02: Plataforma de cursos e vídeo sob demanda

**Quando:** 02/12, 19h–21h · **Setor:** educação e mídia
**Tópicos:** requisitos e SLOs · estimativas · upload multipart · fila e workers · HLS e ABR · CDN e acesso privado · progresso entre dispositivos

## Objetivo

A Aula 1 fixou o método num sistema de requisições pequenas. Esta aplica o mesmo método quando o dado é pesado: um arquivo de 2 GB entra e 30 Gb/s saem. Ao final, o aluno consegue:

1. Separar **plano de controle** (API, metadados) de **plano de dados** (bytes de vídeo) e justificar por que os bytes não passam pela API.
2. Desenhar um pipeline assíncrono que tolera entrega repetida: idempotência, lease e publicação atômica.
3. Fazer as contas de entrega: banda na borda, requisições por segundo, tráfego na origem e custo por hora assistida.
4. Diagnosticar um play ruim separando três causas: origem distante, cache mal configurado e adaptação de bitrate.

## O que mudou em relação ao quadro original

O quadro do Excalidraw tinha nove blocos de conteúdo e seis de aprofundamento. Este roteiro mantém o conteúdo e muda quatro coisas:

- **Método explícito.** Os blocos seguem o **Entenda → Estime → Desenhe → Evolua** da Aula 1, em vez de uma lista de componentes.
- **Lab em cada bloco.** O único lab era "investigue o play" no fim, com dados simulados. Agora cada decisão é demonstrada ao vivo, e o fechamento usa dois casos reais rodando no notebook.
- **Números coerentes.** O quadro usava três picos de publicação diferentes (100/dia, 60/h, 18 em 5 min) sem ligá-los. Aqui eles são uma história só: 100/dia em média, 60/h no pico da noite de domingo, e uma rajada de 18 em 5 min quando uma escola sobe a semana inteira.
- **Lacunas preenchidas.** Fora de escopo declarado (ao vivo, DRM, legendas); estado `FAILED` e limite de tentativas no pipeline; armazenamento das variantes na conta de ingestão; e a ponte com a Aula 1 em cada bloco.

## Antes da aula

- [ ] `make reset && make lab1` e conferir que a aula 1 fica `READY` (cerca de um minuto).
- [ ] Dar um play e conferir o Grafana em http://localhost:3000.
- [ ] Passar uma vez por `make lab2` e `make burst N=18`, para as imagens e o vídeo de exemplo já estarem prontos.
- [ ] Quatro janelas: slides, laboratório (http://localhost:8090), Grafana (modo TV, últimos 15 min) e terminal.
- [ ] Enviar o repositório aos alunos com o pedido de rodarem `make lab1` antes.

## Linha do tempo

| Horário | Bloco | Min | Lab |
|---|---|---|---|
| 19:00 | Abertura e 1. Entenda: jornadas e SLOs | 10 | — |
| 19:10 | 2. Estime: entrega, ingestão, progresso | 8 | — |
| 19:18 | 3. Desenhe: três caminhos | 8 | mostrar o catálogo e a busca |
| 19:26 | Catálogo, busca e publicação atômica | 8 | — |
| 19:34 | 4. Evolua: upload direto e retomável | 14 | Lab 1 ao vivo |
| 19:48 | Evolua: processamento assíncrono | 18 | Lab 2 ao vivo |
| 20:06 | Evolua: HLS e bitrate adaptativo | 14 | Lab 3 ao vivo |
| 20:20 | Evolua: CDN, cache e acesso privado | 15 | Lab 4 ao vivo |
| 20:35 | Evolua: progresso entre dispositivos | 10 | Lab 5, demo curta |
| 20:45 | Investigue o play | 12 | Casos A e B |
| 20:57 | Fechamento e desafio | 3 | — |

Se o tempo apertar, corte a busca (fica só o slide) e o caso A (o Lab 4c já mostrou a causa). Mantenha os labs 2, 3 e 4: são os que têm "quebra → conserta" visível.

O Lab 6 e os blocos de aprofundamento (custo, failover, fila atrasada, evolução do MP4 ao ABR) ficam para casa ou para perguntas.

---

## 19:00 — Abertura e 1. Entenda (10 min)

- Gancho: "Na semana passada, a maior resposta do sistema tinha 300 bytes. Hoje um professor vai nos mandar 2 GB."
- O método é o mesmo. O que muda é que **o dado pesado dita a arquitetura**.
- Quatro jornadas, um verbo cada:

| Jornada | Quem | Exemplo |
|---|---|---|
| **Publicar** | professora | envia um vídeo de 2 GB |
| **Encontrar** | aluno | busca uma aula |
| **Assistir** | aluno | dá play em outra região |
| **Retomar** | aluno | para no celular, continua no notebook |

- **SLOs da jornada** (são eles que o Grafana vai mostrar):
  - Play: primeiro quadro **p95 < 2,5 s** por região. Taxa de erro medida à parte.
  - Publicação: **p95 < 15 min** entre upload completo e `READY`, para vídeos de até 30 min.
- **Fora do escopo**, dito em voz alta: transmissão ao vivo, DRM, legendas, recomendação, pagamento.
- Pergunta para a turma: "Qual das quatro jornadas quebra primeiro se a gente fizer tudo passar pela API?"

## 19:10 — 2. Estime (8 min)

Três contas no quadro. Todas voltam nos labs.

**Entrega**

- 10.000 pessoas assistindo × 3 Mb/s = **30 Gb/s** na borda.
- Segmentos de 6 s → 10.000 ÷ 6 ≈ **1.667 GET/s** de vídeo. Áudio em faixa separada dobraria isso.
- Com 95% de hit na CDN, ~**83 GET/s** chegam à origem.

**Ingestão**

- 100 uploads/dia × 2 GB = **200 GB/dia** de originais.
- As variantes somam 8,5 Mb/s (1 + 2,5 + 5). Para 30 min de vídeo: 8,5 Mb/s × 1.800 s ÷ 8 ≈ **1,9 GB** de saída por aula. A saída é quase do tamanho da entrada.
- Pico: 60 jobs/h × 8 min por job = **8 slots** ocupados em média. A 70% de utilização, **12 slots**.

**Progresso**

- 10.000 players ÷ 30 s = **333 escritas/s**, fora pausa e seek.

Mensagem: a ordem de grandeza separa os problemas. Entrega é problema de **banda** (Gb/s). Ingestão é problema de **CPU** (slots). Progresso é problema de **escrita** (linhas/s). Três gargalos, três soluções.

Para discutir: "Se o pico de uploads vier todo em cinco minutos, qual parte do dimensionamento muda primeiro?"

## 19:18 — 3. Desenhe: três caminhos (8 min)

Desenhe em três faixas horizontais:

```
APLICAÇÃO    cliente ──► API ──► Postgres
INGESTÃO     navegador ══► storage raw ──► fila ──► workers ══► storage vod
REPRODUÇÃO   player ◄══ CDN ◄══ storage vod (só no cache miss)
```

- Setas finas são chamadas pequenas; setas duplas são bytes de vídeo.
- Pergunta-chave: **"Destaque os caminhos dos dois arquivos pesados. Qual deles precisa passar pela API?"** Nenhum.
- A API é o plano de controle: autoriza, registra, aponta. O Postgres é a fonte autoritativa.
- Ponte com a Aula 1: lá o cache protegia o banco de leituras repetidas. Aqui a CDN protege a origem de bytes repetidos. Mesma ideia, outra unidade.

Abra http://localhost:8090 e mostre o catálogo: é a faixa de cima funcionando.

## 19:26 — Catálogo, busca e publicação atômica (8 min)

- Modelo de dados em três tabelas:

```
lessons(id, course_id, position, active_asset_id)
assets(id, lesson_id, version, status, hls_prefix)   UNIQUE(lesson_id, version)
```

- Cada upload cria uma **versão** (asset) da aula. `active_asset_id` é o ponteiro para a versão no ar.
- **Publicação atômica:** validar o pacote, marcar `READY` e trocar o ponteiro **na mesma transação**. O aluno pode pesquisar o curso enquanto a nova versão é preparada; ele nunca vê uma versão pela metade.
- **A busca começa no Postgres:** `tsvector` + índice GIN + `websearch_to_tsquery`. Digite `índices` na busca do laboratório e mostre o resultado.
- Quando separar um motor de busca: relevância, facetas ou carga. Aí entra o **outbox** — alteração e evento na mesma transação, indexador com upsert por ID e versão. O índice passa a ser eventual.
- Para discutir: "O índice devolve uma aula com acesso revogado. Qual serviço toma a decisão final no play?" A API de playback, na fonte autoritativa. Troque o aluno para `bia` e tente assistir à aula 1: 403.

## 19:34 — Upload direto, retomável e privado (14 min) — Lab 1

**Objetivo técnico:** transferir 2 GB com retomada e manter a API fora do caminho dos bytes.

- O contrato, em três chamadas:

```
POST /uploads                → { uploadId, assetId, partSize }
POST /uploads/{id}/parts     → URLs assinadas, uma por parte
POST /uploads/{id}/complete  → junta as partes e enfileira
```

- Exemplo: 2 GiB em partes de 16 MiB = **128 PUTs**. Caiu em 95%? Reenvia 6 partes, não 2 GB.

**Demo (Lab 1):** enviar o vídeo de exemplo com queda em 60% → retomar → repetir o complete.

Pontos para narrar durante a demo:

- **Retomar só o que faltou.** O cliente pergunta ao servidor, que pergunta ao storage. Repare que aparecem 9 partes gravadas, não 8.
- **Concluir sem duplicar.** Se a resposta do complete se perde, o cliente tenta de novo. A API responde "já estava concluído". Se o storage concluiu e a API não soube, ela confere o objeto na chave reservada e reconcilia.
- **Isolamento.** A URL autoriza uma parte de uma chave que a API escolheu, por 15 minutos.
- **Limpeza.** Lifecycle para multipart abandonado; validação de codec e tamanho no ingest.

Para discutir: "A conexão cai nos últimos 5%. Como a API e o cliente retomam a sessão sem criar outro asset?"

## 19:48 — Processamento assíncrono (18 min) — Lab 2

**Objetivo técnico:** tolerar entrega repetida, controlar a capacidade e publicar apenas saídas íntegras.

- Estados: `UPLOADING → QUEUED → PROCESSING → READY`, com `FAILED` depois de N tentativas.
- A fila entrega **pelo menos uma vez**. Três mecanismos mantêm o resultado correto:
  1. **Chave idempotente:** `(assetId, sourceVersion, profileVersion)`. Enfileirar duas vezes é no-op.
  2. **Lease:** o worker renova enquanto trabalha. Venceu? Outro assume, numa tentativa nova, com prefixo de saída novo.
  3. **Publicação condicional:** `UPDATE … WHERE attempt_id = :minha_tentativa`. A tentativa antiga perde.

**Demo (Lab 2a):** enviar um vídeo, `make kill-worker`, ver a lease vencer e a tentativa 2 publicar.

**Demo (Lab 2b):** `make pause-worker` e mostrar no log `lease perdida: outra tentativa assumiu o job`.

- Pergunta do quadro: "O worker gravou todos os segmentos e morreu antes de marcar `READY`. Qual estado permite repetir com segurança?" `PROCESSING` com lease vencida. A saída órfã fica no prefixo da tentativa 1.

**Backpressure com números**

- Com 12 slots e jobs de 8 min, a vazão é **1,5 job/min**. Na rajada entram 18 em 5 min = **3,6 jobs/min**. A diferença — 2,1 jobs/min — vira fila enquanto a rajada durar.
- Orçamento: 15 min de meta − 8 de encode − 1 de validação = **6 min de fila**, no máximo.

**Demo (Lab 2c):** `make burst N=18` com 2 workers (~62 s); depois `WORKERS=4` (~39 s). Mostrar a *idade do job mais antigo*.

- Escale pela **idade do job mais antigo**, não pelo tamanho da fila nem pela CPU.
- Sinais: fila sobe com encode estável → faltam slots. Encode também fica lento → CPU, codec, I/O ou arquivos maiores.

## 20:06 — HLS e bitrate adaptativo (14 min) — Lab 3

- Um MP4 único obriga todo mundo à mesma qualidade. HLS corta o vídeo em segmentos pequenos, em várias qualidades, e deixa **o player escolher**.
- Quatro tipos de arquivo:

```
master.m3u8        lista as variantes e a banda de cada uma
720p/index.m3u8    lista os segmentos da variante
720p/init.mp4      cabeçalho do codec
720p/seg_001.m4s   6 segundos de vídeo
```

- **Ladder de exemplo:** 480p / 1 Mb/s · 720p / 2,5 Mb/s · 1080p / 5 Mb/s. Keyframes e timestamps alinhados entre as variantes, para a troca acontecer na emenda.

**Demo (Lab 3):** 8 Mb/s → 2 Mb/s com ABR → travar em 1080p.

- A conta do travamento: 6 s a 5 Mb/s ≈ **30 Mb** por segmento. Em 2 Mb/s leva ≈ **15 s**. Com 8 s de buffer, o vídeo para por ≈ 7 s.
- A mesma rede na variante de 1 Mb/s: 6 Mb ÷ 2 Mb/s = **3 s**. Sobra.
- Mostre na tabela de requisições: `1080p/seg_006.m4s — 15.459 ms` ao lado de `480p/seg_007.m4s — 3.183 ms`.

Para discutir: "A rede caiu de 8 para 2 Mb/s. Qual variante o player deve tentar e o que observar no buffer?"

## 20:20 — CDN, cache e acesso privado (15 min) — Lab 4

**Ideia central:** sirva segmentos na borda **sem misturar autorização com a identidade do objeto em cache**.

- **Caminho e TTL:** `/vod/a42/v3/720p/seg_001.m4s`. A versão está no caminho, o conteúdo nunca muda, o TTL pode ser de um ano. Trocar de versão muda a URL: rollback sem invalidar a CDN.
- **Acesso por matrícula:** `POST /lessons/{id}/playback` confere a matrícula e devolve o manifest e um cookie assinado, com prazo, válido para aquele asset. A borda confere a assinatura — sem consultar o banco. A origem só aceita a CDN.

**Demo (Lab 4a):** play frio (primeiro quadro ~5 s, TTFB 860 ms) e play quente (~0,2 s, TTFB 1 ms).

**Demo (Lab 4c):** carga com `CACHE_KEY=path` (17 idas à origem para 900 segmentos), depois com `path+cookie` (hit ratio 0%, uma ida por segmento).

- É a resposta para a pergunta do quadro: "A taxa de HIT cai após adicionar autenticação. O que pode ter mudado na chave de cache?"
- Use **byte hit ratio** para estimar tráfego e custo na origem; o hit ratio por requisição engana quando playlists pequenas e segmentos grandes se misturam.
- Origin shield e *request collapsing*: cem alunos pedindo o mesmo segmento frio viram uma ida à origem.

**Demo (Lab 4d), se der tempo:** derrubar a origem com cache quente e depois com cache frio.

## 20:35 — Progresso e retomada entre dispositivos (10 min) — Lab 5

- Salvar a posição parece trivial. É um problema de **concorrência** com uma regra de **produto**: voltar para um trecho antigo precisa funcionar.
- Contrato: `PUT /lessons/{id}/progress { generation, seq, positionMs }`. Uma linha por `(user_id, lesson_id)`.
- Política de dois dispositivos: cada play abre uma sessão e incrementa `generation`. A sessão anterior deixa de valer. Dentro da sessão, `seq` ordena.

**Demo (Lab 5):** a história do celular e do notebook, primeiro em `lww` (a posição volta para 11:00), depois em `conditional` (409).

- Pergunta do quadro: "O notebook buscou 4:20 e o celular reenviou 11:00 da sessão anterior. Qual atualização vence e por quê?" A do notebook: generation maior.
- **Heartbeat:** 333 escritas/s. Distribua com jitter — mostre o gráfico se já tiver rodado a carga.
- **Conclusão é estado próprio**, independente da posição: o aluno que concluiu e voltou para rever não "desconclui".
- Ponte com a Aula 1 (Lab 6, analytics): se o volume crescer, o caminho é o mesmo — escrever em memória e descarregar em lote, aceitando perder alguns segundos de progresso.

## 20:45 — Investigue o play (12 min) — Casos A e B

Atividade em grupos. 6 min para hipóteses, 4 min para validar sinais, 2 min de fechamento.

- **Caso A** (`make caso-a`): "demora a começar". Primeiro quadro alto, que não melhora no segundo play.
- **Caso B** (`make caso-b`): "começa rápido e depois trava". Dê play e, dez segundos depois, baixe a rede para 2 Mb/s. Primeiro quadro dentro do SLO, rebuffer alto.

Roteiro de investigação para pôr no quadro:

1. **Sintoma:** quando e para quem?
2. **Hipótese:** componente ou rede?
3. **Experimento:** uma variável por vez.
4. **Decisão:** evidência + métrica.

Métricas para fechar o diagnóstico:

- **Startup** = primeiro quadro − clique.
- **Rebuffer ratio** = segundos travado ÷ (segundos reproduzidos + travado), excluindo startup e seeks.
- Compare por região e separe HIT de MISS.

Pergunta que guia: "Que evidência faria você mudar de hipótese?" O gabarito está no fim do [LABS.md](LABS.md).

## 20:57 — Fechamento e desafio (3 min)

- Uma frase por grupo: **"mudamos X por causa de Y e verificamos o efeito medindo Z."**
- Os três aprendizados:
  1. **Separe controle de dados.** A API autoriza e aponta; os bytes vão por outro caminho.
  2. **Assíncrono exige idempotência.** Chave única, lease e publicação condicional.
  3. **Cache é identidade.** O que entra na chave decide se a CDN funciona.
- **Desafio para casa:** Lab 6 (publicar um pacote incompleto e fazer rollback) e a conta de custo abaixo.

---

## Aprofundamento (para casa ou para perguntas)

### Do MP4 único ao streaming adaptativo

A arquitetura cresce quando a medição mostra qual requisito deixou de ser atendido.

| Estágio | O que resolve | O que medir para decidir o próximo |
|---|---|---|
| MP4 direto | primeira versão: upload privado, MP4 com início rápido | primeiro quadro, falhas, bytes por hora assistida |
| MP4 + CDN | latência regional | TTFB por país, HIT × MISS |
| HLS + ABR | rede variável | bitrate escolhido × throughput, stalls em rede lenta |
| Duas origens | continuidade regional | objetivo de recuperação, histórico de incidentes |

"O primeiro quadro melhorou, mas o vídeo ainda trava no 4G" → a hipótese é adaptação, não distância. Teste com ABR.

### Quanto custa uma hora assistida?

- 3.000.000 bits/s × 3.600 s ÷ 8 = **1,35 GB por hora assistida**.
- 100 mil horas/mês → **135 TB/mês** saindo da borda.
- Com 95% de byte hit ratio, **6,75 TB** são buscados na origem. Os 135 TB continuam saindo da borda para o aluno: cache reduz a conta da origem, não a da entrega.
- Ingestão, para comparar: 100 originais/dia × 2 GB × 30 dias ≈ **6 TB novos por mês**, mais ~5,7 TB de variantes.
- A alavanca: baixar o bitrate médio de 3 para 2,4 Mb/s reduz o volume em **20%**. Acompanhe custo por hora assistida junto com qualidade e rebuffer ratio.

"O hit ratio sobe e a fatura cresce. O que pode ter aumentado?" Audiência, bitrate médio, ou tráfego em regiões mais caras.

### CDN fria e perda da origem principal

- **O cache engana:** vídeo popular funciona onde o cache está quente; vídeo novo falha em região fria. Teste HIT e MISS com objetos controlados.
- **Replicação assíncrona:** a réplica pode ter o master e ainda não ter um segmento. Verifique o pacote inteiro nas duas origens antes de prometer failover.
- **Orçamento de timeout:** o tempo de espera até a origem secundária sai do buffer do aluno.
- **Plano de controle:** mídia em cache atende quem já tem autorização. O play novo depende da API que confere matrícula.

### Versionamento do pacote

- Gerar em staging (prefixo por tentativa) → validar → publicar (commit no catálogo) → reverter (trocar a versão ativa).
- O aluno que já deu play mantém a URL versionada até o fim da sessão. A retenção da versão antiga precisa cobrir as sessões ainda válidas.
- Playlist 200 seguida de segmento 404 = referência quebrada ou réplica atrasada.

## Referências

- RFC 8216 — HTTP Live Streaming.
- AWS S3 — Multipart upload; notificações de eventos (entrega pelo menos uma vez).
- Amazon CloudFront — conteúdo privado com cookies assinados; failover de origem.
- PostgreSQL — Full Text Search; `SELECT … FOR UPDATE SKIP LOCKED`.
