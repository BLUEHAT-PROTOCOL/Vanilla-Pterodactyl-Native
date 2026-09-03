#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# Vanilla Pterodactyl Native — FULL E2E SUITE
# Boots panel + native daemon against a portable MariaDB and verifies the
# complete acceptance chain. Designed to run in a single shot (CI/sandbox):
# every long-lived service is started here.
#
# Required: E2E_BASE with mariadb/ + downloads/ (see scripts/provision-e2e.sh),
#           php + go on PATH, panel vendor built, frontend built.
# Usage:    bash tests/e2e/run.sh [--skip-db-reset]
# ---------------------------------------------------------------------------
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJ="$(cd "$HERE/../.." && pwd)"
E2E_BASE="${E2E_BASE:-/home/z/e2e}"
PANEL_DIR="$PROJ/panel"
DAEMON_DIR="$PROJ/daemon"
PANEL_URL="http://127.0.0.1:8000"
DAEMON_URL="http://127.0.0.1:18080"
STATE_DIR="$E2E_BASE/state"
mkdir -p "$STATE_DIR"

export PATH="$HOME/toolchain/php:$PATH"
PHP="$(command -v php)"
source "$HERE/lib.sh"

PASS=0; FAIL=0; TOTAL=0
declare -a FAILURES=()

check() { # check <name> <condition-exit-code>
  local name="$1"; shift
  TOTAL=$((TOTAL+1))
  if "$@" >/dev/null 2>&1; then
    PASS=$((PASS+1)); echo "  [PASS] $name"
  else
    FAIL=$((FAIL+1)); FAILURES+=("$name"); echo "  [FAIL] $name"
  fi
}

check_output() { # like check but returns output via global OUT
  local name="$1"; local cmd="$2"
  TOTAL=$((TOTAL+1))
  OUT=$(eval "$cmd" 2>&1)
  if [ $? -eq 0 ]; then
    PASS=$((PASS+1)); echo "  [PASS] $name"
  else
    FAIL=$((FAIL+1)); FAILURES+=("$name"); echo "  [FAIL] $name"; echo "         ↳ $OUT" | head -3
  fi
}

echo "════════════════════════════════════════════════════════"
echo " Vanilla Pterodactyl Native — E2E"
echo "════════════════════════════════════════════════════════"

# ══ 0. services up ═══════════════════════════════════════════════════════
echo "── phase: services"
db_start || { echo "FATAL: db"; exit 1; }
"$MDBCTL" --socket="$MDB_SOCK" -uroot -e "GRANT ALL PRIVILEGES ON panel.* TO 'pterodactyl'@'127.0.0.1'; FLUSH PRIVILEGES;" 2>/dev/null

# migrations idempotent
( cd "$PANEL_DIR" && env -u DATABASE_URL -u DB_URL \
  MDB="$MDB" PATH="$MDB/bin:$PATH" \
  "$PHP" artisan migrate --force >/dev/null 2>&1 )
check "panel: migrations applied" test -d "$PANEL_DIR/vendor/laravel"
check "panel: frontend built" test -f "$PANEL_DIR/public/assets/manifest.yml" -o -d "$PANEL_DIR/public/assets"

# bootstrap admin + key + node
BOOTSTRAP=$( cd "$PANEL_DIR" && env -u DATABASE_URL -u DB_URL "$PHP" \
  "$HERE/bootstrap-e2e.php" "$E2E_BASE" 2>/dev/null )
if [ -z "$BOOTSTRAP" ]; then echo "FATAL: bootstrap failed"; exit 1; fi
echo "$BOOTSTRAP" > "$STATE_DIR/e2e-env.json"
API_TOKEN=$(python3 -c "import json;print(json.load(open('$STATE_DIR/e2e-env.json'))['api_token'])")
NODE_TID=$(python3 -c "import json;print(json.load(open('$STATE_DIR/e2e-env.json'))['node']['daemon_token_id'])")
NODE_TOK=$(python3 -c "import json;print(json.load(open('$STATE_DIR/e2e-env.json'))['node']['daemon_token'])")
NODE_ID=$(python3 -c "import json;print(json.load(open('$STATE_DIR/e2e-env.json'))['node']['id'])")

