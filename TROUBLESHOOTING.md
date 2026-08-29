# Troubleshooting

## Node shows offline in the admin area
1. `curl -H "Authorization: Bearer <token_id>.<token>" http://<daemon>:8080/api/system`
   must return JSON. If not: daemon not running / wrong port / firewall.
2. `panel.url` in `config.yml` must be the Panel's URL **as the daemon sees it**.
3. Token format is `<token_id>.<token>` (from `p:native:setup-node`).

## Server stuck in "Starting"
* The egg's `done` lines mark the switch to `running`. If a custom egg's
  `config_startup.done` string never appears, the console stays in starting.
  Add the egg's real ready-line to the egg's startup config.

## Install fails immediately
* `GET /api/remote/servers/{uuid}/install` must return `{container_image, entrypoint, script}`.
  If the egg's script is empty, the daemon marks install complete instantly.
* Check the daemon log (`/var/log/ptero-native/daemon.log`) for the script output.

## File manager errors "created"/"modified"
* Panel 1.15 requires FileEntry `created`/`modified` (plus `file`, `symlink`,
  `mime`) from the daemon — ptero-native always sends them. If you see this on
  a stock Wings node, that node is the wrong one.

## 401 from daemon endpoints
* The panel sends `Bearer <daemon_token_id>.<daemon_token>`; both parts must
  match `daemon.api_keys` (or `token_id`/`token`) in the daemon config.
* WS/download tokens are signed with the **plain daemon token** — if the node
  was re-created, re-sync `config.yml`.

## Ports already in use
* Allocations are bound by the *server process*. If another daemon/panel
  instance runs on the same host, keep port pools disjoint.

## Migration errors mentioning `runtime_profiles`
* Run `php artisan migrate` after pulling the fork; seeders:
  `php artisan db:seed --class=Database\\Seeders\\NativeRuntimeSeeder`.

## Sandbox / CI quirks
* If `php artisan` misbehaves with `DATABASE_URL` set in the environment,
  unset it: `env -u DATABASE_URL -u DB_URL php artisan ...`
