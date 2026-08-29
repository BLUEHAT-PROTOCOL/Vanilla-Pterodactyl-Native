# AUDIT — Panel-side expectations for the Native Runtime

What the official Pterodactyl Panel (v1.x, Laravel) expects from a node, and the minimal
additive changes required so the Panel works **without Docker** while keeping the
original UI/UX, Eggs, and client APIs intact.

---

## 1. Panel ↔ node communication surface
* `nodes` table: `daemon_token_id`, `daemon_token` (encrypted), `fqdn`, `scheme`,
  `daemon_listen` (default 8080), `daemon_sftp` (unused by native), `maintenance_mode`,
  `upload_size` (MB, used for client upload chunk validation).
* Panel → node calls go through `Repositories\Wings\*Repository` classes (HTTP, base
  `http://<fqdn>:<daemon_listen>`), timeout 15 s (configurable).
* Node → Panel calls go to `/api/remote/*` authenticated with the node's daemon token
  (`Bearer <token>` where token = `daemon_token_id . daemon_token` joined with a dot —
  `DaemonTokenRepository::get()` builds `<token_id>.<token>`).

## 2. What breaks if Docker info is absent (audit conclusion)
* `GET /api/system` — Panel's `Nodes\SystemInformationViewRequest` + admin node edit page
  reads `docker.versions`; the admin UI tolerates a **missing/null** docker block
  (it renders empty version strings). The native daemon reports
  `docker: {"versions": null, "driver": null, "info": null}` explicitly.
* Server creation flow (`ServersService`): by default it selects a Docker image from the
  Egg and passes it to the daemon inside `container.image` / `settings.image`. The native
  daemon does not pull images — it maps them. Therefore the Panel needs **no behavioral
  change** to create servers; it keeps sending the docker image string and the daemon
  translates it. (This is the key insight that makes the fork "additive only".)
* Egg validation (`Egg` + `Nests`): no docker-specific validation exists on Egg import —
  docker images are free-form strings. Egg import works unmodified.
* Install flow: Panel sends `GET /api/servers/{uuid}/install` request payload containing
  the egg's script + container image + entrypoint. The native daemon ignores the docker
  parts and executes the script natively. **No Panel change needed.**

## 3. Required additive Panel changes (the "fork delta")
Minimal additive-only modifications (3 small edits + new files):

### 3.1 New migrations
| Migration | Columns |
|---|---|
| `create_runtime_profiles_table` | `id`, `uuid` (char36), `name`, `slug` (unique), `binary` (e.g. `node`), `binary_args` (e.g. `src/index.js`), `version_field`?, `description`, `supported_versions` (json array), `default_image` (docker image string this profile covers), timestamps |
| `create_runtime_mappings_table` | `id`, `profile_id` (FK), `docker_image` (unique, e.g. `ghcr.io/pterodactyl/yolks:nodejs_20`), `runtime_version` (e.g. `20`), `env_path` (e.g. `/opt/runtimes/node20/bin`), `extra_env` (json), `executable_fallback`?, timestamps |
| `add_native_compat_to_eggs` | `eggs.native_compat` (string enum: `native`, `mapping`, `manual`, `unsupported`, null) + `native_notes` (text, nullable) |
| `add_r_runtime_to_api_keys` | `api_keys.r_runtime` (tinyint, default 0) — permission flag for the runtime API |

### 3.2 New models / controllers / routes
* Models: `RuntimeProfile`, `RuntimeMapping` (+ relations to Egg/Node as needed).
* Application API routes (registered in `routes/api-application.php` under
  `/api/application/runtime/...`, guarded by `api_keys.r_runtime` or admin key scope):
  - `GET /runtime/profiles`, `GET /runtime/profiles/{uuid}`
  - `POST /runtime/profiles`, `PATCH /runtime/profiles/{uuid}`, `DELETE /runtime/profiles/{uuid}`
  - `GET /runtime/mappings`, `POST /runtime/mappings`, `PATCH/DELETE /runtime/mappings/{id}`
  - `GET /runtime/resolve?image=<docker-image>` → `{profile, mapping}` (the daemon/ops
    tooling uses this to verify coverage)
* Transformers for the above (Fractal-style, same as other Panel application resources).

### 3.3 Artisan commands (native tooling)
* `p:native:setup-node` — interactive/non-interactive helper that creates a Node +
  default allocations pool (e.g. `20000-29999`), sets `daemon_token_*`, and prints the
  daemon config snippet (`panel_url`, `token_id`, `token`). Replaces the docker-centric
  default flow for native deployments.
* `p:eggs:compat-audit` — walks all Eggs, inspects `docker_images`, `startup`,
  `config_files`, `scripts.install` and assigns `eggs.native_compat`:
  - `native` — startup/invocation runs a plain binary available in the mapped runtime
    (node/python/java/static…), no docker-specific logic;
  - `mapping` — docker image has a runtime_mapping; invocation translatable
    (`{{VAR}}` env substitution is enough);
  - `manual` — install script/config files contain docker-only commands that need the
    egg-compat layer (documented per-egg notes);
  - `unsupported` — requires docker features native runtime cannot provide (rare).

