# Runtimes — profiles & mappings

## Resolution order (daemon)
1. `settings.runtime` (Panel patch: `"profileSlug|envPath"`) — set from
   `runtime_mappings` when the server was created.
2. Daemon-local mappings synced from `GET /api/remote/runtime/mappings`.
3. Built-in defaults for official `ghcr.io/pterodactyl/yolks:*` images.
4. Fallback: `custom` profile (run startup with system PATH).

## Profiles (seeded)
| Slug | Binary | Path | Covers |
|---|---|---|---|
| node20 | node | /opt/runtimes/node20/bin | yolks:nodejs_18/20 |
| node22 | node | /opt/runtimes/node22/bin | yolks:nodejs_22 |
| python311 | python3 | /opt/runtimes/python311/bin | yolks:python_3.11 |
| python312 | python3 | /opt/runtimes/python312/bin | yolks:python_3.12 |
| java17 | java | /opt/runtimes/java17/bin | yolks:java_17 |
| java21 | java | /opt/runtimes/java21/bin | yolks:java_21/22 |
| static | — | — | yolks:debian/ubuntu/alpine |
| custom | — | — | anything else |
| go / rust | — | — | pre-built binaries |

## Environment contract
The server process receives (plus every egg variable, uppercased):
`SERVER_IP`, `SERVER_PORT`, `SERVER_MEMORY`, `SERVER_UUID`, `SERVER_NAME`,
`P_SERVER_UUID`, `P_SERVER_ALLOCATION_LIMIT`, `P_SERVER_HOST_IP`,
`P_SERVER_HOST_PORT`, `HOME`/`PWD` = data dir, `PATH` prefixed with the
profile's `env_path`.

## Startup translation
* `{{VAR}}` → `${VAR}` (bash interpolation from the env above)
* `/home/container` and `/mnt/server` → server data dir
* Executed as `bash -c` inside the data dir with its own process group.
