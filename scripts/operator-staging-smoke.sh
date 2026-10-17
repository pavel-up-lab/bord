#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bord-operator-staging.XXXXXX")
SERVER_PID=
cleanup() { if [ -n "$SERVER_PID" ]; then kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; fi; rm -rf "$STAGE"; }
trap cleanup EXIT HUP INT TERM
mkdir -m 700 "$STAGE/blobs"

go build -o "$STAGE/bord-server" "$ROOT/cmd/bord-server"
dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64 >"$STAGE/old.key"
dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64 >"$STAGE/new.key"
chmod 600 "$STAGE/old.key" "$STAGE/new.key"

run() { printf '> %s\n' "$1"; shift; "$@"; }
run migrate "$STAGE/bord-server" migrate --database "$STAGE/bord.db"
run status "$STAGE/bord-server" status --database "$STAGE/bord.db"
run admin-create "$STAGE/bord-server" admin create --offline --database "$STAGE/bord.db" --id staging-admin --role administrator --ttl 1h --credential-output "$STAGE/admin.credential"
run admin-rotate "$STAGE/bord-server" admin rotate --offline --database "$STAGE/bord.db" --id staging-admin --role administrator --ttl 2h --credential-output "$STAGE/admin.next" --confirm ROTATE
run admin-revoke "$STAGE/bord-server" admin revoke --offline --database "$STAGE/bord.db" --id staging-admin --reason staging-finished --confirm REVOKE
run moderator-create "$STAGE/bord-server" admin create --offline --database "$STAGE/bord.db" --id staging-moderator --role moderator --ttl 1h --credential-output "$STAGE/moderator.credential"
PORTS=$(python3 - <<'PY'
import socket
socks=[]
for _ in range(2):
    s=socket.socket(); s.bind(('127.0.0.1',0)); socks.append(s)
print(*(s.getsockname()[1] for s in socks))
for s in socks: s.close()
PY
)
set -- $PORTS
PUBLIC_PORT=$1
MANAGEMENT_PORT=$2
BORD_HTTP_ADDRESS="127.0.0.1:$PUBLIC_PORT" BORD_MANAGEMENT_ADDRESS="127.0.0.1:$MANAGEMENT_PORT" BORD_DATABASE="$STAGE/bord.db" BORD_BLOBS="$STAGE/blobs" BORD_IP_LOOKUP_KEY_FILE="$STAGE/old.key" "$STAGE/bord-server" serve >"$STAGE/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$PUBLIC_PORT/readyz" >/dev/null 2>&1 && break; sleep .1; done
curl -fsS "http://127.0.0.1:$PUBLIC_PORT/readyz" >/dev/null
printf '> admin-ban\n'
BAN_OUTPUT=$("$STAGE/bord-server" admin ban --management-url "http://127.0.0.1:$MANAGEMENT_PORT" --credential-file "$STAGE/moderator.credential" --subject-type lookup --subject 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef --operations create_post --ttl 2h --reason staging-flood)
printf '%s\n' "$BAN_OUTPUT"
BAN_ID=${BAN_OUTPUT##*=}
run admin-unban "$STAGE/bord-server" admin unban --management-url "http://127.0.0.1:$MANAGEMENT_PORT" --credential-file "$STAGE/moderator.credential" --ban-id "$BAN_ID" --reason staging-appeal --confirm REVOKE-BAN
kill "$SERVER_PID"
wait "$SERVER_PID"
SERVER_PID=
python3 - "$STAGE/bord.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.execute("insert into request_subjects(ip_lookup,created_at,expires_at) values(?,datetime('now'),datetime('now','+1 day'))", (bytes(32),))
db.commit()
PY
run retention-hold "$STAGE/bord-server" retention hold --offline --database "$STAGE/bord.db" --subject-id 1 --confirm HOLD
run retention-release "$STAGE/bord-server" retention release --offline --database "$STAGE/bord.db" --subject-id 1 --confirm RELEASE
run retention-report "$STAGE/bord-server" retention report --database "$STAGE/bord.db"
run retention-run "$STAGE/bord-server" retention run --offline --database "$STAGE/bord.db" --blobs "$STAGE/blobs" --backup "$STAGE/pre-retention" --confirm DELETE
run reencrypt "$STAGE/bord-server" ip-reencrypt --offline --database "$STAGE/bord.db" --blobs "$STAGE/blobs" --backup "$STAGE/pre-reencrypt" --from-key-id primary --from-key-file "$STAGE/old.key" --to-key-id next --to-key-file "$STAGE/new.key" --batch-size 1 --confirm REENCRYPT
run verify "$STAGE/bord-server" restore-verify --backup "$STAGE/pre-reencrypt"
run restore "$STAGE/bord-server" restore --offline --backup "$STAGE/pre-reencrypt" --destination "$STAGE/restored"
run restored-status "$STAGE/bord-server" status --database "$STAGE/restored/database.sqlite3"

if "$STAGE/bord-server" admin revoke --offline --database "$STAGE/bord.db" --id staging-admin --reason unsafe 2>/dev/null; then
  echo "negative confirmation test unexpectedly succeeded" >&2
  exit 1
fi
test "$(stat -c %a "$STAGE/admin.credential")" = 600
echo "operator staging smoke passed (secrets omitted)"
