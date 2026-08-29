#!/usr/bin/env bash
# E2E helpers: portable MariaDB lifecycle + service management.
# Override via env: E2E_BASE=/home/z/e2e MARIADB_TARBALL_DIR=...
E2E_BASE="${E2E_BASE:-/home/z/e2e}"
MDB="$E2E_BASE/mariadb/mariadb-11.4.5-linux-systemd-x86_64"
MDB_DATADIR="$E2E_BASE/mysql-data"
MDB_SOCK="$E2E_BASE/mysql.sock"
MDB_PORT="${MDB_PORT:-3307}"
LIBAIO_DIR="$E2E_BASE/downloads/usr/lib/x86_64-linux-gnu"
PANEL_PORT="${PANEL_PORT:-8000}"
DAEMON_PORT="${DAEMON_PORT:-18080}"

export LD_LIBRARY_PATH="$LIBAIO_DIR:$MDB/lib:${LD_LIBRARY_PATH:-}"
MDBCTL="$MDB/bin/mariadb"
UNSET_ENV=(env -u DATABASE_URL -u DB_URL)

db_start() {
  if "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT 1" >/dev/null 2>&1; then
    return 0
  fi
  setsid "$MDB/bin/mariadbd" \
    --basedir="$MDB" --datadir="$MDB_DATADIR" \
    --socket="$MDB_SOCK" --port="$MDB_PORT" --bind-address=127.0.0.1 \
    --pid-file="$E2E_BASE/mysqld.pid" --innodb-buffer-pool-size=128M \
    > "$E2E_BASE/mysqld.log" 2>&1 < /dev/null &
  disown
  for i in $(seq 1 40); do
    "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SELECT 1" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "mariadb failed to start"; tail -20 "$E2E_BASE/mysqld.log"; return 1
}

db_stop() { "$MDBCTL" --socket="$MDB_SOCK" -uroot -e "SHUTDOWN" >/dev/null 2>&1 || true; }

wait_http() { # wait_http <url> <token-arg...> <timeout-sec>
  local url="$1"; shift
  local timeout="${@: -1}"
  local auth=("${@:1:$#-1}")
  for i in $(seq 1 "$timeout"); do
    if [ ${#auth[@]} -gt 0 ]; then
      curl -s -o /dev/null -H "Authorization: Bearer ${auth[0]}" "$url" && return 0
    else
      curl -s -o /dev/null "$url" && return 0
    fi
    sleep 1
  done
  return 1
}
