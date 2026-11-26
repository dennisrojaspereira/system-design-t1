# System Design na Prática — Aula 01

**Encurtador de links: do MVP à escala global** · 25/11 · 19h–21h

Material da primeira aula: um encurtador de links que evolui em seis estágios, com Grafana mostrando o efeito de cada decisão.

| Arquivo | O que é |
|---|---|
| [ROTEIRO.md](ROTEIRO.md) | Roteiro da aula, minuto a minuto, com as contas e os trade-offs |
| [LABS.md](LABS.md) | Os seis laboratórios, passo a passo |
| `app/` | O encurtador (Go, um arquivo), com métricas Prometheus |
| `k6/` | Testes de carga |
| `observability/` | Prometheus e o dashboard do Grafana |
| `labs/` | Um `.env` por estágio da arquitetura |
| `ui/` | Interface visual: criar link, testar o redirect e ver um resumo das métricas (http://localhost:8090; para trocar a porta, `UI_PORT`) |

## Pré-requisitos

- Docker com Compose (Docker Desktop, OrbStack ou equivalente), com pelo menos 4 CPUs e 4 GB.
- `make`. Sem ele, veja "Sem make" abaixo.
- Portas livres: 3000 (Grafana), 9090 (Prometheus), 8080 (aplicação) e 8000 (balanceador). Para trocar, defina `GRAFANA_PORT`, `PROM_PORT`, `APP_PORT` ou `LB_PORT` no `.env`.

## Começando

```bash
make lab1
```

```bash
make load SCRIPT=ramp MAX_RPS=3000
```

Prefere clicar a digitar? Suba o painel e faça tudo por uma página só — trocar de lab, aplicar carga, derrubar serviços, editar o `.env` e consultar endereços e senhas:

```bash
make painel        # http://localhost:7070  (sem make: python3 painel/server.py)
```

Abra http://localhost:3000 — o dashboard **Encurtador de links — Aula 01** é a página inicial, sem login.

A primeira subida compila a aplicação e leva alguns minutos. O banco já nasce com 100 mil links (`s-1` a `s-100000`).

## A evolução

```
Lab 1  cliente → app → Postgres
Lab 2  ... trocando a forma de gerar o código curto
Lab 3  cliente → app → Redis → Postgres
Lab 4  cliente → nginx → 3 apps → Redis + Postgres
Lab 5  ... leituras numa réplica do Postgres
Lab 6  ... contando cliques: síncrono × assíncrono
```

Cada `make labN` copia `labs/labN.env` para `.env` e recria os containers. Para um experimento dentro do lab, edite o `.env` e rode `make apply`.

## A API

```bash
curl -s -XPOST localhost:8080/api/links -d '{"url":"https://technapratica.com.br"}'
```

```bash
curl -i localhost:8080/s-42
```

```bash
curl -s localhost:8080/api/links/s-42
```

## Sem make

```bash
cp labs/lab1.env .env
docker compose --profile ha --profile replica --profile tools down --remove-orphans
docker compose build app1
docker compose up -d --wait
docker compose run --rm -e MAX_RPS=3000 k6 run /scripts/ramp.js
```

## Limites deste laboratório

- Tudo roda numa máquina só: aplicação, banco e gerador de carga disputam as mesmas CPUs. Os números absolutos não valem para produção; as curvas, sim.
- O Postgres tem a CPU limitada de propósito, e a réplica tem atraso artificial.
- A fila de analytics é em memória: perde eventos se o processo morrer. É o ponto de partida da discussão, não a resposta.