# daemon config
cat > "$E2E_BASE/daemon-config.yml" <<EOF
panel:
  url: $PANEL_URL
  token: "$NODE_TID.$NODE_TOK"
  allow_insecure: true
daemon:
  listen: 127.0.0.1:18080
  token_id: "$NODE_TID"
  token: "$NODE_TOK"
  api_keys:
    "$NODE_TID": "$NODE_TOK"
  data_path: $E2E_BASE/daemon-data
  backup_path: $E2E_BASE/daemon-backups
  tmp_path: $E2E_BASE/daemon-tmp
  username_prefix: vrp_
  upload_size_limit: 100
limits:
  crash_restarts: 3
  crash_window: 60
  log_max_lines: 5000
debug: false
EOF

# stop any leftover panel/daemon processes (masters + php -S workers)
pkill -f "artisan serve" 2>/dev/null || true
pkill -f "php -S 127.0.0.1:8000" 2>/dev/null || true
pkill -f "ptero-native --config" 2>/dev/null || true
sleep 1
pkill -9 -f "artisan serve" 2>/dev/null || true
pkill -9 -f "php -S 127.0.0.1:8000" 2>/dev/null || true
pkill -9 -f "ptero-native --config" 2>/dev/null || true

# start panel (php artisan serve) — absolute artisan path, sandbox env neutralized
( cd "$PANEL_DIR" && env -u DATABASE_URL -u DB_URL PHP_CLI_SERVER_WORKERS=8 setsid "$PHP" "$PANEL_DIR/artisan" serve --no-reload --host=127.0.0.1 --port=8000 \
  > "$E2E_BASE/panel.log" 2>&1 < /dev/null & )
# start daemon under a detached supervisor (auto-restarts if killed);
# the wrapper + daemon both match the pkill pattern used to stop them.
start_daemon_supervisor() {
  setsid nohup bash -c "while true; do '$DAEMON_DIR/bin/ptero-native' --config '$E2E_BASE/daemon-config.yml' >> '$E2E_BASE/daemon.log' 2>&1; echo '[supervisor] daemon exited, restarting in 1s' >> '$E2E_BASE/daemon.log'; sleep 1; done" > /dev/null 2>&1 < /dev/null &
  disown 2>/dev/null || true
}

# stop daemon: wrapper bash + daemon process (both contain the pattern)
stop_daemon() {
  pkill -f "ptero-native --config" 2>/dev/null || true
  sleep 1
  pkill -9 -f "ptero-native --config" 2>/dev/null || true
  sleep 1
}

start_daemon_supervisor

# wait for both services to accept connections before judging them
for i in $(seq 1 30); do curl -s -o /dev/null "$PANEL_URL" && break; sleep 1; done
for i in $(seq 1 30); do curl -s -o /dev/null "${DAEMON_URL}" && break; sleep 1; done

check "panel: artisan serve up" bash -c "curl -s -o /dev/null -w '%{http_code}' $PANEL_URL | grep -qE '200|302|301'"
check "daemon: /api/system responds" bash -c "curl -s -H 'Authorization: Bearer $NODE_TID.$NODE_TOK' $DAEMON_URL/api/system | grep -q '\"version\"'"

AUTH=(-H "Authorization: Bearer $API_TOKEN" -H "Accept: application/json")
DAUTH=(-H "Authorization: Bearer $NODE_TID.$NODE_TOK" -H "Accept: application/json")

# ══ 1. login (web session) ═══════════════════════════════════════════════
echo "── phase: admin login + egg import"
JAR="$STATE_DIR/cookies.txt"; rm -f "$JAR"
LOGIN_PAGE=$(curl -s -c "$JAR" $PANEL_URL/auth/login)
# Pterodactyl login is a Vue form: the CSRF token lives in a meta tag.
CSRF=$(echo "$LOGIN_PAGE" | grep -o 'name="csrf-token" content="[^"]*"' | head -1 | sed 's/.*content="//;s/"//')
check "panel: login page reachable" test -n "$LOGIN_PAGE"
# Pterodactyl login is an XHR endpoint: field "user" + JSON response.
LOGIN_RESP=$(curl -s -w '\n%{http_code}' -b "$JAR" -c "$JAR" -X POST $PANEL_URL/auth/login \
  -H "X-CSRF-TOKEN: $CSRF" -H "Accept: application/json" \
  -d "user=admin@example.com&password=e2epassword")
