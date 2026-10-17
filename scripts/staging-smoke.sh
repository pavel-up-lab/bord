#!/bin/sh
# Exercise a complete isolated deployment through a real Caddy process.
set -eu

: "${CADDY_BIN:=caddy}"
case "$CADDY_BIN" in
	*/*) [ -x "$CADDY_BIN" ] || { echo "CADDY_BIN is not executable" >&2; exit 2; } ;;
	*) command -v "$CADDY_BIN" >/dev/null || { echo "caddy is required (set CADDY_BIN)" >&2; exit 2; } ;;
esac

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
work=${STAGING_WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/bord-staging.XXXXXX")}
report=${STAGING_REPORT:-$work/staging-evidence.txt}
server_pid=
caddy_pid=

stop_server() {
	if [ -n "$server_pid" ]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; server_pid=; fi
}
cleanup() {
	stop_server
	if [ -n "$caddy_pid" ]; then kill -TERM "$caddy_pid" 2>/dev/null || true; wait "$caddy_pid" 2>/dev/null || true; fi
}
trap cleanup EXIT INT TERM

mkdir -p "$work/data/blobs" "$work/caddy/data" "$work/caddy/config" "$work/bin"
lookup=$(python3 - <<'PY'
import base64, secrets
print(base64.b64encode(secrets.token_bytes(32)).decode())
PY
)
cat >"$work/config.yaml" <<EOF
http:
  address: 127.0.0.1:18080
  max_body_bytes: 16777216
storage:
  database: $work/data/bord.db
  blobs: $work/data/blobs
privacy:
  store_raw_ip: false
EOF

build_server() {
	version=$1
	output=$2
	go build -trimpath -buildvcs=false -ldflags "-s -w -X bord/internal/operations/command.Version=$version" -o "$output" ./cmd/bord-server
}
build_server v0.0.0-staging-old "$work/bin/bord-server-old"
build_server v0.0.0-staging-new "$work/bin/bord-server-new"

start_server() {
	binary=$1
	database=$2
	blobs=$3
	BORD_IP_LOOKUP_KEY="$lookup" BORD_TRUSTED_PROXIES=127.0.0.1/32 \
	BORD_DATABASE="$database" BORD_BLOBS="$blobs" BORD_HTTP_ADDRESS=127.0.0.1:18080 \
		"$binary" serve --config "$work/config.yaml" >"$work/server.log" 2>&1 &
	server_pid=$!
	i=0
	while ! curl -fsS http://127.0.0.1:18080/readyz >/dev/null 2>&1; do
		i=$((i + 1)); [ "$i" -lt 100 ] || { cat "$work/server.log" >&2; return 1; }
		kill -0 "$server_pid" 2>/dev/null || { cat "$work/server.log" >&2; return 1; }
		sleep .1
	done
}

BORD_DOMAIN=https://localhost:18443 BORD_UPSTREAM=127.0.0.1:18080 BORD_HTTP_PORT=18081 \
XDG_DATA_HOME="$work/caddy/data" XDG_CONFIG_HOME="$work/caddy/config" \
	"$CADDY_BIN" run --config "$root/deploy/Caddyfile" --adapter caddyfile >"$work/caddy.log" 2>&1 &
caddy_pid=$!
start_server "$work/bin/bord-server-old" "$work/data/bord.db" "$work/data/blobs"
i=0
while ! curl -kfsS https://localhost:18443/readyz >/dev/null 2>&1; do
	i=$((i + 1)); [ "$i" -lt 100 ] || { cat "$work/caddy.log" >&2; exit 1; }
	kill -0 "$caddy_pid" 2>/dev/null || { cat "$work/caddy.log" >&2; exit 1; }
	sleep .1
done

curl -kfsSN --max-time 20 https://localhost:18443/api/v1/events?board=general >"$work/sse.txt" 2>"$work/sse.err" &
sse_pid=$!
sleep 1
verifier=$(python3 - <<'PY'
import base64
print(base64.urlsafe_b64encode(b'x' * 32).decode().rstrip('='))
PY
)
upload=$(curl -kfsS -H 'Content-Type: image/png' --data-binary @"$root/internal/media/testdata/valid.png" \
	https://localhost:18443/api/v1/attachments)
attachment=$(printf '%s' "$upload" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
response=$(curl -kfsS -H 'Content-Type: application/json' -H 'Idempotency-Key: staging-thread-0001' \
	--data "{\"body\":\"staging smoke\",\"attachment_id\":\"$attachment\",\"session_proof\":\"staging-session-proof-000000000001\",\"delete_verifier\":\"$verifier\"}" \
	https://localhost:18443/api/v1/boards/general/threads)
thread=$(printf '%s' "$response" | python3 -c 'import json,sys; print(json.load(sys.stdin)["thread_id"])')
wait "$sse_pid" 2>/dev/null || true
grep -q '^: heartbeat' "$work/sse.txt"
# Reconnect through Caddy with the resume header. A stream with no retained
# event still has to establish promptly and flush its heartbeat unbuffered.
curl -kfsSN --max-time 17 -H 'Last-Event-ID: 0' \
	https://localhost:18443/api/v1/events?board=general >"$work/sse-reconnect.txt" 2>"$work/sse-reconnect.err" || true
grep -q '^: heartbeat' "$work/sse-reconnect.txt"
curl -kfsS "https://localhost:18443/api/v1/threads/$thread" >"$work/thread-before.json"
curl -kfsS "https://localhost:18443/api/v1/attachments/$attachment" >"$work/blob-before"

# Upgrade with the same supported schema, then prove readiness through Caddy.
stop_server
start_server "$work/bin/bord-server-new" "$work/data/bord.db" "$work/data/blobs"
curl -kfsS https://localhost:18443/readyz >/dev/null
stop_server

"$work/bin/bord-server-new" backup --offline --database "$work/data/bord.db" --blobs "$work/data/blobs" --output "$work/backup" >"$work/backup.log"
"$work/bin/bord-server-new" restore-verify --backup "$work/backup" >>"$work/backup.log"
manifest_hash=$(sha256sum "$work/backup/manifest.json" | cut -d' ' -f1)

# Destructive incident is confined to the generated staging directory.
: >"$work/data/bord.db"
find "$work/data/blobs" -type f -delete
start_server "$work/bin/bord-server-new" "$work/data/bord.db" "$work/data/blobs"
if curl -kfsS "https://localhost:18443/api/v1/threads/$thread" >"$work/incident-read.json" 2>/dev/null; then
	echo "destructive incident did not remove the staging thread" >&2; exit 1
fi
stop_server
"$work/bin/bord-server-new" restore --offline --backup "$work/backup" --destination "$work/restored" >"$work/restore.log"
"$work/bin/bord-server-new" restore-verify --backup "$work/backup" >>"$work/restore.log"
start_server "$work/bin/bord-server-new" "$work/restored/database.sqlite3" "$work/restored/blobs"
curl -kfsS https://localhost:18443/readyz >/dev/null
curl -kfsS "https://localhost:18443/api/v1/threads/$thread" >"$work/thread-after.json"
curl -kfsS "https://localhost:18443/api/v1/attachments/$attachment" >"$work/blob-after"
cmp "$work/thread-before.json" "$work/thread-after.json"
cmp "$work/blob-before" "$work/blob-after"
stop_server

# Roll back to the prior compatible binary against the restored schema.
start_server "$work/bin/bord-server-old" "$work/restored/database.sqlite3" "$work/restored/blobs"
curl -kfsS https://localhost:18443/readyz >/dev/null
stop_server

mkdir -p "$(dirname "$report")"
cat >"$report" <<EOF
staging_smoke=passed
caddy_version=$($CADDY_BIN version | awk '{print $1}')
tls_endpoint=https://localhost:18443
sse=heartbeat-and-resume-reconnect-through-caddy
upgrade=v0.0.0-staging-old-to-v0.0.0-staging-new
backup_manifest_sha256=$manifest_hash
destructive_incident=corrupt-database-and-delete-blobs
restore=manifest-hash-logical-thread-and-blob-hash-match
readiness=passed-before-and-after-restore
compatible_rollback=v0.0.0-staging-old-passed
EOF
cat "$report"
