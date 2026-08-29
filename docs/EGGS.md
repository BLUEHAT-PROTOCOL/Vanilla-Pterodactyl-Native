# Eggs — original Pterodactyl egg compatibility

Stock eggs import unchanged (Admin → Nests → Upload). The native daemon
implements the egg contract without Docker:

## What happens natively
| Egg feature | Native implementation |
|---|---|
| `startup` | `{{VAR}}`→`${VAR}` + docker-path rewrite, `bash -c` in data dir |
| `docker_images` | mapped via runtime_mappings (see docs/RUNTIMES.md) |
| `config_files` (file/yaml/properties/ini/json) | parsed & edited by the daemon on boot (find/replace semantics) |
| `config_startup.done` | console line detection flips state starting→running |
| `config_stop` | `command` → stdin; `^SIG` → process-group signal |
| `script_install` | run natively via bash, env identical to wings, 30 min cap, output streamed to console/WS |
| `file_denylist` | honored for file-manager edit restrictions (panel side) |

## Compatibility audit
`php artisan p:eggs:compat-audit [--fix]` classifies every egg:
* `native` — runs as-is under a mapped runtime
* `mapping` — image mapped, needs config translation (handled)
* `manual` — install script may require host packages (check notes)
* `unsupported` — startup references docker/system services

## Test eggs
`tests/eggs/` ships minimal eggs used by the E2E suite:
* `nodejs.json` — generic Node app (file-parser config)
* `python.json` — Python app
* `java.json` — Java jar
* `static.json` — static file server

## Writing native-friendly eggs
1. Use `file`/`properties`/`yaml`/`json` parsers (all supported).
2. Keep install scripts POSIX-bash; rely on the runtime's PATH (node/python/java
   are pre-provisioned).
3. Reference paths as `/home/container` (translated automatically).