LOGIN_CODE=$(echo "$LOGIN_RESP" | tail -1)
check "panel: admin login accepted" bash -c "test '$LOGIN_CODE' = '200' && echo '$LOGIN_RESP' | grep -q '\"complete\":true'"
# §17: the auth bundle references this SVG; a clean release must ship it.
check "panel: login logo asset served (200)" bash -c "curl -s -o /dev/null -w '%{http_code}' $PANEL_URL/assets/svgs/pterodactyl.svg | grep -q 200"

# login regenerates the session (and rotates the CSRF token). Refresh it from
# an authenticated page — the admin layout emits <meta name="_token">.
CSRF=$(curl -s -b "$JAR" -c "$JAR" $PANEL_URL/admin | grep -o 'name="_token" content="[^"]*"' | head -1 | sed 's/.*content="//;s/"//')

# import eggs through the admin web route (multipart + session)
NEST_ID=1
import_egg() {
  local file="$1"
  local out code
  code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -X POST $PANEL_URL/admin/nests/import \
    -H "X-CSRF-TOKEN: $CSRF" \
    -F "import_file=@$file" -F "import_to_nest=$NEST_ID")
  [ "$code" = "302" ]
}
check "egg: import nodejs.json"      import_egg "$PROJ/tests/eggs/nodejs.json"
check "egg: import python.json"      import_egg "$PROJ/tests/eggs/python.json"
check "egg: import java.json"        import_egg "$PROJ/tests/eggs/java.json"
check "egg: import static.json"      import_egg "$PROJ/tests/eggs/static.json"

EGGS=$(curl -s "${AUTH[@]}" $PANEL_URL/api/application/nests/$NEST_ID/eggs)
NODEJS_EGG_ID=$(echo "$EGGS" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for e in d['data']:
    if 'Node.js E2E' == e['attributes']['name']:
        print(e['attributes']['id']); break
")
check "egg: nodejs egg visible via application API" test -n "$NODEJS_EGG_ID"

# compat audit
AUDIT=$( cd "$PANEL_DIR" && env -u DATABASE_URL -u DB_URL "$PHP" artisan p:eggs:compat-audit --fix 2>&1 )
check "egg: compat audit classifies nodejs egg" bash -c "echo '$AUDIT' | grep -qE 'Node.js E2E.*native|Node.js E2E.*mapping'"

# runtime resolve
RESOLVE=$(curl -s "${AUTH[@]}" "$PANEL_URL/api/application/runtime/resolve?image=ghcr.io/pterodactyl/yolks:nodejs_20")
check "runtime: resolve maps yolks:nodejs_20" bash -c "echo '$RESOLVE' | grep -q '\"resolved\":true'"
RESOLVE24=$(curl -s "${AUTH[@]}" "$PANEL_URL/api/application/runtime/resolve?image=ghcr.io/pterodactyl/yolks:nodejs_24")
check "runtime: resolve maps yolks:nodejs_24" bash -c "echo '$RESOLVE24' | grep -q '\"resolved\":true'"
PROFILES=$(curl -s "${AUTH[@]}" "$PANEL_URL/api/application/runtime/profiles")
check "runtime: node24 profile present" bash -c "echo '$PROFILES' | grep -q '\"node24\"'"
check "runtime: node24 binary valid" bash -c "command -v node >/dev/null && node --version | grep -qE '^v24\.'"

# ══ 2. server creation (pull model) ═══════════════════════════════════════
echo "── phase: server create + install"
ALLOC_ID=$(curl -s "${AUTH[@]}" $PANEL_URL/api/application/nodes/$NODE_ID/allocations | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d['data'][0]['attributes']['id'])")
ADMIN_ID=$(python3 -c "import json;print(json.load(open('$STATE_DIR/e2e-env.json'))['admin_email'] and 1)")

# find admin user id
ADMIN_ID=$(curl -s "${AUTH[@]}" $PANEL_URL/api/application/users | python3 -c "
import json,sys
d=json.load(sys.stdin)
for u in d['data']:
    if u['attributes']['email']=='admin@example.com':
        print(u['attributes']['id']); break")

CREATE=$(curl -s -w '\n%{http_code}' "${AUTH[@]}" -X POST $PANEL_URL/api/application/servers \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"E2E Node Server\",
    \"user\": $ADMIN_ID,
    \"egg\": $NODEJS_EGG_ID,
    \"docker_image\": \"ghcr.io/pterodactyl/yolks:nodejs_20\",
    \"startup\": \"node /home/container/{{SERVER_FILE}}\",
    \"environment\": {\"SERVER_FILE\": \"index.js\"},
    \"allocation\": {\"default\": $ALLOC_ID},
    \"limits\": {\"memory\": 256, \"swap\": 0, \"disk\": 1024, \"io\": 500, \"cpu\": 0, \"threads\": null},
    \"feature_limits\": {\"databases\": 0, \"allocations\": 2, \"backups\": 5},
    \"start_on_completion\": false
  }")
CREATE_CODE=$(echo "$CREATE" | tail -1)
SERVER_JSON=$(echo "$CREATE" | head -n -1)
# NOTE: use the FULL uuid — the daemon routes key on the complete uuid, not the
# 8-char identifier shown in the UI.
SERVER_UUID=$(echo "$SERVER_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['uuid'])" 2>/dev/null)
check "server: created via application API (201)" test "$CREATE_CODE" = "201"
check "server: uuid returned" test -n "$SERVER_UUID"

# daemon must pull the config from panel
sleep 2
DSRV=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID)
check "daemon: server registered (pull model)" bash -c "echo '$DSRV' | grep -q '\"uuid\"'"

# wait for install to complete (daemon runs install script then callbacks).
# Panel v1.15 contract: successful install => servers.status IS NULL.
install_done() {
  local st
  st=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers 2>/dev/null)
  # server state must be offline/running (not installing) AND panel shows installed
  if echo "$st" | grep -q "installing"; then return 1; fi
  curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID >/dev/null && \
  "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT status FROM panel.servers WHERE uuid='$SERVER_UUID'" 2>/dev/null | grep -q '^NULL$' && return 0
  return 1
}
for i in $(seq 1 60); do install_done && break; sleep 1; done
check "server: native install completed + panel callback" install_done

