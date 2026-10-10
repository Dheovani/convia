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
	postgres:17-alpine -c max_connections=200 >/dev/null
docker run -d --name convia-load-redis --network "$network" redis:8.2-alpine >/dev/null

until docker exec convia-load-pg pg_isready -U convia >/dev/null 2>&1; do sleep 1; done

echo "starting Convia: 2 processors, 4 GB"
docker run -d --name convia-load-app --network "$network" --cpus 2 --memory 4g \
	--add-host host.docker.internal:host-gateway \
	-p "$port:8080" -v "$mount:/load" \
	-e CONVIA_ENVIRONMENT=development \
	-e CONVIA_HTTP_HOST=0.0.0.0 -e CONVIA_HTTP_PORT=8080 \
	-e CONVIA_LOG_LEVEL=warn \
	-e CONVIA_DATABASE_URL="postgres://convia:convia@convia-load-pg:5432/convia?sslmode=disable" \
	-e CONVIA_DATABASE_MAX_CONNECTIONS=40 \
	-e CONVIA_REDIS_URL="redis://convia-load-redis:6379/0" \
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

status=0
go run ./cmd/convia-load -target "http://127.0.0.1:$port" -operator-key "$key" \
	-scale "$scale" -duration "$duration" -metrics 0.0.0.0:4318 -window .load/window.txt || status=$?

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
