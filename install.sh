#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# Vanilla Pterodactyl Native — installer (Ubuntu 24.04 / Debian 12)
#
# Installs:
#   1. Panel dependencies (PHP 8.3, Composer, MariaDB, nginx)     [panel]
#   2. The Panel fork (web UI, unchanged UX + native runtime layer) [panel]
#   3. Native runtimes (node/python/java into /opt/runtimes)      [runtimes]
#   4. ptero-native daemon (Go build or release binary)           [daemon]
#
# systemd is OPTIONAL everywhere. Every service start/reload goes through
# helpers that detect a real systemd init and otherwise fall back to direct
# service invocation (NAT VPS / containers where apt blocks auto-start via
# policy-rc.d included).
#
# Usage:  sudo bash install.sh [--panel-only|--daemon-only|--runtimes-only]
# ---------------------------------------------------------------------------
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-all}"
PANEL_DIR="${PANEL_DIR:-/var/www/pterodactyl}"
DAEMON_DIR="${DAEMON_DIR:-/opt/ptero-native}"
RUNTIME_DIR="${RUNTIME_DIR:-/opt/runtimes}"

log() { printf '\033[1;36m[install]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Run as root (sudo)."

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "Missing dependency: $2"; }

# --- no-systemd-safe service helpers (§4.4/§4.5) ----------------------------
# Only take the systemctl path when systemd is genuinely PID 1; checking for
# the binary alone is not enough on containers.
have_systemd() {
  [ -d /run/systemd/system ] && [ "$(ps -p 1 -o comm= 2>/dev/null)" = "systemd" ]
}

svc_start() { # svc_start <unit-name> <direct-start-cmd...>
  local unit="$1"; shift
  if have_systemd; then
    systemctl start "$unit" 2>/dev/null && return 0
  fi
  if command -v service >/dev/null 2>&1; then
    service "$unit" start >/dev/null 2>&1 && return 0
  fi
  "$@" # direct fallback invocation
  return $?
}

svc_reload() { # svc_reload <unit-name> <direct-reload-cmd...>
  local unit="$1"; shift
  if have_systemd; then
    systemctl reload "$unit" 2>/dev/null && return 0
  fi
  "$@"
  return $?
}