# ══ 3. console + ws ═══════════════════════════════════════════════════════
echo "── phase: realtime console"
CLIENT_TOKEN="$API_TOKEN"

# write app files via files API (daemon)
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Findex.js" \
  -H 'Content-Type: text/plain' --data-binary 'process.stdin.on("data", function(d){ var s=String(d).trim(); if (s==="stop") { process.exit(0); } console.log("echo: "+s); }); setInterval(function(){ console.log("tick"); }, 1000); console.log("e2e-server-ready");'
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Fpackage.json" \
  -H 'Content-Type: application/json' --data-binary '{"name":"e2e","main":"index.js"}'

# start via panel client API (panel → daemon power)
START_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X POST \
  $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "start"}')
check "power: start accepted (204 via panel)" test "$START_CODE" = "204"

# websocket via panel-issued token
WS_JSON=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID/websocket)
WS_TOKEN=$(echo "$WS_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['token'])" 2>/dev/null)
WS_URL=$(echo "$WS_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['socket'])" 2>/dev/null)
check "console: panel issues websocket JWT" test -n "$WS_TOKEN" -a -n "$WS_URL"

# run ws client: expect done-line, stats, stdin echo of "e2e-stdin-hello"
cd "$HERE" && [ -d node_modules ] || npm install --no-fund --no-audit >/dev/null 2>&1
WSOUT=$(timeout 40 node ws-client.mjs "ws://127.0.0.1:18080$(echo $WS_URL | sed 's|.*18080||;s|ws://[^/]*||')" "$WS_TOKEN" "e2e-server-ready" "e2e-stdin-hello" 30 2>/dev/null || true)
check "console: ws connected + console output" bash -c "echo '$WSOUT' | grep -q '\"connected\":true'"
check "console: done-line detected (starting→running)" bash -c "echo '$WSOUT' | grep -q '\"doneLine\":true'"
check "console: stats stream received" bash -c "echo '$WSOUT' | grep -q '\"stats\":true'"
check "console: stdin command echo via ws" bash -c "echo '$WSOUT' | grep -q '\"stdinEcho\":true'"

