# Roteiro — Aula 02: Plataforma de cursos e vídeo sob demanda

**Quando:** 02/12, 19h–21h · **Setor:** educação e mídia
**Tópicos:** identidade e autorização · requisitos e SLOs · upload multipart · fila e workers · HLS e ABR · CDN e acesso privado · progresso entre dispositivos

Este roteiro segue o quadro do Excalidraw (versão de 28/11, 24 quadros) e diz, para cada quadro, **o que falar, o que mostrar e onde olhar**. Para não gastar tempo de aula esperando, cada demonstração tem três formas, da mais rápida para a mais real:

| Forma | Onde | Quando usar |
|---|---|---|
| **Clipe** | nos slides (toca sozinho) e em `videos/` | padrão da aula: zero espera |
| **Simulador** | `http://localhost:8090/simulador.html` ou o link publicado | a turma mexe, com o tempo acelerado |
| **Lab de verdade** | `http://localhost:8090` | quando o efeito é rápido (login, ABR) ou para casa |

## Objetivo

Ao final, o aluno consegue:

1. Separar **autenticação** (delegada a um IdP) de **autorização** (regra da plataforma, escrita como policy).
2. Separar **plano de controle** (API, metadados) de **plano de dados** (bytes de vídeo).
3. Desenhar um pipeline assíncrono que tolera entrega repetida: idempotência, lease e publicação condicional.
4. Fazer as contas de entrega e diagnosticar um play ruim separando origem, cache e adaptação.

## O quadro e o material: o que confere e o que eu mudaria

**Confere e já está coberto**

- Os 16 quadros principais têm slide, clipe ou lab correspondente (tabela abaixo).
- Os números batem: 128 PUTs, 12 slots, 30 Mb ÷ 2 Mb/s = 15 s, 1.667 → 83 GET/s, 333 writes/s, 1,35 GB/h.
- O trecho de `app/worker.go` do quadro 06 é o código real do `publish`.

**Sugestões de mudança no quadro**

1. **Quadro 00B diz "o login é simulado por X-User" e "IdP e OPA são propostas".** Não são mais: `make idp` sobe um Keycloak com login OIDC + PKCE de verdade, e o OPA avalia `policy/playback.rego` a cada play. Troque o rodapé por "No lab: Entrar com Keycloak; edite `policy/playback.rego` com tudo no ar".
2. **O tempo não fecha.** O plano do quadro 01 soma 120 min (45 + 25 + 7 + 27 + 16) e a abertura 00A–00C acrescenta 12. A linha do tempo abaixo cabe em 120: abertura em 7 min (00C vira uma pergunta dentro do 00B e o resto vai para os extras) e um minuto a menos em sete quadros.
3. **Quadro 01, "Plano de 120 min"**: atualize o texto para a divisão nova e acrescente `make demo` ao "Antes da aula".
4. **Quadro 02**: acrescente o IdP e o OPA ao desenho da faixa "Aplicação", e aponte para a aba **Arquitetura** do lab (C1–C4 animados, com os seis estágios).
5. **Quadro 12 (waterfall)**: os valores são "simulados" (HIT em 55 ms). No lab o HIT responde em ~1 ms e o MISS em ~855 ms. Use os números reais, ou diga que os do quadro incluem a rede do aluno.
6. **Quadro 15, Caso A**: o quadro fala em hit ratio de 40%. O `make caso-a` produz 0% (token na chave de cache), e o primeiro quadro fica em ~5,4 s nos dois plays. Ajuste o enunciado para "hit ratio próximo de zero, e não melhora no segundo play" — é a pista que leva à chave de cache.
7. **Quadro 09 (IDR)**: é o único sem apoio no lab. O encode usa GOP de 60 (IDR a cada 2 s), então todo corte de 6 s está alinhado. Mantenha como exercício de quadro ou mova para os extras.
8. **Quadro 10 (oito fichas)**: o simulador reproduz o jogo — aba *Player e ABR*, rede em 2 Mb/s, travar em 1080p. Faça com as fichas e confirme na tela.
9. **Falta um quadro "onde olhar"**. Sugestão: um quadro com os painéis do Grafana por tema (slide 26). Todo "Abrir Lab N ↗" ganha um par "Painel ↗".
10. **Falta o fechamento.** O debrief (16) termina nos casos. Acrescente os três aprendizados: separe controle de dados; assíncrono exige idempotência; cache é identidade.

## Antes da aula

