# Atalhos do laboratório. Tudo aqui é só `docker compose` — veja o README
# se preferir digitar os comandos na mão (ou estiver no Windows sem make).
DC := $(shell docker compose version >/dev/null 2>&1 && echo docker compose || echo docker-compose)
ALL := --profile tools

SCRIPT ?= playback
K6_VARS := VIEWERS DURATION LESSON VARIANT PACE INTERVAL JITTER
K6_ENV := $(foreach v,$(K6_VARS),$(if $($(v)),-e $(v)=$($(v))))
N ?= 8
PAUSE ?= 35
BUSY := $(DC) exec -T pg psql -U vod -At -c "SELECT worker FROM jobs WHERE state = 'PROCESSING' ORDER BY started_at LIMIT 1"

.PHONY: lab1 lab2 lab3 lab4 lab5 lab6 caso-a caso-b apply load burst kill-worker pause-worker fila ps logs down reset urls

# Troca de laboratório: mantém os dados, os vídeos e o histórico de métricas.
lab1 lab2 lab3 lab4 lab5 lab6 caso-a caso-b:
	cp labs/$@.env .env
	$(DC) $(ALL) down --remove-orphans
	$(DC) build
	$(DC) up -d --wait
	@$(MAKE) --no-print-directory urls

# Reaplica o .env depois de editar uma variável.
apply:
	$(DC) up -d --wait

# make load SCRIPT=playback VIEWERS=50 DURATION=1m
# make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=false
load:
	$(DC) run --rm $(K6_ENV) k6 run /scripts/$(SCRIPT).js

# Rajada de publicações: make burst N=18
burst:
	@$(DC) exec -T api /vod burst $(N)

# Mata, sem aviso, o worker que está processando um job agora.
# A lease vence e outro worker (ou o mesmo, depois de `make apply`) assume.
kill-worker:
	@W=$$($(BUSY)); \
	if [ -z "$$W" ]; then echo "Nenhum job em processamento. Envie um vídeo ou rode 'make burst' antes."; \
	else echo "kill -9 no worker $$W"; docker kill $$W >/dev/null; fi

# Congela o worker ocupado por mais tempo que a lease e o solta de novo:
# é o "worker antigo" que acorda achando que ainda é dono do job.
pause-worker:
	@W=$$($(BUSY)); \
	if [ -z "$$W" ]; then echo "Nenhum job em processamento. Envie um vídeo ou rode 'make burst' antes."; \
	else echo "pausando $$W por $(PAUSE)s (a lease vence nesse meio-tempo)"; docker pause $$W >/dev/null; \
	sleep $(PAUSE); docker unpause $$W >/dev/null; echo "worker $$W acordou — veja 'make logs'"; fi

# A fila vista pelo banco.
fila:
	@$(DC) exec -T pg psql -U vod -c "SELECT id, asset_id, state, attempt, worker, GREATEST(extract(epoch FROM lease_until - now()), 0)::int AS lease_s, left(last_error, 70) AS erro FROM jobs ORDER BY id DESC LIMIT 15"

ps:
	$(DC) ps

logs:
	$(DC) logs -f --tail 50 api worker edge

# Para tudo, mantém os volumes.
down:
	$(DC) $(ALL) down --remove-orphans

# Apaga tudo: banco, vídeos e histórico do Prometheus.
reset:
	$(DC) $(ALL) down -v --remove-orphans

urls:
	@echo ""
	@echo "  Laboratório  http://localhost:8090   (catálogo, upload, fila, player)"
	@echo "  Grafana      http://localhost:3000"
	@echo "  Prometheus   http://localhost:9090"
	@echo "  API          http://localhost:8080"
	@echo ""
	@echo "  Na primeira subida, a aula 1 leva cerca de um minuto para ficar READY."
	@echo ""
