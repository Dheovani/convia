#!/bin/sh
# Puts a Convia under load the way docs/scaling.md sizes it, and reports.
#
#   scripts/load.sh [smoke|quiet|provider] [duration] [instances] [outage]
#
# Everything it starts is disposable and removed at the end: a PostgreSQL, a
# Redis and one Convia or more, on a network of their own. Each Convia runs in
# a container limited to 2 processors and 4 GB, because that is the instance
# the targets in docs/scaling.md are stated for -- a run on an unlimited
# machine would measure the machine.
#
# With more than one instance, every stream is held open on one of them and
# every request goes to any of them, the way a load balancer spreads clients:
# most events are then produced on one instance and delivered by another, and
# the report gives that delay apart from the other.
#
# CONVIA_LOAD_CEILINGS names a ceilings file to fail a run that is slower than
# it allows, or that failed requests; the nightly workflow sets it.
#
# An outage takes Redis away a minute into the load, so the run needs to be
# longer than that, and the report shows, every ten seconds, what failed or
# was slow while it was gone and whether it came back:
#
#   pause    Redis stops answering for 30 seconds, connections held open --
#            what a failover or a network partition looks like from here
#   stop     Redis is gone for 30 seconds, and its name with it
#   restart  Redis is killed and started again at once, losing what it held
#
# or the media provider, which the generator stands in for, so that it is the
# control API that is measured and never media:
#
#   media-slow  every call to it answered after ten seconds, past the timeout
#   media-down  every call answered 503, as LiveKit does while it restarts
#   media-hang  no call answered at all
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
instances=${3:-1}
outage=${4:-none}
case "$outage" in
none | pause | stop | restart | media-slow | media-down | media-hang) ;;
*)
	echo "no outage $outage; there is none, pause, stop, restart, media-slow, media-down and media-hang" >&2
	exit 2
	;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
# Docker on Windows wants a Windows path for a bind mount; elsewhere pwd is one.
mount=$(pwd -W 2>/dev/null || pwd)/.load

network=convia-load
port=18100
mkdir -p .load
rm -f .load/operator.txt

apps() {
	k=1
	while [ "$k" -le "$instances" ]; do echo "convia-load-app-$k"; k=$((k + 1)); done
}

cleanup() {
	[ -n "${sampler:-}" ] && kill "$sampler" 2>/dev/null || true
	# What Convia logged is kept: a run that saw a 500 is a run worth reading.
	for app in $(apps); do docker logs "$app" > ".load/${app#convia-load-app-}.log" 2>&1 || true; done
	# Every instance a previous run may have left, whatever its count was.
	docker rm -f $(docker ps -aq --filter name=convia-load-) >/dev/null 2>&1 || true
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

# The first instance migrates and issues the operator key; the others start
# once it is serving, against the schema it made.
start() {
	n=$1
	command=$2
	docker run -d --name "convia-load-app-$n" --network "$network" --cpus 2 --memory 4g \
		--add-host host.docker.internal:host-gateway \
		-p "$((port + n - 1)):8080" -v "$mount:/load" \
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
		-e CONVIA_SERVICE_INSTANCE="load-$n" \
		-e CONVIA_LIVEKIT_URL="http://host.docker.internal:7880" \
		-e CONVIA_LIVEKIT_API_KEY=devkey \
		-e CONVIA_LIVEKIT_API_SECRET=a-development-secret-long-enough-to-be-accepted \
		golang:1.26.9 sh -c "$command" >/dev/null
}

serving() {
	until curl -sf "http://127.0.0.1:$((port + $1 - 1))/health" >/dev/null; do
		if [ "$(docker inspect -f '{{.State.Running}}' "convia-load-app-$1" 2>/dev/null)" != "true" ]; then
			docker logs "convia-load-app-$1"
			exit 1
		fi
		sleep 1
	done
}

echo "starting $instances Convia: 2 processors, 4 GB each"
start 1 '/load/convia migrate up >/dev/null &&
	/load/convia operator issue load > /load/operator.txt &&
	exec /load/convia'
until [ -s .load/operator.txt ]; do sleep 1; done
serving 1
targets="http://127.0.0.1:$port"
i=2
while [ "$i" -le "$instances" ]; do
	start "$i" 'exec /load/convia'
	serving "$i"
	targets="$targets,http://127.0.0.1:$((port + i - 1))"
	i=$((i + 1))
done
key=$(grep -o 'cvo_[A-Za-z0-9_]*' .load/operator.txt | head -1)

# What the containers used, sampled every few seconds for the report.
( while true; do
	now=$(date +%s)
	docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' $(apps) 2>/dev/null |
		sed "s/^/$now /" >> .load/stats.txt
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

media_fault=
case "$outage" in media-*) media_fault=${outage#media-} ;; esac

# Ceilings hold a run to being no worse than before (cmd/convia-load/ceilings.json).
# A run that breaks something on purpose is held to the targets alone: it is
# meant to fail requests, and the question it answers is which ones.
held=
if [ -n "${CONVIA_LOAD_CEILINGS:-}" ] && [ "$outage" = none ]; then
	held="-ceilings $CONVIA_LOAD_CEILINGS"
fi

rm -f .load/outage.txt
if [ "$outage" != none ] && [ -z "$media_fault" ]; then
	( until [ -e .load/loading ]; do sleep 1; done
		began=$(date +%s)
		sleep 60
		echo "Redis: $outage at +$(($(date +%s) - began))s" >> .load/outage.txt
		case "$outage" in
		pause) docker pause convia-load-redis >/dev/null; sleep 30; docker unpause convia-load-redis >/dev/null ;;
		stop) docker stop convia-load-redis >/dev/null; sleep 30; docker start convia-load-redis >/dev/null ;;
		restart) docker restart -t 0 convia-load-redis >/dev/null ;;
		esac
		echo "Redis: back at +$(($(date +%s) - began))s" >> .load/outage.txt ) &
	saboteur=$!
fi

status=0
go run ./cmd/convia-load -target "$targets" -operator-key "$key" \
	-scale "$scale" -duration "$duration" -metrics 0.0.0.0:4318 -window .load/window.txt \
	-loading .load/loading -timeline -media 0.0.0.0:7880 -media-fault "$media_fault" $held || status=$?
kill "$resetter" 2>/dev/null || true
[ -n "${saboteur:-}" ] && wait "$saboteur" 2>/dev/null || true
[ -s .load/outage.txt ] && { echo; cat .load/outage.txt; }

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
	echo "the containers during the load, as Docker saw them"
	for app in $(apps); do
		awk -v from="$from" -v to="$to" -v app="$app" '$1 >= from && $1 <= to && $2 == app {
			cpu = $3; sub("%", "", cpu); cpu = cpu + 0; total += cpu; n++; if (cpu > peak) peak = cpu; memory = $4
		} END {
			if (n) printf "  %s  processor average %.0f%%, peak %.0f%% of 200%% (%d samples); memory %s at the end\n", app, total / n, peak, n, memory
		}' .load/stats.txt
	done
fi
for app in $(apps); do
	log=".load/${app#convia-load-app-}.log"
	docker logs "$app" > "$log" 2>&1 || true
	echo "  $app logged $(grep -c 'level=ERROR\|"level":"ERROR"' "$log" || true) errors; see $log"
done

exit "$status"
