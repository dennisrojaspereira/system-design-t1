# Laboratórios — Aula 02: Plataforma de cursos e vídeo sob demanda

Seis laboratórios e dois casos de investigação. O ciclo é o da Aula 1:

1. **Suba o estágio** (`make labN`).
2. **Provoque** — derrube uma conexão, mate um worker, estrangule a banda.
3. **Olhe** o painel do laboratório (http://localhost:8090) e o Grafana (http://localhost:3000).
4. **Mude uma coisa** e veja o que muda.

O histórico de métricas e os vídeos são mantidos entre os labs.

> **Sobre os números.** Os valores de referência foram medidos numa máquina com 4 CPUs. Cada worker tem 1 CPU de propósito, para a capacidade de encode virar conta de slots. O vídeo de exemplo (90 s) leva cerca de 50 s para ser processado; o clipe da rajada (12 s), cerca de 7 s.

| Lab | Tema | Problema que você vai ver | Ao vivo? |
|---|---|---|---|
| 1 | Upload direto e retomável | Conexão cai em 60%; retomar sem criar outro asset | sim |
| 2 | Fila e workers | Worker morto, worker zumbi, rajada que vira fila | sim |
| 3 | HLS e ABR | Banda cai: com ABR a qualidade desce; sem ABR, trava | sim |
| 4 | CDN e acesso privado | Origem distante; autenticação na chave de cache | sim |
| 5 | Progresso entre dispositivos | Checkpoint antigo faz a posição voltar | demo curta |
| 6 | Publicação e rollback | Pacote incompleto no ar; voltar de versão | para casa |
| A, B | Investigue o play | Sintoma sem causa | fechamento |

---

## Lab 1 — Upload direto, retomável e privado

**Arquitetura:** navegador → API (3 chamadas pequenas) · navegador → storage (os bytes).

```bash
make lab1
```

Abra a aba **Upload**, escolha a aula "Plataforma de vídeo sob demanda", clique em **Usar vídeo de exemplo** (58 MB, 12 partes de 5 MiB) e em **1. Enviar**.

**O que observar**

- A API recebe `POST /api/uploads` e `POST …/parts`. Os `PUT` das partes vão para `/s3/…`, direto ao storage, cada um com sua URL assinada.
- Com 8 de 12 partes, a conexão é derrubada. O asset continua em `UPLOADING`.
- **2. Retomar o que faltou**: `GET /api/uploads/{id}` pergunta ao storage o que já chegou; a API assina URLs só para as partes que faltam. Repare que podem aparecer 9 partes gravadas, não 8: uma estava em trânsito quando o cliente desistiu. O storage é a fonte da verdade, não o cliente.
- **3. Repetir o complete**: a resposta é `já estava concluído`. Nenhum asset novo, nenhum job novo.

Referência: no Grafana, painel *Uploads* — 1 `created`, 1 `completed`, 1 `complete_repeated`.

**Experimentos**

1. Tente concluir antes da hora: derrube a conexão e chame o complete com `curl -XPOST localhost:8080/api/uploads/<id>/complete`. A API responde 409 e diz quantas partes faltam — ela lê a lista do storage, não confia na do cliente.
2. Troque `PART_SIZE_MB=5` por `16` no `.env`, `make apply`, e envie de novo. Quantas partes? O que muda no custo de uma retomada? (No S3, 5 MiB é o mínimo por parte e 10.000 é o máximo de partes.)
3. Abra o DevTools, aba Rede, e confira: nenhuma requisição para `/api/` carrega megabytes.

**Perguntas**

- A URL assinada vazou num log. O que alguém consegue fazer com ela, e por quanto tempo?
- O professor fechou o notebook em 60% e nunca mais voltou. Quem apaga as partes órfãs? (Regra de lifecycle para multipart incompleto.)
- Por que a chave do objeto é escolhida pela API (`uploads/a42/original.mp4`) e não pelo cliente?

---

## Lab 2 — Processamento assíncrono: fila, lease e backpressure

**Arquitetura:** storage raw → fila (tabela `jobs`) → 2 workers → storage vod → `READY`.

```bash
make lab2
```

### 2a. O worker morre no meio do encode

Na aba **Upload**, envie o vídeo de exemplo com "Derrubar a conexão" em `0`. Vá para **Fila e workers**, espere o estado `PROCESSING` e rode:

```bash
make kill-worker
```

**O que observar**

- O job continua `PROCESSING`, com a lease contando até zero. Ninguém avisou a fila que o worker morreu: é a lease que vence.
- Cerca de 20 s depois, o outro worker assume: **tentativa 2**, prefixo de saída novo (`a5/t2/`).
- O asset fica `READY` uma vez só. O lixo da tentativa 1 fica isolado no prefixo `t1/`.

`make apply` traz o worker morto de volta.

### 2b. O worker zumbi

Edite o `.env`: `ENCODE_EXTRA_S=45` e `make apply`. Enfileire um job e congele o worker que o pegou por mais tempo que a lease:

```bash
make burst N=1
make pause-worker        # pausa por 35 s e solta
make logs
```

**O que observar** — o worker antigo acorda achando que ainda é dono do job:

```
worker-2 | job 7 (asset 7) tentativa 2: início
worker-1 | job 7 tentativa 1: lease perdida: outra tentativa assumiu o job — nada foi publicado por esta tentativa
```

A proteção está em uma linha de SQL: `UPDATE jobs SET state = 'DONE' WHERE id = $1 AND attempt_id = $2`. Se outra tentativa assumiu, nenhuma linha muda. No Grafana, *Tentativas por resultado* mostra `lost_lease`.

Volte `ENCODE_EXTRA_S=0` e `make apply` antes de continuar.

### 2c. Rajada e backpressure

```bash
make burst N=18
```

**Painéis:** *Fila: jobs por estado* · *Idade do job mais antigo* · *Workers: ocupados × disponíveis* · *Publicação: enfileirado → READY*.

- Com 2 workers a ~7 s por job, a vazão é de ~17 jobs/min. Os 18 jobs chegam de uma vez: a fila enche e esvazia em cerca de um minuto. A **idade do mais antigo** sobe em rampa — é ela que o professor sente.
- Edite `WORKERS=4`, `make apply` e repita. A rampa encurta, mas não cai pela metade: numa máquina de 4 CPUs os workers já disputam CPU com o banco e o storage. Dobrar slots só dobra a vazão se houver máquina para eles.

Referência: 18 jobs, 2 workers → último `READY` em ~62 s. Com 4 workers → ~39 s.

**Perguntas**

- A meta é publicar em até 15 min (p95). Encode leva 8 min, validação 1 min. Quanto tempo um job pode esperar na fila?
- Você escalaria os workers pelo tamanho da fila, pela idade do job mais antigo ou pela CPU? Por quê?
- A fila é uma tabela do Postgres. O que precisa acontecer para valer a troca por SQS ou Kafka?
- Um arquivo corrompido derruba o ffmpeg toda vez. O que impede esse job de rodar para sempre? (`MAX_ATTEMPTS` → `FAILED`, a "DLQ" do lab.)

---

## Lab 3 — HLS e bitrate adaptativo

**Arquitetura:** player → borda → segmentos de 6 s em três variantes.

```bash
make lab3
```

Aba **Player e CDN** → aula "Encurtador de links" → **▶ Play**. A banda começa em 8 Mb/s.

**Antes de mexer, olhe o pacote.** Com o cookie de acesso (veja o README), baixe as playlists:

```
master.m3u8          → as três variantes: 480p / 1 Mb/s, 720p / 2,5 Mb/s, 1080p / 5 Mb/s
1080p/index.m3u8     → init.mp4 + 15 segmentos de 6 s
```

**O que observar**

1. **8 Mb/s:** o player sobe para 1080p. Cada segmento tem ~3,8 MB (6 s × 5 Mb/s ≈ 30 Mb) e chega em ~4 s. O buffer se mantém.
2. Clique em **2 Mb/s**. A coluna *Total* do próximo segmento 1080p salta para ~15 s (30 Mb ÷ 2 Mb/s). O player percebe e desce para **480p**: segmentos de ~760 kB que chegam em ~3 s. Sem travamento.
3. Agora clique em **travar em 1080p** (ABR desligado), ainda em 2 Mb/s. O buffer drena, zera e o vídeo trava: cada 6 s de vídeo levam 15 s para chegar. Rebuffer ratio acima de 30%.

É a conta do slide, ao vivo: *"6 s a 5 Mb/s ≈ 30 Mb. Em 2 Mb/s leva ≈ 15 s. Se havia 8 s de buffer, faltam 7 s antes de o segmento chegar."*

**Experimentos**

1. Com ABR ligado, vá a **1 Mb/s**. O 480p (1 Mb/s nominal) fica no limite: o que o player faz? O que faltou na ladder?
2. Volte a **sem limite** e veja quanto tempo o player leva para subir de novo. Por que subir é mais lento que descer?

**Perguntas**

- Por que os keyframes precisam estar alinhados entre as variantes? (Veja `-g 60 -sc_threshold 0` em `app/worker.go`.)
- Segmentos de 2 s reagiriam mais rápido à queda de banda. O que você paga por isso?
- O player estima a banda pelo tempo de download dos segmentos. O que um MISS lento na borda faz com essa estimativa?

---

## Lab 4 — CDN, cache e acesso privado

**Arquitetura:** player → borda (confere o cookie, HIT ou MISS) → origem privada a 850 ms.

```bash
make lab4
```

### 4a. Cache frio × cache quente

Dê **▶ Play** na aula 1. Depois dê play de novo.

- 1ª vez: todas as requisições `MISS`, TTFB ~860 ms, *primeiro quadro* em torno de 5 s — o dobro do SLO de 2,5 s. São seis requisições em sequência (playlists, init, segmento), cada uma pagando a viagem até a origem.
- 2ª vez: tudo `HIT`, TTFB de 1 ms, primeiro quadro em ~0,2 s.

O sistema é o mesmo; só mudou a temperatura do cache. É por isso que vídeo popular funciona e vídeo novo falha em região fria.

### 4b. Acesso por matrícula

- Troque o aluno para **bia** (canto superior direito) e tente assistir à aula 1: `403 — sem matrícula neste curso`. Quem decide é a API de playback, não a busca.
- Sem cookie, a borda nega: `curl -i localhost:8081/vod/a1/t1/master.m3u8` → 403.
- Com o cookie do asset 1, peça um arquivo do asset 2: `assinatura não confere`. O acesso vale para um asset e tem prazo.

### 4c. A autenticação entrou na chave de cache

```bash
make load SCRIPT=playback VIEWERS=20 DURATION=1m
```

**Painéis:** *Hit ratio* · *Borda: segmentos por segundo* · *Tráfego: borda × origem* · *Idas à origem*.

Referência com `CACHE_KEY=path`: hit ratio de ~68% no primeiro minuto, subindo enquanto a carga durar; **17 idas à origem para 900 segmentos servidos**. Os MISS do começo são alunos que pediram o mesmo segmento frio ao mesmo tempo: a borda junta todos numa ida só à origem (*request collapsing*).

Agora o erro clássico. No `.env`, troque `CACHE_KEY=path` por `CACHE_KEY=path+cookie`, `make apply` e repita a carga.

- Hit ratio: **0%**. Cada sessão tem um token diferente, então cada sessão abre a própria entrada de cache.
- *Tráfego: borda × origem*: as duas linhas se encostam. A CDN virou um proxy caro.
- *Cache da borda*: cresce sem parar com cópias do mesmo segmento, até começar a expulsar.

A regra: **autorize pelo cookie, identifique o objeto pelo caminho.** O token não pode fazer parte da identidade do objeto em cache.

### 4d. A origem cai

Assista à aula 1 até o fim, para aquecer o cache. Clique em **Derrubar a origem** e dê play: tudo o que está em cache toca; um segmento que a borda nunca viu responde 502. Clique em **Esvaziar o cache** e tente de novo: nem o manifest carrega. "A origem caiu e só os vídeos novos falham" é exatamente isso.

**Perguntas**

- Com 1.667 GET/s na borda e 95% de hit, quantas requisições chegam à origem? E se o hit ratio cair para 80%?
- Por que `/vod/a42/t3/720p/seg_001.m4s` pode ter TTL de um ano?
- O cache da borda protege quem já está assistindo. Quem dá o primeiro play depende de quê? (Da API de playback: o plano de controle também precisa de redundância.)

---

## Lab 5 — Progresso e retomada entre dispositivos

**Arquitetura:** player → API → uma linha por aluno e aula no Postgres.

```bash
make lab5        # sobe com PROGRESS_MODE=lww: "última escrita vence"
```

Aba **Dois dispositivos**. Reproduza a história do slide:

1. **Celular:** ▶ Play, posição em 11:00, **Enviar checkpoint**.
2. **Notebook:** ▶ Play (retoma em 11:00), volte para 4:20 — o aluno quis rever um trecho —, **Enviar checkpoint**.
3. **Celular:** **Reenviar o último (atrasado)** — o checkpoint de 11:00 que ficou preso na rede do metrô.

**Com `lww`:** o servidor aceita e a posição volta para 11:00. O aluno abre o notebook amanhã e cai no lugar errado.

Agora troque para `PROGRESS_MODE=conditional` no `.env`, `make apply`, e repita os três passos.

**Com `conditional`:** o passo 3 recebe `409 recusado: servidor está em generation 2`. A posição fica em 4:20.

```sql
UPDATE progress SET pos_ms = :pos, seq = :seq
WHERE user_id = :user AND lesson_id = :lesson
  AND generation = :gen AND seq < :seq;
```

- `generation` sobe a cada play: a sessão anterior deixa de valer.
- `seq` cresce a cada checkpoint: dentro da sessão, um pacote atrasado ou repetido não sobrescreve um mais novo.
- Voltar o vídeo funciona, porque a regra compara **sequência**, não **posição**. "Só aceito posição maior" quebraria o seek para trás.

### Carga: o efeito do jitter

```bash
make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=false
make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=true
```

**Painéis:** *Checkpoints por segundo* · *Latência do checkpoint*.

A carga média é a mesma (60 escritas/s). Sem jitter, os 600 players disparam no mesmo instante: picos de 600 escritas e fila no pool de conexões. Com jitter, a linha fica plana.

Referência: mediana do checkpoint de ~44 ms sem jitter para ~1 ms com jitter.

**Perguntas**

- 10.000 alunos, um checkpoint a cada 30 s: quantas escritas por segundo? O Postgres aguenta? E com 1 milhão?
- O que você perde se guardar os checkpoints no Redis e gravar no Postgres a cada minuto? (Lembre do Lab 6 da Aula 1.)
- Por que "aula concluída" é um campo próprio e não "posição ≥ 95%"?

---

## Lab 6 — Publicação, versão e rollback

**Arquitetura:** worker → pacote em prefixo próprio → validação → troca do ponteiro da aula.

```bash
make lab6        # FAULT=drop_segment: o worker apaga um segmento depois de enviar o pacote
```

### 6a. Com validação

Envie o vídeo de exemplo para a aula 1 (ela já tem a v1 no ar). Em **Fila e workers**:

- Tentativa 1 falha: `validação: a6/t1/720p/seg_001.m4s referenciado na playlist e ausente no storage`.
- Tentativa 2 falha igual. O job vai para `FAILED`.
- A aula 1 continua tocando a **v1**. Os alunos não viram nada.

### 6b. Sem validação

No `.env`, `VALIDATE=false`, `make apply`, e envie de novo.

- O job fica `READY` e a v3 vira a versão ativa.
- Dê play, com ABR ligado: a playlist responde 200 e o segmento `720p/seg_001.m4s` responde **404**. Playlist 200 seguida de segmento 404 é a assinatura de uma publicação parcial.

### 6c. Rollback sem invalidar a CDN

Na aba **Catálogo**, clique em **ativar v1** na aula 1 e dê play de novo.

- O manifest volta a ser `/vod/a1/t1/master.m3u8`. Nada foi apagado nem invalidado: a URL mudou porque a versão faz parte do caminho.
- Quem já estava assistindo à v3 continua na URL da v3 até o fim da sessão; novas sessões recebem a v1.

**Perguntas**

- Por quanto tempo a v1 precisa ficar no storage depois que a v2 vai ao ar?
- O que a validação deste lab **não** pega? (Segmento presente mas corrompido; áudio fora de sincronia; duração errada.)
- Por que cada tentativa grava em um prefixo próprio, em vez de sobrescrever?

---

## Casos de investigação — fechamento da aula

Dois sintomas. A causa está no `.env`, mas a ideia é não olhar: **hipótese → experimento → evidência**. Use o HUD do player, a tabela de requisições, o DevTools e o Grafana.

### Caso A — "Demora a começar"

```bash
make caso-a
```

Dê play na aula 1 três ou quatro vezes. Rode também `make load SCRIPT=playback VIEWERS=20 DURATION=1m`.

- Qual é o primeiro quadro? Ele melhora na segunda vez, como no Lab 4?
- Qual é o TTFB dos segmentos? Qual é o hit ratio?
- Escreva duas hipóteses e o experimento que distingue uma da outra.

### Caso B — "Começa rápido e depois trava"

```bash
make caso-b
```

Dê play. Dez segundos depois, clique em **2 Mb/s**: o aluno entrou no metrô. Assista por mais 40 segundos.

- O primeiro quadro está dentro do SLO? E o hit ratio no segundo play?
- Compare: bitrate da variante × banda estimada × tempo total de download de um segmento.
- Qual métrica separa "problema de entrega" de "problema de adaptação"?

Feche com uma frase por grupo: **"mudamos X por causa de Y e verificamos o efeito medindo Z."**

<details>
<summary>Gabarito (abra depois de ouvir as hipóteses)</summary>

**Caso A.** Origem a 850 ms **e** `CACHE_KEY=path+cookie`. O primeiro quadro não melhora no segundo play porque cada play tem um token novo — e o token está na chave de cache. Evidências: `X-Cache: MISS` em toda requisição, mesmo repetida; TTFB ~860 ms; hit ratio 0%; *Tráfego: borda × origem* com as duas linhas juntas. Experimento que isola: repetir o mesmo segmento com duas sessões autorizadas — se cada sessão abre uma entrada, a chave está errada. Correção: `CACHE_KEY=path` + `make apply`. A distância até a origem continua lá, mas só o primeiro aluno paga.

**Caso B.** O player está travado em 1080p (`PLAYER_ABR=locked`): começa pela variante leve, sobe e nunca mais desce. Primeiro quadro rápido e hit ratio alto descartam borda e origem. Evidências: variante em 1080p com banda estimada de 2 Mb/s; segmentos de 3,8 MB levando ~15 s; buffer chegando a zero; rebuffer ratio alto. A conta: 30 Mb ÷ 2 Mb/s = 15 s para 6 s de vídeo. Na variante de 1 Mb/s, 6 Mb ÷ 2 Mb/s = 3 s. Correção: clicar em **ABR ligado** e ver o player descer para 480p sem travar.

</details>
