# Roteiro — Aula 01: Encurtador de links, do MVP à escala global

**Quando:** 25/11, 19h–21h · **Setor:** SaaS e plataformas
**Tópicos:** requisitos · estimativas · geração de IDs · cache · disponibilidade · banco de dados · analytics

## Objetivo

É a aula que fixa o método do curso. Ao final, o aluno consegue:

1. Aplicar o roteiro **Entenda → Estime → Desenhe → Evolua** num problema novo.
2. Fazer as contas de carga, armazenamento e cache e usá-las para justificar o desenho.
3. Defender três decisões com trade-offs: como gerar o ID, onde pôr cache, como contar cliques.
4. Ler um dashboard e apontar o recurso que saturou.

## Antes da aula

- [ ] `make reset && make lab1` e conferir o Grafana em http://localhost:3000.
- [ ] Rodar uma rampa de aquecimento, para as imagens já estarem em cache.
- [ ] Deixar três janelas prontas: slides, Grafana (modo TV, últimos 15 min) e terminal.
- [ ] Enviar o repositório aos alunos com o pedido de rodarem `make lab1` antes.

## Linha do tempo

| Horário | Bloco | Min | Lab |
|---|---|---|---|
| 19:00 | Abertura e o método | 10 | — |
| 19:10 | 1. Entenda: requisitos | 15 | — |
| 19:25 | 2. Estime: números | 15 | — |
| 19:40 | 3. Desenhe: o MVP | 15 | Lab 1 ao vivo |
| 19:55 | 4. Evolua: geração de IDs | 15 | Lab 2, demo curta |
| 20:10 | Evolua: cache | 15 | Lab 3 ao vivo |
| 20:25 | Evolua: disponibilidade | 15 | Lab 4 ao vivo |
| 20:40 | Evolua: banco de dados | 10 | Lab 5, mostrar o gráfico |
| 20:50 | Evolua: analytics | 5 | Lab 6, para casa |
| 20:55 | Fechamento e desafio | 5 | — |

Se o tempo apertar, corte a demo do Lab 2 (fica só o slide) e mantenha os labs 1, 3 e 4: são os que mostram o ciclo "quebra → conserta → quebra de outro jeito".

---

## 19:00 — Abertura e o método (10 min)

- Pergunta de abertura: "Quem já travou diante do quadro em branco?"
- O curso não ensina diagramas prontos. Ensina um roteiro que se repete em toda aula.
- Os quatro passos: **Entenda** (requisitos), **Estime** (números), **Desenhe** (arquitetura), **Evolua** (escala e trade-offs).
- Por que o encurtador: o domínio cabe numa frase, então sobra atenção para o método.
- Combinado da aula: toda afirmação de arquitetura vai ser testada no Grafana.

## 19:10 — 1. Entenda: requisitos (15 min)

Conduza como entrevista: os alunos perguntam, você responde como "dono do produto".

**Funcionais**

- Dada uma URL longa, devolver uma curta.
- Ao acessar a curta, redirecionar para a longa.
- Ver quantos cliques cada link teve.

**Fora do escopo** (dizer em voz alta o que não vamos fazer)

- Login e painel de usuário, links personalizados, expiração, QR code, detecção de abuso.

**Não funcionais**

- Redirect rápido: p99 abaixo de 100 ms.
- Alta disponibilidade no redirect: 99,99%. Criar link pode ser 99,9%.
- Link criado nunca se perde nem muda de destino.
- Código curto e não trivialmente adivinhável.
- Contagem de cliques pode atrasar alguns segundos.

**Ponto a martelar:** os requisitos não funcionais são diferentes para leitura e escrita. Essa assimetria decide quase todo o desenho.

**Pergunta para a turma:** 301 ou 302? (301 o navegador guarda: menos carga, mas você perde os cliques. 302 passa sempre por você.)

## 19:25 — 2. Estime: números (15 min)

Faça as contas no quadro, arredondando sem medo.

| Premissa | Valor |
|---|---|
| Links novos por mês | 100 milhões |
| Leitura : escrita | 100 : 1 |
| Retenção | 5 anos |
| Tamanho por link | ~500 bytes |

