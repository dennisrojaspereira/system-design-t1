# Laboratórios — Aula 01: Encurtador de links

Seis laboratórios, um por decisão de arquitetura. Em todos o ciclo é o mesmo:

1. **Suba o estágio** (`make labN`).
2. **Aplique carga** (`make load ...`).
3. **Olhe o Grafana** (http://localhost:3000) e ache o problema.
4. **Mude uma coisa** e veja o gráfico mudar.

O histórico de métricas é mantido entre os labs: ao trocar de estágio, dá para ver o antes e o depois no mesmo gráfico.

> **Sobre os números.** Os valores de referência foram medidos num notebook com Docker de 4 CPUs. Na sua máquina eles serão outros — o que importa é o formato da curva. O Postgres roda de propósito com 0,2 CPU (`PG_CPUS`), para o gargalo aparecer com pouca carga.

| Lab | Tema | Problema que você vai ver | Ao vivo? |
|---|---|---|---|
| 1 | MVP | O banco satura; latência e erros disparam | sim |
| 2 | Geração de IDs | Colisões crescem conforme o espaço de códigos enche | demo curta |
| 3 | Cache | Cache lento derruba tudo; cache frio devolve a carga ao banco | sim |
| 4 | Disponibilidade | Instância morre; banco morre; quem sobrevive? | sim |
| 5 | Banco de dados | Réplica atrasada: "criei o link e deu 404" | para casa |
| 6 | Analytics | Contar clique na requisição transforma leitura em escrita | para casa |

Comandos de carga (todos aceitam variáveis):

```bash
make load SCRIPT=read  RPS=500 DURATION=2m      # redirects constantes
make load SCRIPT=ramp  MAX_RPS=3000             # rampa em 6 degraus de 30s
make load SCRIPT=write RPS=200 DURATION=2m      # criação de links
make load SCRIPT=mixed RPS=300 WRITE_RPS=20     # leitura + "criei e cliquei"
```

`SKEW` controla a popularidade dos links: `SKEW=1` é acesso uniforme; o padrão (8) concentra o tráfego em poucos links.

---

## Lab 1 — O MVP e o primeiro gargalo

**Arquitetura:** cliente → 1 aplicação → 1 Postgres. Sem cache, sem réplica.

```bash
make lab1
make load SCRIPT=ramp MAX_RPS=3000
```

**Painéis:** *Requisições por segundo, por status* · *Latência do redirect* · *Pool de conexões* · *Espera por conexão*.

**O que observar**

- Até ~2.000 req/s tudo responde 302 em poucos milissegundos.
- Perto de 2.500 req/s o pool chega a 10/10, a *espera por conexão* sobe e o p99 passa de 1 segundo.
- Aparecem 503 e o k6 passa a descartar iterações: o sistema não acompanha mais a taxa pedida.

Referência: a 3.000 req/s, 97% de sucesso e p95 de 1,1 s.

**Experimentos**

1. Edite `.env`, troque `DB_POOL_MAX=10` por `50`, rode `make apply` e repita a rampa. Melhorou? Por quê? (Pool maior não cria CPU no banco.)
2. Troque `PG_CPUS=0.2` por `0.5` no `.env` (acrescente a linha) e repita. Isso é escalar verticalmente.

**Perguntas**

- A estimativa da aula pede ~4.000 req/s de média e 20.000 de pico. Este desenho aguenta?
- Qual é o recurso que acaba primeiro? Como você sabe?

---

## Lab 2 — Geração de IDs

**Arquitetura:** a mesma do Lab 1; muda só como o código curto nasce.

```bash
make lab2                                # ID_STRATEGY=random, CODE_LENGTH=3
make load SCRIPT=write RPS=400 DURATION=2m
```

**Painéis:** linha *Geração de IDs* — *Colisões de código* e *Taxa de colisão*.

**O que observar**

- Com 3 caracteres existem só 62³ = 238.328 códigos. A taxa de colisão sobe sem parar conforme o espaço enche (referência: 9% depois de 24 mil links).
- Cada colisão é um INSERT desperdiçado e uma nova tentativa: a criação fica mais lenta com o tempo.

**Experimentos** — edite `.env` e rode `make apply` entre cada um:

1. `CODE_LENGTH=7` → 62⁷ ≈ 3,5 trilhões. As colisões somem. Quanto tempo até voltarem a incomodar?
2. `ID_STRATEGY=counter` → sem colisão, mas olhe *Latência das queries*: apareceu `id_next`, uma ida extra ao primário por link. E os códigos:
   ```bash
   curl -s -XPOST localhost:8080/api/links -d '{"url":"https://exemplo.com"}'
   ```
   Crie três seguidos. Dá para adivinhar o próximo? Isso é um problema?
3. `ID_STRATEGY=range` → cada instância reserva 1.000 IDs de uma vez. `id_block` aparece uma vez a cada mil links. Reinicie a aplicação (`docker compose restart app1`): o que acontece com o resto do bloco?

**Perguntas**

- Qual estratégia continua funcionando com 3 instâncias? E com 3 regiões?
- Que estratégia você usaria se o código não pudesse ser adivinhável?

---

## Lab 3 — Cache

**Arquitetura:** cliente → aplicação → **Redis** → Postgres (cache-aside).

```bash
make lab3
make load SCRIPT=ramp MAX_RPS=3000
```

**Painéis:** linha *Cache* · *Latência do redirect* · *Quem responde o redirect: cache × banco*.

**O que observar**

- A mesma rampa que quebrou o MVP agora passa: referência de 100% de sucesso e p95 abaixo de 5 ms a 3.000 req/s.
- O *hit ratio* começa baixo e sobe: cache frio não ajuda ninguém.
- Os SELECTs no banco ficam quase constantes enquanto o tráfego cresce.

**Experimento 1 — cache lento é pior que cache fora do ar**

```bash
make load SCRIPT=read RPS=1000 DURATION=90s
# em outro terminal, no meio do teste:
docker compose pause redis      # o Redis congela, sem recusar conexão
docker compose unpause redis    # 15 segundos depois
```

O p99 vai para perto de 5 segundos e a vazão despenca: cada requisição espera o timeout de 1 s do Redis antes de ir ao banco. Agora troque `REDIS_TIMEOUT_MS=1000` por `50` no `.env`, rode `make apply` e repita. O que mudou?

**Experimento 2 — cache frio**

```bash
make load SCRIPT=read RPS=1500 DURATION=90s
docker compose exec redis redis-cli FLUSHALL    # no meio do teste
```

Olhe *Quem responde o redirect*: a carga volta inteira para o banco de uma vez.

**Experimento 3 — o que o cache não resolve**

```bash
make load SCRIPT=read RPS=1500 SKEW=1 DURATION=2m
```

Com acesso uniforme sobre 100 mil links, o hit ratio demora muito mais para subir. Cache funciona porque o tráfego real é concentrado.

**Desafio:** requisições para códigos que não existem nunca entram no cache e sempre batem no banco. Implemente *negative caching* em `resolve()` (`app/main.go`).

---

## Lab 4 — Disponibilidade

**Arquitetura:** cliente → **nginx** → 3 aplicações → Redis + Postgres.

```bash
make lab4
make load SCRIPT=read RPS=1500 DURATION=3m
```

**Painéis:** linha *Disponibilidade* · *Erros (5xx)* · *Falhas vistas pelo cliente*.

**Experimento 1 — morre uma instância**

```bash
docker compose kill app2
docker compose start app2     # uns 20 segundos depois
```

*Instâncias no ar* cai para 2, o tráfego se redistribui e o cliente quase não percebe (referência: 99,98% de sucesso). O balanceador reenvia para outra instância a requisição que falhou.

**Experimento 2 — morre o banco**

```bash
docker compose kill pg
```

- Redirects de links que estão no cache continuam funcionando (referência: ~85% do tráfego).
- Links fora do cache e toda criação de link respondem 503.
- Suba de volta com `docker compose start pg`.

Leitura e escrita têm disponibilidades diferentes. Qual das duas o negócio precisa mais?

**Experimento 3 — health check agressivo demais**

Em `lb/nginx.conf`, troque `proxy_next_upstream error timeout;` por `proxy_next_upstream error timeout http_503;`, rode `docker compose restart lb` e repita o experimento 2. O balanceador passa a tratar todo 503 como instância morta, tira as três da rotação e até os links em cache viram 502. Uma falha parcial virou indisponibilidade total.

**Perguntas**

- Liste os pontos únicos de falha que sobraram.
- Com 3 instâncias de 99,9% cada, qual a disponibilidade da camada de aplicação? E do sistema inteiro, com um banco só?

---

## Lab 5 — Banco de dados: réplica de leitura

**Arquitetura:** escritas no primário; leituras numa **réplica** com 2 s de atraso proposital.

```bash
make lab5                      # começa com o cache desligado
make load SCRIPT=mixed RPS=300 WRITE_RPS=20 DURATION=2m
```

**Painéis:** linha *Leitura do que acabei de escrever* · *Lag de replicação* · *Transações por segundo no Postgres*.

**O que observar**

- As leituras saíram do primário: as transações agora aparecem em `replica`.
- O cenário "criei e cliquei" recebe 404 em 100% das vezes: a réplica ainda não tem o link.
- *Lag de replicação* fica em torno de 2 s.

**Experimentos**

1. Ligue o cache: `CACHE_ENABLED=true` no `.env`, `make apply`, repita a carga. As leituras obsoletas vão a zero — a criação grava o link no cache (*write-through*) e a leitura seguinte nem chega à réplica.
2. Mude `REPLICA_APPLY_DELAY` para `200ms`. Para valer, é preciso recriar a réplica:
   ```bash
   docker compose rm -sf pg-replica && docker volume rm sd-aula01_pgreplica && make apply
   ```
   Com o cache desligado, que fração das leituras ainda falha?

**Perguntas**

- Que outras formas existem de garantir "leio o que escrevi"? Qual o custo de cada uma?
- 6 bilhões de links cabem num primário só? Quando a réplica deixa de bastar e entra o sharding? Por qual chave?

---

## Lab 6 — Analytics

**Arquitetura:** a do Lab 4, agora contando cliques.

```bash
make lab6                      # ANALYTICS_MODE=sync
make load SCRIPT=read RPS=150 DURATION=1m
```

**Painéis:** linha *Analytics* · *Latência do redirect* · *Linhas lidas × escritas por segundo*.

**O que observar — modo síncrono**

- A 150 req/s funciona, mas o redirect já ficou lento (referência: p95 de 214 ms). Cada clique virou um UPDATE no primário, dentro da requisição.
- O cache deixou de proteger o banco: toda leitura agora é também uma escrita.
- Suba para `RPS=1500`: colapso. Referência: 64% de sucesso, p99 de 2,5 s, quase nenhum clique gravado.

**Agora o modo assíncrono**

Troque `ANALYTICS_MODE=sync` por `async` no `.env`, rode `make apply` e repita as duas cargas.

- O redirect só coloca o clique numa fila em memória; um lote por segundo vai para uma tabela de eventos.
- Referência a 1.500 req/s: 100% de sucesso, p95 de 35 ms, 1.500 cliques/s gravados e fila perto de zero.

**Experimentos**

1. Com carga rodando, mate uma instância sem aviso: `docker compose kill -s KILL app1`. Quantos cliques se perderam? Isso é aceitável para analytics? E para cobrança por clique?
2. Confira a contagem de um link: `curl -s localhost:8080/api/links/s-1`.

**Perguntas**

- O que você trocaria na fila em memória para não perder cliques? Qual o custo?
- A tabela de eventos cresce 345 milhões de linhas por dia na escala da aula. Onde esses dados deveriam morar?

---

## Encerrando

```bash
make down     # para tudo, mantém os dados
make reset    # apaga banco e histórico de métricas
```