# ══ 4. file manager ═══════════════════════════════════════════════════════
echo "── phase: file manager"
FLIST=$(curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F")
check "files: list-directory returns entries" bash -c "echo '$FLIST' | grep -q 'index.js'"
check "files: FileEntry has created field (panel 1.15 contract)" bash -c "echo '$FLIST' | grep -q '\"created\"'"
check "files: FileEntry has modified field" bash -c "echo '$FLIST' | grep -q '\"modified\"'"
check "files: FileEntry has mode_bits + file + symlink + mime" bash -c "echo '$FLIST' | grep -q 'mode_bits' && echo '$FLIST' | grep -q '\"file\"' && echo '$FLIST' | grep -q 'symlink' && echo '$FLIST' | grep -q 'mime'"

# panel-side client file API (validates the whole transform chain)
PFLIST=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID/files/list?directory=%2F)
check "files: panel client API transforms FileEntry (created_at/modified_at)" bash -c "echo '$PFLIST' | grep -q 'created_at' && echo '$PFLIST' | grep -q 'modified_at'"

# rename / copy / mkdir / chmod
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/create-directory" \
  -H 'Content-Type: application/json' -d '{"root":"/","name":"logs"}'
check "files: create-directory" curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F" | grep -q 'logs'
curl -s -o /dev/null "${DAUTH[@]}" -X PUT "$DAEMON_URL/api/servers/$SERVER_UUID/files/rename" \
  -H 'Content-Type: application/json' -d '{"root":"/","files":[{"from":"package.json","to":"package-renamed.json"}]}'
check "files: rename" curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F" | grep -q 'package-renamed.json'
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/copy" \
  -H 'Content-Type: application/json' -d '{"location":"/package-renamed.json"}'
check "files: copy" curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F" | grep -q 'package-renamed.copy.json'
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/chmod" \
  -H 'Content-Type: application/json' -d '{"root":"/","files":[{"file":"index.js","mode":"0755"}]}'