| Conta | Resultado |
|---|---|
| Escritas: 100 M ÷ 2,6 M segundos | **~40 por segundo** |
| Leituras: 40 × 100 | **~4.000 por segundo** |
| Pico (5×) | **~20.000 por segundo** |
| Links em 5 anos: 100 M × 60 | **6 bilhões** |
| Armazenamento: 6 bi × 500 B | **~3 TB** |
| Redirects por dia: 4.000 × 86.400 | **~345 milhões** |
| Cache: 20% dos acessos do dia × 500 B | **~35 GB** |
| Tamanho do código: 62⁶ = 56,8 bi · 62⁷ = 3,5 tri | **7 caracteres** |

**O que os números dizem**

- É um sistema de leitura. A escrita é quase irrelevante em volume.
- 3 TB cabem num banco só; 20.000 leituras por segundo num banco só, não com folga.
- 35 GB de cache cabem em memória. Cachear é viável e barato.

## 19:40 — 3. Desenhe: o MVP (15 min) · Lab 1

**API**

```
POST /api/links   {"url": "..."}   → 201 {"code": "aZ3x9Kp", "short_url": "..."}
GET  /{code}                        → 302 Location: <url longa>
GET  /api/links/{code}              → 200 {"url": "...", "clicks": 123}
```

**Modelo de dados:** uma tabela `links(code PK, url, created_at, clicks)`.

**Desenho:** cliente → aplicação → Postgres. Mostrar `app/main.go`: é um arquivo.

**Demo**

```bash
make lab1
make load SCRIPT=ramp MAX_RPS=3000
```

Narre o Grafana enquanto a rampa sobe: primeiro tudo verde; depois o pool chega a 10/10; a espera por conexão sobe; o p99 dispara; aparecem os 503.

**Fala-chave:** "A estimativa pedia 4 mil por segundo de média. Nosso MVP morreu antes de 3 mil. E repare: quem acabou primeiro não foi a aplicação, foi o banco."

Pergunte antes de avançar: "Aumentar o pool resolve?" (Não: pool maior não cria CPU no banco.)

## 19:55 — 4. Evolua: geração de IDs (15 min) · Lab 2

Apresente as três famílias e peça à turma os problemas de cada uma.

| Estratégia | Vantagem | Custo |
|---|---|---|
| Aleatório + checar colisão | Não adivinhável, sem coordenação | Colisões crescem com a ocupação |
| Contador central + base62 | Sem colisão, códigos curtos | Ida extra ao banco, previsível, ponto único |
| Blocos de IDs por instância | Sem colisão, sem ida ao banco por link | Buracos na sequência, ainda previsível |
| Hash da URL | Mesma URL, mesmo código | Colisão ao truncar; impede dois links para a mesma URL |

**Demo curta:** `make lab2`, `make load SCRIPT=write RPS=400`, mostrar a taxa de colisão subindo com 3 caracteres. Trocar para `CODE_LENGTH=7` e ver sumir.

**Fala-chave:** "Colisão não é azar, é aritmética. 62⁷ parece infinito até você calcular quanto do espaço vai estar ocupado em 5 anos."

**Trade-off para discutir:** blocos resolvem a escala; para não ser adivinhável, embaralha-se o ID (permutação ou cifra) antes do base62.

## 20:10 — Evolua: cache (15 min) · Lab 3

**Desenho:** cache-aside. Lê do Redis; se não achar, lê do banco e grava no Redis.

**Demo**

```bash
make lab3
make load SCRIPT=ramp MAX_RPS=3000
```

A mesma rampa que derrubou o MVP passa com p95 abaixo de 5 ms. Mostre o hit ratio subindo e os SELECTs estabilizando.

**Depois quebre:** com carga constante, `docker compose pause redis`. O p99 vai para perto de 5 s.

**Fala-chave:** "Um cache lento é pior que um cache fora do ar. Fora do ar, a conexão é recusada na hora. Lento, cada requisição espera o timeout inteiro."

Ajuste `REDIS_TIMEOUT_MS=50`, repita, mostre a diferença.

**Para discutir:** TTL, invalidação (aqui o link é imutável, e isso simplifica tudo), cache frio após reinício, códigos inexistentes que nunca entram no cache.

