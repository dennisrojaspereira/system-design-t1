#!/bin/sh
# Réplica de leitura (Lab 5): copia o primário na primeira subida e passa a
# aplicar o WAL dele. REPLICA_APPLY_DELAY atrasa a aplicação de propósito,
# para o lag de replicação ficar visível no Grafana.
set -e
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  until pg_isready -h pg -U shortener -q; do sleep 1; done
  PGPASSWORD=replicator pg_basebackup -h pg -U replicator -D "$PGDATA" -R -X stream
  echo "recovery_min_apply_delay = '${REPLICA_APPLY_DELAY:-0}'" >> "$PGDATA/postgresql.auto.conf"
  chmod 700 "$PGDATA"
fi
# max_connections da réplica não pode ser menor que o do primário.
exec postgres -c max_connections=300