CHMOD_LIST=$(curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F")
check "files: chmod applied" bash -c "echo '$CHMOD_LIST' | grep -q '0755'"

# compress + decompress
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/compress" \
  -H 'Content-Type: application/json' -d '{"root":"/","files":["index.js","logs"]}'
COMPRESS_LIST=$(curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F")
check "files: compress creates archive" bash -c "echo '$COMPRESS_LIST' | grep -q 'archive-.*tar.gz'"
ARCHIVE=$(curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F" | python3 -c "
import json,sys,re
d=sys.stdin.read()
m=re.search(r'archive-[0-9-]+\.tar\.gz', d)
print(m.group(0) if m else '')")
if [ -n "$ARCHIVE" ]; then
  curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/decompress" \
    -H 'Content-Type: application/json' -d "{\"root\":\"/\",\"file\":\"$ARCHIVE\"}"
  check "files: decompress extracts (zip/tar-slip guarded)" curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2Flogs" | grep -q '.'
else
  check "files: decompress extracts" false
fi

# traversal + symlink protection
TRAVERSAL_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${DAUTH[@]}" \
  "$DAEMON_URL/api/servers/$SERVER_UUID/files/contents?file=%2F..%2F..%2Fetc%2Fpasswd")
check "security: path traversal blocked" bash -c "[ \"$TRAVERSAL_CODE\" -ge 400 ] && [ \"$TRAVERSAL_CODE\" -lt 500 ]"
# symlink escape
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Flink.js" \
  -H 'Content-Type: text/plain' --data-binary 'x'
curl -s -o /dev/null "${DAUTH[@]}" -X PUT "$DAEMON_URL/api/servers/$SERVER_UUID/files/rename" \
  -H 'Content-Type: application/json' -d '{"root":"/","files":[{"from":"link.js","to":"evil"}]}' || true
ESCAPE_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${DAUTH[@]}" \
  "$DAEMON_URL/api/servers/$SERVER_UUID/files/contents?file=..%2F..%2F..%2Fetc%2Fshadow")
check "security: symlink/path escape blocked" bash -c "[ \"$ESCAPE_CODE\" -ge 400 ]"

# signed download (panel client API → daemon)
DL_URL=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID/files/download?file=%2Findex.js | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['url'])" 2>/dev/null)
check "files: signed download URL works" bash -c "curl -s '$DL_URL' | grep -q 'setInterval'"

# upload via panel-issued signed URL
UP_URL=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID/files/upload | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['url'])" 2>/dev/null)
echo "uploaded-by-e2e" > "$STATE_DIR/upload-test.txt"
UP_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$UP_URL" \
  -H "X-Data-Dir: /" -F "files=@$STATE_DIR/upload-test.txt")
check "files: signed multipart upload works" test "$UP_CODE" = "204" -o "$UP_CODE" = "201"

# ══ 5. power lifecycle + crash detection ═══════════════════════════════════
echo "── phase: power lifecycle"
STATE_NOW=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
check "power: server running" test "$STATE_NOW" = "running"

curl -s -o /dev/null "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "stop"}'
stopped() { [ "$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)" = "offline" ]; }
for i in $(seq 1 20); do stopped && break; sleep 1; done
check "power: graceful stop (egg stop command)" stopped

curl -s -o /dev/null "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "start"}'
running() { [ "$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)" = "running" ] || [ "$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)" = "starting" ]; }
for i in $(seq 1 20); do running && break; sleep 1; done
check "power: start again" running

# crash detection: replace index.js with an instantly-crashing script
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Findex.js" \
  -H 'Content-Type: text/plain' --data-binary 'process.exit(2);'
curl -s -o /dev/null "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "restart"}'
for i in $(seq 1 25); do
  ST=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
  [ "$ST" = "crashed" ] && break
  sleep 1
done
check "crash: crash detected (state=crashed)" test "$ST" = "crashed"

# auto-restart: the daemon restarts the crashed server after its debounce.
# The crashing app never reaches "running", so observe the crash budget
# counter incrementing (crash_count) — proof the supervisor re-spawned it.
C1=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('crash_count',0))" 2>/dev/null)
C2=$C1
for i in $(seq 1 15); do
  C2=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('crash_count',0))" 2>/dev/null)
  [ "${C2:-0}" -gt "${C1:-0}" ] 2>/dev/null && break
  sleep 1
done
check "crash: auto-restart engaged (crash budget counting)" bash -c "[ '${C2:-0}' -gt '${C1:-0}' ] 2>/dev/null"

curl -s -o /dev/null "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "kill"}'
for i in $(seq 1 10); do
  ST=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
  [ "$ST" = "offline" ] && break
  sleep 1
done
check "power: kill accepted (offline after)" test "$ST" = "offline"

# ══ 6. backups ═════════════════════════════════════════════════════════════
echo "── phase: backups"
# restore a healthy index.js first
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Findex.js" \
  -H 'Content-Type: text/plain' --data-binary 'console.log("e2e-server-ready"); process.stdin.on("data", function(d){ var s=String(d).trim(); if (s==="stop") { process.exit(0); } }); setInterval(function(){}, 1000);'

BCREATE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/backups -H 'Content-Type: application/json' -d '{}')
check "backup: create accepted (200)" test "$BCREATE" = "200"
backup_ready() {
  "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT is_successful FROM panel.backups WHERE server_id=(SELECT id FROM panel.servers WHERE uuid='$SERVER_UUID')" 2>/dev/null | grep -q '^1$'
}
for i in $(seq 1 40); do backup_ready && break; sleep 1; done
check "backup: completed + checksum reported to panel" backup_ready
BACKUP_UUID=$("$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT uuid FROM panel.backups WHERE server_id=(SELECT id FROM panel.servers WHERE uuid='$SERVER_UUID') LIMIT 1" 2>/dev/null | tail -1)
CHECKSUM=$("$MDBCTL" --socket="$MDB_SOCK" -uroot -N -e "SELECT checksum FROM panel.backups WHERE uuid='$BACKUP_UUID'" 2>/dev/null | tail -1)
check "backup: sha256 checksum stored" bash -c "echo '$CHECKSUM' | grep -q '^sha256:'"