## 20:25 — Evolua: disponibilidade (15 min) · Lab 4

**Contas rápidas:** 99,9% são 8,7 horas fora por ano; 99,99% são 52 minutos. Componentes em série multiplicam; em paralelo, somam noves.

**Demo**

```bash
make lab4
make load SCRIPT=read RPS=1500 DURATION=3m
docker compose kill app2
```

O cliente quase não percebe. Depois: `docker compose kill pg`.

**Fala-chave:** "Olhem: o banco morreu e 85% dos redirects continuam funcionando. Leitura e escrita têm disponibilidades diferentes — e era isso que o requisito pedia."

**Se sobrar tempo:** o experimento do `http_503` no nginx, em que o health check transforma falha parcial em total.

**Pergunta para a turma:** quais pontos únicos de falha ainda existem? (Banco, Redis, balanceador, a região.)

## 20:40 — Evolua: banco de dados (10 min) · Lab 5

- Réplica de leitura para escalar o que o cache não pegou.
- Mostre o gráfico do Lab 5 já rodado: 100% de 404 no "criei e cliquei" com 2 s de lag.
- Solução vista no lab: gravar no cache ao criar (write-through).
- Quando a réplica não basta: sharding por código. 6 bilhões de linhas e 3 TB ainda cabem num primário; o que força o sharding costuma ser a escrita ou a operação (backup, migração), não o tamanho.
- SQL ou NoSQL? O acesso é sempre por chave. Um chave-valor distribuído serve bem; o relacional serve até bem longe e é mais simples de operar no começo.

## 20:50 — Evolua: analytics (5 min) · Lab 6

- O jeito ingênuo: `UPDATE clicks = clicks + 1` em cada redirect.
- Efeito: toda leitura virou escrita, e o cache não protege mais o banco. No lab, colapsa a 1.500 req/s.
- O jeito certo: o redirect só publica um evento; a contagem acontece depois, em lote.
- Trade-off: a contagem atrasa e, com fila em memória, pode perder cliques. Fila durável custa latência e operação.
- Fica como lab para casa.

## 20:55 — Fechamento (5 min)

**Recapitule o caminho no quadro:** MVP → IDs → cache → réplicas e balanceador → réplica de leitura → eventos.

**As três ideias para levar**

1. Os números decidem o desenho: "100 leituras para 1 escrita" explicou quase todas as escolhas.
2. Toda peça nova resolve um problema e cria outro. Nomear o problema novo é o trabalho.
3. Sem métrica não há discussão de arquitetura, só opinião.

**Desafio da semana**

- Fazer os labs 5 e 6.
- Implementar negative caching (desafio do Lab 3).
- Responder por escrito: "Como este desenho muda para atender três continentes com p99 abaixo de 100 ms?"

**Gancho para a aula 02** (plataforma de cursos e vídeo sob demanda): lá o problema deixa de ser a chave pequena e passa a ser o objeto grande.

---

## Perguntas prováveis

| Pergunta | Resposta curta |
|---|---|
| Por que não usar UUID? | 36 caracteres não é "curto". Dá para truncar, e aí voltam as colisões. |
| E se duas pessoas encurtarem a mesma URL? | Dois códigos diferentes. Deduplicar exige índice na URL e mistura as estatísticas. |
| CDN resolve? | Ajuda no redirect (cache na borda), mas a borda precisa reportar os cliques. |
| Como apagar um link malicioso? | Apaga no banco e invalida no cache; com TTL longo, a invalidação ativa é obrigatória. |
| E multirregião? | Leitura local com réplica e cache por região; escrita numa região só, ou IDs em blocos por região. |
| Qual banco em produção? | Depende do time. O padrão de acesso é chave-valor; relacional com réplicas vai longe. |

## Se a demo falhar

- Grafana vazio: confira `make ps` e http://localhost:9090/targets.
- Rampa não quebra o MVP: a máquina é mais forte; suba `MAX_RPS` ou baixe `PG_CPUS` no `.env`.
- Rampa quebra cedo demais: suba `PG_CPUS=0.5`.
- Tudo travou: `make reset && make lab1` leva cerca de um minuto com as imagens em cache.
