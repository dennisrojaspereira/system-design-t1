# Atalhos do laboratório. Tudo aqui é só `docker compose` — veja o README
# se preferir digitar os comandos na mão (ou estiver no Windows sem make).
# Usa `docker compose` (plugin) e cai para `docker-compose` (binário antigo) se precisar.
DC := $(shell docker compose version >/dev/null 2>&1 && echo docker compose || echo docker-compose)
ALL := --profile ha --profile replica --profile tools

SCRIPT ?= read
K6_VARS := RPS DURATION MAX_RPS STEPS STEP_DURATION WRITE_RPS SKEW LINKS
K6_ENV := $(foreach v,$(K6_VARS),$(if $($(v)),-e $(v)=$($(v))))

.PHONY: lab1 lab2 lab3 lab4 lab5 lab6 apply load ps logs down reset urls painel

# Troca de laboratório: mantém os dados e o histórico de métricas.
lab1 lab2 lab3 lab4 lab5 lab6:
	cp labs/$@.env .env
	$(DC) $(ALL) down --remove-orphans
	$(DC) build app1
	$(DC) up -d --wait
	@$(MAKE) --no-print-directory urls

# Reaplica o .env depois de editar uma variável.
apply:
	$(DC) up -d --wait

# make load SCRIPT=read|ramp|write|mixed RPS=500 DURATION=2m
load:
	$(DC) run --rm $(K6_ENV) k6 run /scripts/$(SCRIPT).js

ps:
	$(DC) ps

logs:
	$(DC) logs -f --tail 50 app1

# Para tudo, mantém os volumes.
down:
	$(DC) $(ALL) down --remove-orphans

# Apaga tudo, inclusive o banco e o histórico do Prometheus.
reset:
	$(DC) $(ALL) down -v --remove-orphans

# Página única para subir os labs, aplicar carga e ver endereços e senhas.
# Outra porta: PAINEL_PORT=7071 make painel
painel:
	@python3 painel/server.py

urls:
	@echo ""
	@echo "  Grafana     http://localhost:3000"
	@echo "  Prometheus  http://localhost:9090"
	@echo "  Encurtador  http://localhost:8080"
	@echo "  Painel      make painel  ->  http://localhost:7070"
	@echo ""