### 3.4 Seeders
* `NativeRuntimeSeeder` — 10 runtime profiles: `node20`, `node22`, `python311`,
  `python312`, `java17`, `java21`, `static` (nginx/caddy static file server),
  `custom` (user-supplied command), plus `mono`/`dotnet` placeholders off by default —
  with matching default mappings for the official `ghcr.io/pterodactyl/yolks:*` images
  (nodejs_18→node20 profile mapping, nodejs_20, nodejs_22, python_3.11, python_3.12,
  java_17, java_21, java_22→java21, ghcr.io/pterodactyl/yolks:debian→static).

### 3.5 The 3 small edits to existing Panel files
1. `app/Models/Egg.php` — add `native_compat`, `native_notes` to `$fillable` (casts).
2. `app/Models/ApiKey.php` — add `r_runtime` to permission whitelist used by
   `app\Http\Middleware\AuthenticateApplicationKey` (route-level `r_runtime` check
   alongside r_servers/r_nodes…).
3. `app/Services/Servers/ServerCreationService.php` — after build-data assembly, attach
   `settings.runtime` (resolved via RuntimeProfile/RuntimeMapping from the egg's docker
   image) to the daemon creation payload. **Pure addition** — the daemon treats unknown
   keys as opaque; old Wings ignores it harmlessly.

## 4. Panel-side data the daemon needs per server
Fetched via `GET /api/remote/servers/{uuid}` (shape documented in AUDIT-WINGS §4). Key
fields: `uuid`, `name`, `jwt_secret`, `user`, `suspended`, `egg_id`, `container`
(`{"startup_command", "image", "stop_command", "environment", "installed"}`),
`allocations` (`default.ip/port`, `mappings`), `build` (limits), `relationships.egg`
(variables with validation rules when `include=variables`).

Notes:
* `container.image` is the authoritative docker image string → runtime mapping key.
* `environment` map = egg variables resolved (user_value or default) + Panel built-ins
  (`SERVER_IP`, `SERVER_PORT`, `SERVER_UUID`, `SERVER_MEMORY`, `SERVER_NAME`, `P_SERVER_*`).
* `installed` ∈ {0,1,2} (0=never installed, 1=installed, 2=install failed). The daemon
  uses this to decide whether to auto-run install after server create.

## 5. Client features that must keep working (acceptance surface)
* Login/2FA, admin area, user area — untouched upstream UI.
* Server console (WS), power controls, commands, resource graphs.
* File manager: list/edit/rename/copy/move/chmod/compress/decompress/upload/download/
  new file/new folder — depends on FileEntry `created`/`modified` fields (see AUDIT-WINGS §3.3).
* Allocations: assignment UI in admin (Panel DB) + daemon exports SERVER_IP/SERVER_PORT.
* Schedules/tasks: Panel-side cron (`p:schedule:process`) sends power/commands via its
  node repositories → works unchanged once the daemon serves power/commands APIs.
* Backups: create/list/delete/download/restore with local adapter → daemon tar.gz.
* Sub-users, API keys (application + client), activity logs — untouched Panel features.

## 6. Install-time environment variables (egg contract)
Panel builds `environment` from egg variables; the daemon additionally exposes:
`SERVER_IP`, `SERVER_PORT`, `SERVER_MEMORY`, `SERVER_UUID`, `SERVER_NAME`, `SERVER_OWNER`,
`P_SERVER_UUID`, `P_SERVER_ALLOCATION_LIMIT`, `P_SERVER_LOCATION`, `P_SERVER_HOST_IP`,
`P_SERVER_HOST_PORT`, and every egg variable under both its raw name and uppercase form.
Install scripts also receive the egg `script_env` values. `cd` start dir = server data
root. `{{SERVER_PORT}}`-style placeholders in scripts/startup are interpolated by the
daemon from this environment (docker-image references are ignored).

## 7. Risks / gotchas discovered in the previous audit cycle
1. **FileEntry timestamps** — the Panel React file manager requires `created`+`modified`
   per file; missing fields break the file manager UI (this was the last bug fixed
   before the previous E2E 35/38 run — fix is pinned in AUDIT-WINGS §3.3).
2. Sandbox env hijack — when running Panel artisan in the dev sandbox, unset
   `DATABASE_URL`/`DB_URL` or Laravel parses them as its DSN.
3. `GET /api/system` must include `system.type` etc. or admin dashboard JS throws.
4. Panel checks `settings.image` presence for display only — daemon must echo it back in
   `GET /api/servers/{uuid}` responses to keep admin pages happy.
5. The frontend dev build (`yarn build:production`) requires Node ≥18 — sandbox has 24;
   fine. Composer platform check requires PHP ≥8.2/8.3 depending on panel version.
