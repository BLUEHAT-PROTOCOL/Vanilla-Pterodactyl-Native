# Installation — Vanilla Pterodactyl Native

Target: **Ubuntu 24.04** (Debian 12 works), incl. **NAT VPS** and restricted
containers (no `CAP_NET_ADMIN`, no network namespaces, systemd optional for the
daemon).

## Architecture recap
* **Panel** — official Pterodactyl Panel v1.15.1 fork (additive native-runtime
  layer). PHP 8.3 + MariaDB + nginx.
* **ptero-native** — single Go binary speaking the Wings HTTP/WS protocol and
  supervising real processes (no Docker).
* **Runtimes** — Node 20/22, Python 3.11/3.12, Java 17/21 under `/opt/runtimes`.

## 1. Quick install (all-in-one)
```bash
sudo bash install.sh
```
This provisions runtimes, installs the panel (+ DB, migrations, seeders,
frontend build), and builds the daemon. It finishes by running
`p:native:setup-node`, which prints the daemon config block.

## 2. Manual install (piece by piece)

### 2.1 Runtimes
```bash
sudo bash scripts/provision-runtimes.sh /opt/runtimes
```

### 2.2 Panel
```bash
sudo bash install.sh --panel-only
```
Manual steps (what the installer automates):
1. `apt install php8.3-* mariadb-server nginx redis-server`
2. `composer install --no-dev --optimize-autoloader`
3. create DB `panel` + user, copy `.env.example .env`
4. `php artisan key:generate --force && php artisan migrate --seed --force`
5. `yarn install --frozen-lockfile && yarn build:production`
6. nginx vhost: `scripts/nginx/ptero-native.conf`
7. admin user: `php artisan p:user:make`

### 2.3 Daemon
```bash
sudo bash install.sh --daemon-only
```
1. Configure `/etc/ptero-native/config.yml` (values from `p:native:setup-node`).
2. Pick a service manager:
   * systemd: `cp scripts/systemd/ptero-native.service /etc/systemd/system/`
   * pm2: `pm2 start /opt/ptero-native/bin/ptero-native --name ptero-native -- --config /etc/ptero-native/config.yml`
   * supervisor: `cp scripts/supervisor/ptero-native.conf /etc/supervisor/conf.d/`
   * runit: `cp -r scripts/runit/ptero-native /etc/service/`

### 2.4 Connect panel ↔ daemon
1. `php artisan p:native:setup-node --fqdn=<panel-host> --listen=8080 --ports=20200-20250`
2. Copy the printed block into `/etc/ptero-native/config.yml`.
3. Restart the daemon; the node must show as online in the admin area.

## 3. NAT VPS specifics
* The panel needs **80/tcp** (or your web port) and the daemon needs **8080/tcp**
  reachable by the browser (console WS + file downloads).
* Game allocation ports (e.g. `20200-20250`) must be **forwarded** from the
  public IP to this machine — the daemon binds them directly (no NAT loopback
  hacks, no netns). See docs/NETWORKING.md.
* If the public IP is not bound locally, set allocation `ip` to the LAN/VPN IP
  (e.g. `10.0.0.5`) and `ip_alias` to your public IP so the panel shows the
  right connect string.

## 4. Restricted containers (no root / no CAP_NET_ADMIN)
The daemon runs unprivileged (falls back to shared-user isolation):
* per-server unix users are skipped (`vrp_*` only when root);
* everything else works (files, backups, console, installs);
* `config.yml` paths must be writable by the daemon user.

## 5. First server
1. Admin → Nests → import a standard egg (see tests/eggs/).
2. Admin → Servers → create server → pick the node + allocation.
3. The daemon pulls the config, runs the egg install script natively and
   reports back. Start the server from the UI — console, files, backups,
   schedules behave exactly like stock Pterodactyl.

## 6. Verify
```bash
curl -H "Authorization: Bearer <token_id>.<token>" \
  http://127.0.0.1:8080/api/system
```
