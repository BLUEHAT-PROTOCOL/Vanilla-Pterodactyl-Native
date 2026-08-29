#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# One-shot panel E2E environment setup (run AFTER scripts/provision-e2e.sh):
#   - writes the panel .env (pointing at the portable MariaDB)
#   - initializes + starts MariaDB (socket-based, no systemd)
#   - creates the panel database + user
#   - generates APP_KEY, runs migrations and seeders
# Then start the suite:  bash tests/e2e/run.sh
# ---------------------------------------------------------------------------
set -uo pipefail

TC="${TC:-$HOME/toolchain}"
E2E="${E2E_BASE:-/home/z/e2e}"
MDB="$E2E/mariadb/mariadb-11.4.5-linux-systemd-x86_64"
MDB_DATADIR="$E2E/mysql-data"
MDB_SOCK="$E2E/mysql.sock"
PROJ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PANEL="$PROJ/panel"

export PATH="$TC/php:$TC/bin:$MDB/bin:$PATH"
export LD_LIBRARY_PATH="$E2E/downloads/usr/lib/x86_64-linux-gnu:$MDB/lib"
unset DATABASE_URL DB_URL

mkdir -p "$E2E"

# --- .env ------------------------------------------------------------------
if [ ! -f "$PANEL/.env" ]; then
  SALT=$(openssl rand -hex 16)
  cat > "$PANEL/.env" <<EOF
APP_ENV=production
APP_DEBUG=true
APP_KEY=
APP_URL=http://127.0.0.1:8000
APP_TIMEZONE=UTC
APP_LOCALE=en
APP_ENVIRONMENT_ONLY=true

LOG_CHANNEL=single
LOG_LEVEL=debug

DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_PORT=3307
DB_DATABASE=panel
DB_USERNAME=pterodactyl
DB_PASSWORD=e2epassword

CACHE_DRIVER=file
SESSION_DRIVER=file
QUEUE_CONNECTION=sync
QUEUE_HIGH=high
QUEUE_STANDARD=standard
QUEUE_LOW=low

MAIL_MAILER=log
MAIL_HOST=127.0.0.1
MAIL_PORT=25
MAIL_ENCRYPTION=null
MAIL_FROM_ADDR=e2e@localhost
MAIL_FROM_NAME=Pterodactyl

HASHIDS_SALT=$SALT
HASHIDS_LENGTH=8
HASHIDS_ALPHABET=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890

TRUSTED_PROXIES=*
RECAPTCHA_ENABLED=false
EOF
  echo ".env created"
fi

# --- laravel runtime dirs (not tracked by git) ------------------------------
mkdir -p "$PANEL/storage/framework/"{cache/data,sessions,testing,views} \
         "$PANEL/storage/logs" "$PANEL/storage/app" "$PANEL/bootstrap/cache" \
         "$PANEL/public/assets"

# --- mariadb init + start ---------------------------------------------------
if [ ! -d "$MDB_DATADIR/mysql" ]; then
  mkdir -p "$MDB_DATADIR"
  "$MDB/scripts/mariadb-install-db" \
    --basedir="$MDB" --datadir="$MDB_DATADIR" --auth-root-authentication-method=normal \
    --skip-test-db > "$E2E/install-db.log" 2>&1
  echo "datadir initialized"
fi

if ! "$MDB/bin/mariadb" --socket="$MDB_SOCK" -uroot -e "SELECT 1" >/dev/null 2>&1; then
  setsid "$MDB/bin/mariadbd" \
    --basedir="$MDB" --datadir="$MDB_DATADIR" \
    --socket="$MDB_SOCK" --port=3307 --bind-address=127.0.0.1 \
    --pid-file="$E2E/mysqld.pid" --innodb-buffer-pool-size=128M \
    > "$E2E/mysqld.log" 2>&1 < /dev/null &
  for i in $(seq 1 60); do
    "$MDB/bin/mariadb" --socket="$MDB_SOCK" -uroot -e "SELECT 1" >/dev/null 2>&1 && break
    sleep 0.5
  done
fi
"$MDB/bin/mariadb" --socket="$MDB_SOCK" -uroot -e "SELECT 1" >/dev/null 2>&1 || {
  echo "DB FAIL"; tail -30 "$E2E/mysqld.log"; exit 1; }
echo "mariadb up"

"$MDB/bin/mariadb" --socket="$MDB_SOCK" -uroot <<'SQL'
CREATE DATABASE IF NOT EXISTS panel CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'pterodactyl'@'127.0.0.1' IDENTIFIED BY 'e2epassword';
CREATE USER IF NOT EXISTS 'pterodactyl'@'localhost' IDENTIFIED BY 'e2epassword';
GRANT ALL PRIVILEGES ON panel.* TO 'pterodactyl'@'127.0.0.1';
GRANT ALL PRIVILEGES ON panel.* TO 'pterodactyl'@'localhost';
FLUSH PRIVILEGES;
SQL
echo "db+user ready"

# --- key / migrate / seed ---------------------------------------------------
cd "$PANEL"
[ -n "$(grep '^APP_KEY=base64:' .env)" ] || php artisan key:generate --force || exit 1
php artisan migrate --force 2>&1 | tail -3 || exit 1
php artisan db:seed --force 2>&1 | tail -3 || exit 1
echo "PANEL SETUP DONE — run: bash tests/e2e/run.sh"