- [ ] `make reset && make lab1` — a aula 1 fica `READY` em cerca de um minuto.
- [ ] `make demo` — espectadores simulados: o Grafana fica com dados em todos os painéis.
- [ ] `make idp` — Keycloak em http://localhost:8088 (`admin` / `admin`; alunas `ana` / `ana` e `bia` / `bia`).
- [ ] Abas abertas: slides · laboratório (http://localhost:8090) · simulador · Grafana (http://localhost:3000, últimos 15 min) · Keycloak · terminal.
- [ ] Enviar aos alunos o link do simulador. Quem quiser roda `make lab1` em casa.

De outra máquina da rede, troque `localhost` pelo IP da máquina que roda o lab (`make urls` mostra).

## Linha do tempo

| Horário | Quadro | Min | Slides | Mostrar | No Grafana |
|---|---|---|---|---|---|
| 19:00 | 00A · Conta própria ou IdP? | 4 | 2, 3, 5 | lab: **Entrar com Keycloak** (ou clipe) | Logins no IdP |
| 19:04 | 00B · Matrícula e policy no OPA (+ 00C em uma pergunta) | 3 | 4 | lab: play como bia → 403 | Play autorizado × negado · Decisões do OPA |
| 19:07 | 01 · Plataforma e SLOs | 8 | 6, 7 | — | Visão geral (linha dos SLOs) |
| 19:15 | 02 · Arquitetura e caminho dos dados | 6 | 8, 9, 12 | aba **Arquitetura**: ver evoluir (ou clipe) | — |
| 19:21 | 03 · Catálogo, busca e publicação | 6 | 10, 11 | lab: buscar `índices` | API por rota |
| 19:27 | 04 · Upload direto e retomável | 8 | 13, 14 | **clipe** · simulador *Upload* | Uploads |
| 19:35 | 05 · Processamento assíncrono | 8 | 15, 16, 17 | **clipe** · `make burst N=18` | Fila por estado · Idade do mais antigo |
| 19:43 | 06 · Disputa de workers | 5 | 15 | **simulador** *Fila*: matar e pausar worker | Tentativas (lost_lease) |
| 19:48 | 07 · HLS e bitrate adaptativo | 5 | 18 | lab: abrir `master.m3u8` | — |
| 19:53 | 08 · Você é o player | 5 | 19 | atividade no quadro | — |
| 19:58 | 09 · Onde dá para trocar de qualidade? | 4 | — | atividade no quadro | — |
| 20:02 | 10 · O jogo das oito fichas | 7 | 19, 20 | fichas + **clipe duplo** (ou lab, é rápido) | Bitrate · Travamentos |
| 20:09 | **Pausa** | 7 | | | |
| 20:16 | 11 · CDN, cache e acesso privado | 8 | 21, 22, 23 | **clipe** · simulador *CDN*: trocar a chave | Hit ratio · Borda × origem |
| 20:24 | 12 · Abra o waterfall do play | 7 | 23 | lab: DevTools + tabela de requisições | TTFB HIT × MISS |
| 20:31 | 13 · Progresso e retomada | 6 | 24 | — | Checkpoints por segundo |
| 20:37 | 14 · Qual checkpoint vence? | 5 | 25 | **clipe** · simulador *Dois dispositivos* | Checkpoints (stale) |
| 20:42 | 15 · Incidente final | 10 | 26, 27 | `make caso-a` e `make caso-b` | o painel que o grupo escolher |
| 20:52 | 16 · Debrief | 6 | 28 | gabarito no fim do [LABS.md](LABS.md) | — |
| 20:58 | folga | 2 | | | |

Se o tempo apertar: corte o quadro 09 e o caso A do incidente (o quadro 11 já mostrou a causa).

---

## 19:00 — 00A · Conta própria ou IdP? (4 min)

- Pergunta do quadro: "A Ana entra pela primeira vez. Onde criamos a conta e quem verifica que ela é mesmo a Ana?"
- Duas perguntas diferentes (slide 2): **autenticação** — é quem diz ser? — e **autorização** — o que pode fazer aqui? Só a segunda é nossa.
- Por que não construir (slide 3, uma linha por clique): senha, segundo fator, biometria, "esqueci a senha", força bruta, login social. **Biometria não é sua**: a digital fica no aparelho; você integra passkeys e recebe uma assinatura.
- **Mostrar:** no lab, tela de login → **Entrar com Keycloak** → `bia` / `bia`. Depois abra http://localhost:8088 → realm *cursos* → *Authentication → Policies*: senha, OTP, WebAuthn e passwordless já estão lá.
- Okta, Auth0, Cognito e Entra ID são o mesmo papel como serviço. Keycloak é o aberto, e é o que dá para subir do zero numa aula.
- Detalhe técnico para quem perguntar: OIDC Authorization Code + PKCE; a API valida o JWT com a chave pública do IdP (JWKS), sem segredo compartilhado. Vincule `(iss, sub)` ao `user_id` da plataforma.

## 19:04 — 00B · Matrícula e policy no OPA (3 min)

- Votação: Ana logada e matriculada → ? Bia logada e sem matrícula → ?
- A policy tem três linhas e nega por padrão (slide 4). A API monta o `input` com fatos verificados; o OPA decide; a API aplica.
- **Mostrar:** play como bia na aula 1 → `403 sem matrícula neste curso`.
- Pergunta que cobre o 00C: "O serviço de autorização caiu. A API libera ou nega?" Negar. Se sobrar um minuto: `docker compose stop opa`, play → negado; `docker compose start opa`.
- O resto do 00C (regra no serviço × OPA remoto × OPA local) fica nos três cliques do slide 4 ou nos extras.

## 19:07 — 01 · Plataforma e SLOs (8 min)

- Gancho: 10 mil alunos dão play às 20h, e uma professora envia 2 GB.
- Quatro jornadas (slide 6): Publicar, Encontrar, Assistir, Retomar. **SLOs:** play p95 < 2,5 s; publicação p95 < 15 min.
- Três contas (slide 7): 30 Gb/s na borda · 12 slots de encode · 333 escritas/s. Três gargalos, três unidades.
- No Grafana, a primeira linha do dashboard são esses SLOs, ao vivo com o `make demo`.

## 19:15 — 02 · Arquitetura e caminho dos dados (6 min)

- Desenhe as três faixas (slide 8). Pergunta: "Qual dos dois arquivos pesados precisa passar pela API?" Nenhum.
- **Mostrar:** aba **Arquitetura** → C2 → **▶ ver evoluir** (slide 9 tem o clipe). Seis estágios; o tracejado vermelho é o gargalo que o estágio seguinte resolve. É o mapa da aula.
- Fluxo de negócio → *Assistir* (slide 12): a linha azul grossa nunca toca a API.
- C1, C3 e C4 ficam para quem quiser abrir: contexto, componentes de API/worker/borda e os quatro trechos de código que sustentam as decisões.

## 19:21 — 03 · Catálogo, busca e publicação (6 min)

- Cenário do quadro: a busca mostra a aula nova, mas a matrícula do Rafa acabou de ser revogada. Quem decide no play? A API, na fonte autoritativa — é a policy do bloco anterior.
- Modelo (slide 11): `lessons.active_asset_id` aponta para a versão no ar. Publicação atômica: validar, `READY` e trocar o ponteiro na mesma transação.
- Busca começa no Postgres (`tsvector` + GIN). **Mostrar:** digitar `índices` no catálogo.

## 19:27 — 04 · Upload direto, retomável e privado (8 min)

- Contrato em três chamadas (slide 13). 2 GiB ÷ 16 MiB = 128 PUTs.
- **Mostrar:** clipe do slide 14 — cai em 60%, retoma, repete o complete. Narre: aparecem 9 partes gravadas, não 8; o storage é a fonte da verdade.
- Para a turma mexer: simulador → *Upload*, queda em 95%: reenvia dezenas de MB em vez de 2 GB.

## 19:35 — 05 · Processamento assíncrono (8 min)

- Cenário do quadro: o worker gravou o pacote e caiu antes de confirmar; a fila reenviou o job.
- Três mecanismos, um por clique (slide 15): chave idempotente, lease, publicação condicional.
- Backpressure (slide 16): 1,5 job/min de capacidade contra 3,6 na rajada; orçamento de fila de 6 min.
- **Mostrar:** clipe do slide 17, ou `make burst N=18` e os painéis *Fila por estado* e *Idade do mais antigo* (referência: 62 s com 2 workers, 39 s com 4).

## 19:43 — 06 · Disputa de workers (5 min)

- Atividade do quadro: A perde a lease, B assume com outro `attempt_id`, A volta e tenta publicar.
- **Mostrar:** simulador → *Fila* → Rajada → **Matar worker** → **Pausar worker (zumbi)**. Em dez segundos: lease vence, tentativa 2, e "recusado, attempt_id não confere".
- No lab de verdade (para casa): `make kill-worker` e `make pause-worker`.
- O código é o do quadro: `UPDATE … WHERE attempt_id = $2`. Zero linhas → nada publicado.

## 19:48 — 07 · HLS e bitrate adaptativo (5 min)

- "No primeiro GET o Rafa recebe texto, não vídeo." Quatro arquivos (slide 18): master, media playlist, init, segmentos. Ladder 480p / 720p / 1080p.
- **Mostrar:** no lab, dar play e abrir `master.m3u8` e `720p/index.m3u8` pelo DevTools.

## 19:53 — 08 · Você é o player (5 min) e 19:58 — 09 · Onde trocar de qualidade? (4 min)

- Atividades no quadro: a turma monta a sequência de GETs e decide se dá para subir de variante no próximo corte.
- Fato do lab para o 09: GOP de 60 quadros a 30 fps, um IDR a cada 2 s; todo corte de 6 s está alinhado nas três variantes.

## 20:02 — 10 · O jogo das oito fichas (7 min)

- Com as fichas: 8 s de buffer, rede de 2 Mb/s. 1080p: 30 Mb ÷ 2 = 15 s → 7 s de travamento. 480p: 6 Mb ÷ 2 = 3 s → sobra.
- **Mostrar:** clipe duplo do slide 20 (com ABR × preso em 1080p), ou ao vivo — este lab é rápido: play, **2 Mb/s**, depois **travar em 1080p**.
- No Grafana: *Player: bitrate em reprodução* e *Player: travamentos*.

## 20:09 — Pausa (7 min)

## 20:16 — 11 · CDN, cache e acesso privado (8 min)

- "Autorize pelo cookie, identifique pelo caminho" (slide 21). A versão está no caminho; o token fica fora da chave.
- A medida (slide 22): chave = caminho → 17 idas à origem para 900 segmentos. Chave = caminho + cookie → 0% de hit.
- **Mostrar:** clipe do slide 23 (frio ~5 s, quente ~0,2 s). Para a turma: simulador → *CDN* → iniciar carga → trocar a chave e ver o hit ratio despencar.

## 20:24 — 12 · Abra o waterfall do play (7 min)

- **Mostrar:** no lab, origem em **850 ms**, **Esvaziar o cache**, play com o DevTools aberto. A tabela *Requisições de mídia* é o waterfall: arquivo, HIT/MISS, TTFB, total.
- Teste de hipótese do quadro: repetir a mesma URL e comparar `X-Cache` e TTFB.
- No Grafana: *TTFB na borda: HIT × MISS* — a distância entre as linhas é a distância até a origem.

## 20:31 — 13 · Progresso (6 min) e 20:37 — 14 · Qual checkpoint vence? (5 min)

- Contrato e SQL condicional (slide 24, um clique por passo da história).
- **Mostrar:** clipe do slide 25, ou simulador → *Dois dispositivos*: alterne "última escrita vence" e "escrita condicional" e repita os mesmos três cliques.
- O teste determinístico do quadro 14 é exatamente essa sequência; rode no quadro trocando a ordem de entrega.
- 333 escritas/s com jitter: mediana de 44 ms sem jitter para 1 ms com.

## 20:42 — 15 · Incidente final (10 min) e 20:52 — 16 · Debrief (6 min)

- Grupos. `make caso-a` ("demora a começar") e `make caso-b` ("começa rápido e depois trava": dê play e, dez segundos depois, baixe a rede para 2 Mb/s).
- Roteiro (slide 27): sintoma → hipótese → experimento → decisão. Slide 26 lembra onde olhar.
- Debrief (slide 28): uma frase por grupo — "mudamos X por causa de Y e verificamos medindo Z" — e os três aprendizados.

---

## Extras (quadros 17–21 e 00C)

| Quadro | Slide | Apoio |
|---|---|---|
| 00C · Autorização distribuída | 4 (três cliques) | `AUTHZ=code` no `.env` + `make apply` troca o OPA pela regra no serviço |
| 17 · Do MP4 ao streaming adaptativo | 30 | aba Arquitetura, estágios 1 a 5 |
| 18 · Versionamento do pacote | — | Lab 6: `make lab6`, pacote incompleto e rollback |
| 19 · Quando a fila atrasa | 16 | simulador *Fila* com 12 workers, encode de 8 min, rajada de 18 |
| 20 · CDN fria e perda da origem | 23 | Lab 4d: **Derrubar a origem** com cache quente e frio |
| 21 · Quanto custa uma hora assistida | 29 | painel *Tráfego: borda × origem* |

## Referências

- OpenID Connect Core · OAuth 2.0 PKCE (RFC 7636) · WebAuthn.
- Open Policy Agent — integração com a aplicação.
- RFC 8216 — HTTP Live Streaming.
- AWS S3 — Multipart upload. Amazon CloudFront — cookies assinados; failover de origem.
- PostgreSQL — Full Text Search; `SELECT … FOR UPDATE SKIP LOCKED`.