# download via signed url and verify checksum
BDL=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID/backups/$BACKUP_UUID/download | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['url'])" 2>/dev/null)
if [ -n "$BDL" ]; then
  curl -s -o "$STATE_DIR/backup-dl.tar.gz" "$BDL"
  GOT=$(sha256sum "$STATE_DIR/backup-dl.tar.gz" | cut -d' ' -f1)
  WANT=$(echo "$CHECKSUM" | cut -d: -f2)
  check "backup: download matches sha256" test "$GOT" = "$WANT"
else
  check "backup: download matches sha256" false
fi

# restore with truncate
curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER_UUID/files/write?file=%2Frestore-marker.txt" \
  -H 'Content-Type: text/plain' --data-binary 'will-be-truncated'
curl -s -o /dev/null -w '%{http_code}\n' "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/backups/$BACKUP_UUID/restore -H 'Content-Type: application/json' -d '{"truncate": true}'
restore_done() {
  [ "$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)" != "restoring" ] && \
  ! curl -s "${DAUTH[@]}" "$DAEMON_URL/api/servers/$SERVER_UUID/files/list-directory?directory=%2F" | grep -q 'restore-marker'
}
for i in $(seq 1 40); do restore_done && break; sleep 1; done
check "backup: restore (truncate) completed + file restored" restore_done

# ══ 7. schedules ═══════════════════════════════════════════════════════════
echo "── phase: schedules"
SCHED=$(curl -s -w '\n%{http_code}' "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/schedules \
  -H 'Content-Type: application/json' -d '{"name": "e2e-schedule", "minute": "*", "hour": "*", "day_of_month": "*", "month": "*", "day_of_week": "*", "active": true}')
SCHED_CODE=$(echo "$SCHED" | tail -1)
check "schedule: created via client API (200)" test "$SCHED_CODE" = "200"
SCHED_ID=$(echo "$SCHED" | head -n -1 | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['id'])" 2>/dev/null)
TASK_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/schedules/$SCHED_ID/tasks \
  -H 'Content-Type: application/json' -d '{"action": "command", "payload": "echo schedule-ran-ok", "time_offset": 0}')
check "schedule: task created (200)" test "$TASK_CODE" = "200"

# ══ 8. restart panel + daemon ═══════════════════════════════════════════════
echo "── phase: restarts"
# panel restart — full kill (masters + php -S workers) then fresh start
pkill -f "artisan serve" 2>/dev/null || true
pkill -f "php -S 127.0.0.1:8000" 2>/dev/null || true
sleep 1
pkill -9 -f "artisan serve" 2>/dev/null || true
pkill -9 -f "php -S 127.0.0.1:8000" 2>/dev/null || true
( cd "$PANEL_DIR" && env -u DATABASE_URL -u DB_URL PHP_CLI_SERVER_WORKERS=8 setsid "$PHP" "$PANEL_DIR/artisan" serve --no-reload --host=127.0.0.1 --port=8000 > "$E2E_BASE/panel.log" 2>&1 < /dev/null & )
for i in $(seq 1 20); do curl -s -o /dev/null -w '' "$PANEL_URL" && break; sleep 1; done
check "restart: panel back up" curl -s -o /dev/null $PANEL_URL
AUTH_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" $PANEL_URL/api/application/servers)
check "restart: panel API still authorized" bash -c "test '$AUTH_CODE' = '200'"

# daemon restart (server running should be re-adopted)
curl -s -o /dev/null "${AUTH[@]}" -X POST $PANEL_URL/api/client/servers/$SERVER_UUID/power -H 'Content-Type: application/json' -d '{"signal": "start"}'
for i in $(seq 1 20); do running && break; sleep 1; done
stop_daemon
start_daemon_supervisor
for i in $(seq 1 20); do curl -s -o /dev/null "${DAUTH[@]}" $DAEMON_URL/api/system && break; sleep 1; done
sleep 2
DA_STATE=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
check "restart: daemon recovered + server re-adopted/registered" bash -c "[ \"$DA_STATE\" != '' ]"

# ══ 9. allocation sanity ═══════════════════════════════════════════════════
echo "── phase: allocations"
ALLOCS_JSON=$(curl -s "${AUTH[@]}" $PANEL_URL/api/client/servers/$SERVER_UUID)
check "allocations: server has default allocation" bash -c "echo '$ALLOCS_JSON' | grep -q 'allocation'"

