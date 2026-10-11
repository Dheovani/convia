#!/bin/sh
# Puts a Convia under load the way docs/scaling.md sizes it, and reports.
#
#   scripts/load.sh [smoke|provider] [duration]
#
# Everything it starts is disposable and removed at the end: a PostgreSQL, a
# Redis and one Convia, on a network of their own. Convia runs in a container
# limited to 2 processors and 4 GB, because that is the instance the targets
# in docs/scaling.md are stated for -- a run on an unlimited machine would
# measure the machine.
#
# The generator runs here, outside the container, and is trusted as a proxy so
# that the people it simulates can arrive from many addresses. That setting is
# for this disposable Convia only; see cmd/convia-load.
set -eu

# Git Bash on Windows rewrites arguments that look like paths, including the
# ones meant for inside the container. Nothing else reads this.
export MSYS_NO_PATHCONV=1

scale=${1:-smoke}
duration=${2:-2m}

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
# Docker on Windows wants a Windows path for a bind mount; elsewhere pwd is one.
mount=$(pwd -W 2>/dev/null || pwd)/.load

network=convia-load
port=18100
mkdir -p .load
rm -f .load/operator.txt

cleanup() {
	[ -n "${sampler:-}" ] && kill "$sampler" 2>/dev/null || true
	# What Convia logged is kept: a run that saw a 500 is a run worth reading.
	docker logs convia-load-app > .load/convia.log 2>&1 || true
	docker rm -f convia-load-app convia-load-pg convia-load-redis >/dev/null 2>&1 || true
	docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
cleanup

echo "building Convia for Linux"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o .load/convia ./cmd/convia

docker network create "$network" >/dev/null
docker run -d --name convia-load-pg --network "$network" \
	-e POSTGRES_USER=convia -e POSTGRES_PASSWORD=convia -e POSTGRES_DB=convia \
	postgres:17-alpine -c max_connections=200 	-c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all >/dev/null
# Reached as `redis`, the throwaway address the secret scanner knows for what it
# is (.gitleaks.toml), rather than by a container name it would have to be told.
docker run -d --name convia-load-redis --network "$network" --network-alias redis redis:8.2-alpine >/dev/null

until docker exec convia-load-pg pg_isready -U convia >/dev/null 2>&1; do sleep 1; done
# What every query cost, read back after the run. See docs/load.md.
until docker exec convia-load-pg psql -U convia -d convia -qc "CREATE EXTENSION IF NOT EXISTS pg_stat_statements" >/dev/null 2>&1; do sleep 1; done

echo "starting Convia: 2 processors, 4 GB"
docker run -d --name convia-load-app --network "$network" --cpus 2 --memory 4g \
	--add-host host.docker.internal:host-gateway \
	-p "$port:8080" -v "$mount:/load" \
	-e CONVIA_ENVIRONMENT=development \
	-e CONVIA_HTTP_HOST=0.0.0.0 -e CONVIA_HTTP_PORT=8080 \
	-e CONVIA_LOG_LEVEL=warn \
	-e CONVIA_DATABASE_URL="postgres://convia:convia@convia-load-pg:5432/convia?sslmode=disable" \
	-e CONVIA_DATABASE_MAX_CONNECTIONS=40 \
	-e CONVIA_REDIS_URL="redis://redis:6379/0" \
	-e CONVIA_TRUSTED_PROXIES="172.16.0.0/12,192.168.0.0/16" \
	-e CONVIA_PUBLIC_ADDRESS="http://127.0.0.1:$port" \
	-e CONVIA_TENANT_REQUESTS_PER_MINUTE=1000000 \
	-e CONVIA_PERSON_REQUESTS_PER_MINUTE=100000 \
	-e CONVIA_METRICS_ENDPOINT="http://host.docker.internal:4318" \
	-e OTEL_METRIC_EXPORT_INTERVAL=5000 \
	-e CONVIA_SERVICE_INSTANCE=load-1 \
	golang:1.26.9 sh -c '/load/convia migrate up >/dev/null &&
		/load/convia operator issue load > /load/operator.txt &&
		exec /load/convia' >/dev/null

until [ -s .load/operator.txt ] && curl -sf "http://127.0.0.1:$port/health" >/dev/null; do
	if [ "$(docker inspect -f '{{.State.Running}}' convia-load-app 2>/dev/null)" != "true" ]; then
		docker logs convia-load-app
		exit 1
	fi
	sleep 1
done
key=$(grep -o 'cvo_[A-Za-z0-9_]*' .load/operator.txt | head -1)

# What the container used, sampled every few seconds for the report.
( while true; do
	echo "$(date +%s) $(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' convia-load-app 2>/dev/null)" >> .load/stats.txt
	sleep 5
done ) &
sampler=$!
: > .load/stats.txt

rm -f .load/loading .load/window.txt
# Setting up is not what is profiled: when the load begins, PostgreSQL's
# counters start again, so the report below is about the load alone.
( until [ -e .load/loading ]; do sleep 1; done
	docker exec convia-load-pg psql -U convia -d convia -qc "SELECT pg_stat_statements_reset(); SELECT pg_stat_reset();" >/dev/null ) &
resetter=$!

status=0
go run ./cmd/convia-load -target "http://127.0.0.1:$port" -operator-key "$key" \
	-scale "$scale" -duration "$duration" -metrics 0.0.0.0:4318 -window .load/window.txt \
	-loading .load/loading || status=$?
kill "$resetter" 2>/dev/null || true

# What the load asked of PostgreSQL, and whether it found an index to do it with.
docker exec convia-load-pg psql -U convia -d convia -P pager=off -c "
	SELECT calls, round(mean_exec_time::numeric, 2) AS mean_ms, round(total_exec_time::numeric) AS total_ms,
	       rows, left(regexp_replace(query, '\s+', ' ', 'g'), 110) AS query
	FROM pg_stat_statements WHERE query NOT ILIKE '%pg_stat%'
	ORDER BY total_exec_time DESC LIMIT 12;" > .load/database.txt 2>&1
docker exec convia-load-pg psql -U convia -d convia -P pager=off -c "
	SELECT s.relname AS table_name, c.reltuples::bigint AS rows, s.seq_scan, s.seq_tup_read, s.idx_scan
	FROM pg_stat_user_tables s JOIN pg_class c ON c.oid = s.relid
	WHERE s.seq_scan + coalesce(s.idx_scan, 0) > 0
	ORDER BY s.seq_tup_read DESC, c.reltuples DESC LIMIT 12;" >> .load/database.txt 2>&1
echo
echo "what the load asked of PostgreSQL (.load/database.txt)"
cat .load/database.txt

# Docker's view over the load's window alone, which is what the processor
# figure has to come from: the runtime's own estimate only moves at a GC.
if [ -s .load/window.txt ]; then
	read -r from to < .load/window.txt
	echo
	echo "the container during the load, as Docker saw it"
	awk -v from="$from" -v to="$to" '$1 >= from && $1 <= to {
		cpu = $2; sub("%", "", cpu); cpu = cpu + 0; total += cpu; n++; if (cpu > peak) peak = cpu; memory = $3
	} END {
		if (n) printf "  processor  average %.0f%%, peak %.0f%% of 200%%  (%d samples)\n  memory     %s at the end\n", total / n, peak, n, memory
	}' .load/stats.txt
fi
docker logs convia-load-app > .load/convia.log 2>&1 || true
echo "  Convia logged $(grep -c 'level=ERROR\|"level":"ERROR"' .load/convia.log || true) errors; see .load/convia.log"

exit "$status"
