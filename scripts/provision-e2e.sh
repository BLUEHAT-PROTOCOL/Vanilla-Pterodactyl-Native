#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# Provision the portable E2E toolchain (no root, no systemd required):
#   - Go toolchain                       -> /home/z/toolchain/go
#   - static PHP 8.3 CLI (pdo_mysql etc) -> /home/z/toolchain/php
#   - Composer                           -> /home/z/toolchain/bin/composer
#   - MariaDB 11.4.5 bintar + libaio     -> /home/z/e2e/mariadb + downloads/
#
# After running this, execute scripts/setup-panel-e2e.sh (creates the panel
# .env, initializes the database, migrates and seeds) and then run the full
# acceptance suite with:  bash tests/e2e/run.sh
# ---------------------------------------------------------------------------
set -uo pipefail

TC="${TC:-$HOME/toolchain}"
E2E="${E2E_BASE:-/home/z/e2e}"
DL="$E2E/downloads"
mkdir -p "$TC/bin" "$DL" "$E2E/mariadb" "$DL/usr/lib/x86_64-linux-gnu"

echo "== [1/5] Go toolchain =="
if [ ! -x "$TC/go/bin/go" ]; then
  GO_VER=$(curl -s --max-time 15 "https://go.dev/VERSION?m=text" | head -1)
  echo "latest go: $GO_VER"
  curl -sL -o "$DL/${GO_VER}.linux-amd64.tar.gz" "https://go.dev/dl/${GO_VER}.linux-amd64.tar.gz"
  rm -rf "$TC/go" && tar -C "$TC" -xzf "$DL/${GO_VER}.linux-amd64.tar.gz"
fi
"$TC/go/bin/go" version

echo "== [2/5] static PHP 8.3 CLI (bulk flavor: includes sodium for lcobucci/jwt) =="
if [ ! -x "$TC/php/php" ]; then
  PHP_TGZ=$(curl -s --max-time 20 "https://dl.static-php.dev/static-php-cli/bulk/" | \
    grep -oE 'php-8\.3\.[0-9]+-cli-linux-x86_64\.tar\.gz' | sort -V | tail -1)
  echo "php tarball: $PHP_TGZ"
  mkdir -p "$TC/php"
  curl -sL -o "$DL/$PHP_TGZ" "https://dl.static-php.dev/static-php-cli/bulk/$PHP_TGZ"
  tar -C "$TC/php" -xzf "$DL/$PHP_TGZ"
fi
"$TC/php/php" -v | head -1

echo "== [3/5] Composer =="
if [ ! -x "$TC/bin/composer" ]; then
  curl -sS --max-time 30 https://getcomposer.org/installer -o "$DL/composer-setup.php"
  "$TC/php/php" "$DL/composer-setup.php" --quiet --install-dir="$TC/bin" --filename=composer.phar
  printf '#!/usr/bin/env bash\nexec %s/php/php %s/bin/composer.phar "$@"\n' "$TC" "$TC" > "$TC/bin/composer"
  chmod +x "$TC/bin/composer"
fi
"$TC/bin/composer" --version 2>/dev/null | head -1

echo "== [4/5] libaio (mariadbd dependency) =="
if [ ! -e "$DL/usr/lib/x86_64-linux-gnu/libaio.so.1" ]; then
  for deb in \
    "http://archive.ubuntu.com/ubuntu/pool/main/liba/libaio/libaio1t64_0.3.113-8build1_amd64.deb" \
    "http://archive.ubuntu.com/ubuntu/pool/main/liba/libaio/libaio1t64_0.3.113-6build1_amd64.deb" \
    "http://archive.ubuntu.com/ubuntu/pool/main/liba/libaio/libaio1_0.3.112-13build1_amd64.deb"; do
    curl -sL --max-time 30 -o "$DL/libaio.deb" "$deb"
    if dpkg-deb -I "$DL/libaio.deb" >/dev/null 2>&1; then break; fi
    echo "unusable mirror artifact: $deb"
  done
  ( cd "$DL" && dpkg-deb -x libaio.deb debroot 2>/dev/null || \
    (ar x libaio.deb && mkdir -p debroot && tar -C debroot -xf data.tar.*) )
  find "$DL/debroot" -name 'libaio.so*' -exec cp -a {} "$DL/usr/lib/x86_64-linux-gnu/" \;
  # t64 builds ship libaio.so.1t64 only; mariadbd links libaio.so.1
  [ -e "$DL/usr/lib/x86_64-linux-gnu/libaio.so.1" ] || \
    ln -s libaio.so.1t64 "$DL/usr/lib/x86_64-linux-gnu/libaio.so.1"
fi
ls "$DL/usr/lib/x86_64-linux-gnu/" | head -2

echo "== [5/5] MariaDB 11.4.5 bintar =="
MDB_DIR="$E2E/mariadb/mariadb-11.4.5-linux-systemd-x86_64"
if [ ! -x "$MDB_DIR/bin/mariadbd" ]; then
  MDB_URL="https://archive.mariadb.org/mariadb-11.4.5/bintar-linux-systemd-x86_64/mariadb-11.4.5-linux-systemd-x86_64.tar.gz"
  echo "downloading mariadb (large, ~350MB)..."
  curl -sL -C - -o "$DL/mariadb-11.4.5.tar.gz" "$MDB_URL"
  tar -C "$E2E/mariadb" -xzf "$DL/mariadb-11.4.5.tar.gz"
fi
"$MDB_DIR/bin/mariadbd" --version

echo "== PROVISION DONE — now run: scripts/setup-panel-e2e.sh =="