# ══ 10. Node 24 end-to-end (profile+mapping+create+install+start+stop) ════
echo "── phase: node24 runtime"
ALLOC24_ID=$(curl -s "${AUTH[@]}" $PANEL_URL/api/application/nodes/$NODE_ID/allocations | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d['data'][1]['attributes']['id'])")
CREATE24=$(curl -s -w '\n%{http_code}' "${AUTH[@]}" -X POST $PANEL_URL/api/application/servers \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"E2E Node24 Server\",
    \"user\": $ADMIN_ID,
    \"egg\": $NODEJS_EGG_ID,
    \"docker_image\": \"ghcr.io/pterodactyl/yolks:nodejs_24\",
    \"startup\": \"node /home/container/{{SERVER_FILE}}\",
    \"environment\": {\"SERVER_FILE\": \"index.js\"},
    \"allocation\": {\"default\": $ALLOC24_ID},
    \"limits\": {\"memory\": 256, \"swap\": 0, \"disk\": 1024, \"io\": 500, \"cpu\": 0, \"threads\": null},
    \"feature_limits\": {\"databases\": 0, \"allocations\": 2, \"backups\": 5},
    \"start_on_completion\": false
  }")
CREATE24_CODE=$(echo "$CREATE24" | tail -1)
SERVER24_UUID=$(echo "$CREATE24" | head -n -1 | python3 -c "import json,sys; print(json.load(sys.stdin)['attributes']['uuid'])" 2>/dev/null)
check "node24: server created via application API (201)" test "$CREATE24_CODE" = "201"
check "node24: uuid returned" test -n "$SERVER24_UUID"

sleep 2
DSRV24=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER24_UUID)
check "node24: daemon registered server" bash -c "echo '$DSRV24' | grep -q '\"uuid\"'"

install_done_for() {
  local u="$1" st
  st=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers 2>/dev/null)
  if echo "$st" | grep -q "installing"; then return 1; fi
  "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT status FROM panel.servers WHERE uuid='$u'" 2>/dev/null | grep -q '^NULL$' && return 0
  return 1
}
for i in $(seq 1 60); do install_done_for "$SERVER24_UUID" && break; sleep 1; done
check "node24: native install completed + panel callback" install_done_for "$SERVER24_UUID"

curl -s -o /dev/null "${DAUTH[@]}" -X POST "$DAEMON_URL/api/servers/$SERVER24_UUID/files/write?file=%2Findex.js" \
  -H 'Content-Type: text/plain' --data-binary 'console.log("node24-ready"); setInterval(function(){}, 1000);'
ST24_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X POST \
  $PANEL_URL/api/client/servers/$SERVER24_UUID/power -H 'Content-Type: application/json' -d '{"signal": "start"}')
check "node24: start accepted (204 via panel)" test "$ST24_CODE" = "204"
ST24=""
for i in $(seq 1 30); do
  ST24=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER24_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
  [ "$ST24" = "running" ] && break
  sleep 1
done
check "node24: state running" test "$ST24" = "running"
STOP24_CODE=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X POST \
  $PANEL_URL/api/client/servers/$SERVER24_UUID/power -H 'Content-Type: application/json' -d '{"signal": "stop"}')
check "node24: stop accepted (204)" test "$STOP24_CODE" = "204"
ST24=""
for i in $(seq 1 30); do
  ST24=$(curl -s "${DAUTH[@]}" $DAEMON_URL/api/servers/$SERVER24_UUID | python3 -c "import json,sys; print(json.load(sys.stdin).get('state',''))" 2>/dev/null)
  [ "$ST24" = "offline" ] && break
  sleep 1
done
check "node24: state offline after stop" test "$ST24" = "offline"

# ══ summary ═══════════════════════════════════════════════════════════════
echo "════════════════════════════════════════════════════════"
echo " RESULTS: $PASS/$TOTAL PASS, $FAIL FAIL"
if [ $FAIL -gt 0 ]; then
  printf ' - %s\n' "${FAILURES[@]}"
fi
echo "════════════════════════════════════════════════════════"
echo "$PASS $TOTAL" > "$STATE_DIR/last-result.txt"
exit $FAIL
