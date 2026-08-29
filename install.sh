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
# systemd is REQUIRED for the panel services (php-fpm + nginx) but OPTIONAL
# for the daemon (systemd/pm2/supervisor/runit units in scripts/).
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

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "Missing dependency: $2 (install with: $2)"; }

install_panel_deps() {
  log "Installing panel dependencies (php 8.3, mariadb, nginx, redis)…"
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
  [ -f .env ] || cp .env.example .env
  # write db settings (idempotent enough for a first install)
  sed -i "s/^DB_DATABASE=.*/DB_DATABASE=panel/;s/^DB_USERNAME=.*/DB_USERNAME=pterodactyl/;s/^DB_PASSWORD=.*/DB_PASSWORD=$DBPASS/;s/^DB_HOST=.*/DB_HOST=127.0.0.1/" .env
  PHP=$(command -v php8.3 || command -v php)
  env -u DATABASE_URL -u DB_URL "$PHP" artisan key:generate --force
  env -u DATABASE_URL -u DB_URL "$PHP" artisan migrate --seed --force

  chown -R www-data:www-data "$PANEL_DIR"/*
  chmod -R 755 "$PANEL_DIR/storage" "$PANEL_DIR/bootstrap/cache"

  log "Building frontend (this can take several minutes)…"
  (command -v yarn >/dev/null 2>&1 || npm install -g yarn)
  corepack enable 2>/dev/null || true
  (cd "$PANEL_DIR" && yarn install --frozen-lockfile && yarn build:production)

  if [ -d scripts/nginx ]; then
    cp scripts/nginx/ptero-native.conf /etc/nginx/sites-available/pterodactyl.conf
    sed -i "s|__PANEL_DIR__|$PANEL_DIR|g" /etc/nginx/sites-available/pterodactyl.conf
    ln -sf /etc/nginx/sites-available/pterodactyl.conf /etc/nginx/sites-enabled/pterodactyl.conf
    nginx -t && systemctl reload nginx
  fi

  cat <<'PHPEOF'
  Log in at http://<your-host>:80 and create the admin user:
    cd /var/www/pterodactyl && php artisan p:user:make
PHPEOF

  log "Creating node for the native daemon:"
  env -u DATABASE_URL -u DB_URL "$PHP" artisan p:native:setup-node --fqdn=127.0.0.1 --listen=8080 --ports=20200-20250
}

install_daemon() {
  log "Installing ptero-native daemon to $DAEMON_DIR…"
  command -v go >/dev/null 2>&1 || die "Go 1.23+ is required to build the daemon (https://go.dev/dl/)."
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
limits:
  crash_restarts: 3
  crash_window: 60
  log_max_lines: 5000
debug: false
CFGEOF
  fi
  log "Daemon installed. Choose a service manager:"
  log "  systemd:   cp scripts/systemd/ptero-native.service /etc/systemd/system/ && systemctl enable --now ptero-native"
  log "  pm2:       pm2 start $DAEMON_DIR/bin/ptero-native --name ptero-native -- --config /etc/ptero-native/config.yml"
  log "  supervisor: cp scripts/supervisor/ptero-native.conf /etc/supervisor/conf.d/"
  log "  runit:     cp -r scripts/runit/ptero-native /etc/service/"
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