wait_tcp() { # wait_tcp <host> <port> <tries>
  local host="$1" port="$2" tries="${3:-30}" i
  for ((i = 0; i < tries; i++)); do
    if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then
      exec 3>&- 3<&- 2>/dev/null || true
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_mysql() { # wait for the local mariadb socket/TCP readiness
  local tries="${1:-30}" i
  for ((i = 0; i < tries; i++)); do
    if mysqladmin ping --silent 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

start_mariadb() {
  if wait_mysql 5; then return 0; fi
  log "Starting MariaDB (no-systemd fallback path)…"
  svc_start mariadb \
    sh -c '(command -v mariadbd-safe >/dev/null && mariadbd-safe --skip-syslog || mysqld_safe --skip-syslog) >/dev/null 2>&1 &'
  wait_mysql 60 || die "MariaDB did not become ready — check /var/log/mysql error log."
  log "MariaDB is ready."
}

start_php_fpm() {
  local fpm
  fpm="$(ls /run/php/php*-fpm.sock 2>/dev/null | head -1 || true)"
  [ -S "$fpm" ] && return 0
  log "Starting PHP-FPM…"
  svc_start php8.3-fpm php-fpm8.3 || svc_start php7.4-fpm php-fpm7.4 || true
  for ((i = 0; i < 15; i++)); do
    fpm="$(ls /run/php/php*-fpm.sock 2>/dev/null | head -1 || true)"
    [ -S "$fpm" ] && { log "PHP-FPM is ready."; return 0; }
    sleep 1
  done
  die "PHP-FPM socket not found after start — check /var/log/php8.3-fpm.log."
}

start_nginx() {
  if pgrep -x nginx >/dev/null 2>&1; then return 0; fi
  log "Starting nginx…"
  svc_start nginx nginx
  for ((i = 0; i < 15; i++)); do
    pgrep -x nginx >/dev/null 2>&1 && { log "nginx is ready."; return 0; }
    sleep 1
  done
  die "nginx did not start — run 'nginx -t' to inspect the configuration."
}

go_min_version() { # go_min_version <minimum> — §4.6: existence check is not enough
  local min="$1" have
  command -v go >/dev/null 2>&1 || die "Go >= $min is required to build the daemon (https://go.dev/dl/)."
  have="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  [ -n "$have" ] || have="$(go version | sed 's/^go version go\([0-9.]*\).*/\1/')"
  [ -n "$have" ] || die "cannot determine Go version"
  # numeric compare over dotted versions
  local ok
  ok="$(python3 - "$have" "$min" <<'PY'
import sys
def t(v): return [int(x) for x in v.split(".")]
have, need = t(sys.argv[1]), t(sys.argv[2])
print("yes" if have >= need else "no")
PY
)"
  [ "$ok" = "yes" ] || die "Go $have found, but the daemon requires Go >= $min"
}

install_panel_deps() {
  log "Installing panel dependencies (php 8.3, mariadb, nginx, redis)…"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y ca-certificates curl gnupg software-properties-common
  add-apt-repository -y ppa:ondrej/php || true
  apt-get update -y
  apt-get install -y \
    php8.3-cli php8.3-fpm php8.3-mysql php8.3-gd php8.3-mbstring php8.3-bcmath \
    php8.3-xml php8.3-curl php8.3-zip php8.3-intl php8.3-sqlite3 \
    mariadb-server mariadb-client nginx redis-server tar unzip git
  curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer
}

install_panel() {
  install_panel_deps
  start_mariadb

  log "Installing Panel into $PANEL_DIR…"
  mkdir -p "$PANEL_DIR"
  cp -a "$HERE/panel/." "$PANEL_DIR/"
  cd "$PANEL_DIR"
  composer install --no-dev --optimize-autoloader --no-interaction

  log "Setting up database…"
  mysql -e "CREATE DATABASE IF NOT EXISTS panel CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
  DBPASS=$(openssl rand -hex 16)
  mysql -e "CREATE USER IF NOT EXISTS 'pterodactyl'@'127.0.0.1' IDENTIFIED BY '$DBPASS';"
  mysql -e "GRANT ALL PRIVILEGES ON panel.* TO 'pterodactyl'@'127.0.0.1'; FLUSH PRIVILEGES;"
  if [ ! -f .env ]; then
    [ -f .env.example ] || die "panel/.env.example missing from the release (clean-install blocker)"
    cp .env.example .env
  fi
  # write db settings (idempotent enough for a first install)
  sed -i "s|^DB_DATABASE=.*|DB_DATABASE=panel|;s|^DB_USERNAME=.*|DB_USERNAME=pterodactyl|;s|^DB_PASSWORD=.*|DB_PASSWORD=$DBPASS|;s|^DB_HOST=.*|DB_HOST=127.0.0.1|" .env
  # HASHIDS_SALT is required by config/hashids.php — generate it here
  if grep -qE '^HASHIDS_SALT=$' .env || ! grep -qE '^HASHIDS_SALT=.' .env; then
    sed -i "s|^HASHIDS_SALT=.*|HASHIDS_SALT=$(openssl rand -hex 32)|" .env
  fi
  PHP=$(command -v php8.3 || command -v php)
  env -u DATABASE_URL -u DB_URL "$PHP" artisan key:generate --force
  env -u DATABASE_URL -u DB_URL "$PHP" artisan migrate --seed --force

  # Laravel writable directories must exist BEFORE ownership/permission setup;
  # git checkouts ship without them (storage/framework is fully gitignored).
  mkdir -p storage/framework/{cache,sessions,views,testing} storage/logs bootstrap/cache public/assets
  chown -R www-data:www-data "$PANEL_DIR"
  chmod -R 755 "$PANEL_DIR/storage" "$PANEL_DIR/bootstrap/cache"

  log "Building frontend (this can take several minutes)…"
  (command -v yarn >/dev/null 2>&1 || npm install -g yarn)
  corepack enable 2>/dev/null || true
  (cd "$PANEL_DIR" && mkdir -p public/assets && yarn install --frozen-lockfile && yarn build:production)

  # nginx site config: always reference the source tree absolutely — the cwd
  # changed to $PANEL_DIR above, so a bare `scripts/nginx` would never match (§4.3).
  if [ -d "$HERE/scripts/nginx" ]; then
    cp "$HERE/scripts/nginx/ptero-native.conf" /etc/nginx/sites-available/pterodactyl.conf
    sed -i "s|__PANEL_DIR__|$PANEL_DIR|g" /etc/nginx/sites-available/pterodactyl.conf
    ln -sf /etc/nginx/sites-available/pterodactyl.conf /etc/nginx/sites-enabled/pterodactyl.conf
  fi
  nginx -t
  start_nginx
  # reload only when nginx was already running under systemd; direct starts
  # above are already serving fresh config.
  svc_reload nginx nginx -s reload || true
  start_php_fpm

  cat <<'PHPEOF'
  Log in at http://<your-host>:80 and create the admin user:
    cd /var/www/pterodactyl && php artisan p:user:make
PHPEOF

  log "Creating node for the native daemon:"
  env -u DATABASE_URL -u DB_URL "$PHP" artisan p:native:setup-node --fqdn=127.0.0.1 --listen=8080 --ports=20200-20250
}

install_daemon() {
  log "Installing ptero-native daemon to $DAEMON_DIR…"
  go_min_version "1.25"
  mkdir -p "$DAEMON_DIR/bin" /etc/ptero-native /var/lib/ptero-native/volumes /var/lib/ptero-native/backups /var/log/ptero-native
  (cd "$HERE/daemon" && CGO_ENABLED=0 go build -ldflags "-s -w" -o "$DAEMON_DIR/bin/ptero-native" ./cmd/ptero-native)
  chmod +x "$DAEMON_DIR/bin/ptero-native"
  if [ ! -f /etc/ptero-native/config.yml ]; then
    cat > /etc/ptero-native/config.yml <<'CFGEOF'
panel:
  url: http://127.0.0.1:80
  token: "<token_id>.<token>"     # from p:native:setup-node output
daemon:
  listen: 0.0.0.0:8080
  token_id: "<token_id>"          # same values as above
  token: "<token>"
  api_keys:
    "<token_id>": "<token>"
  data_path: /var/lib/ptero-native
  backup_path: /var/lib/ptero-native/backups
  tmp_path: /tmp/ptero-native
  username_prefix: vrp_
  upload_size_limit: 100
  # extra browser origins allowed for signed uploads/downloads (no wildcard):
  # cors_allowed_origins:
  #   - https://panel.example.com
limits:
  crash_restarts: 3
  crash_window: 60
  log_max_lines: 5000
debug: false
CFGEOF
    chmod 600 /etc/ptero-native/config.yml
  fi
  log "Daemon installed. Choose a service manager (all optional):"
  log "  systemd:   cp scripts/systemd/ptero-native.service /etc/systemd/system/ && systemctl enable --now ptero-native"
  log "  pm2:       pm2 start $DAEMON_DIR/bin/ptero-native --name ptero-native -- --config /etc/ptero-native/config.yml"
  log "  supervisor: cp scripts/supervisor/ptero-native.conf /etc/supervisor/conf.d/"
  log "  runit:     cp -r scripts/runit/ptero-native /etc/service/"
  log "  nohup:     nohup $DAEMON_DIR/bin/ptero-native --config /etc/ptero-native/config.yml >>/var/log/ptero-native/daemon.log 2>&1 &"
}

install_runtimes() {
  log "Provisioning native runtimes into $RUNTIME_DIR…"
  bash "$HERE/scripts/provision-runtimes.sh" "$RUNTIME_DIR"
}

case "$MODE" in
  --panel-only) install_panel ;;
  --daemon-only) install_daemon ;;
  --runtimes-only) install_runtimes ;;
  all) install_runtimes; install_panel; install_daemon ;;
  *) die "usage: install.sh [--panel-only|--daemon-only|--runtimes-only]" ;;
esac

log "Done."
