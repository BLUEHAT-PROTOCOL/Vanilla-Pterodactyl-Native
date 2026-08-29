# AUDIT — Wings Daemon Protocol Surface (what `ptero-native` must implement)

Scope: protocol-compatible replacement of Wings without Docker. This document maps every
HTTP route and WebSocket event that the official Pterodactyl Panel (v1.x) exchanges with
Wings, so the native daemon can satisfy the Panel byte-for-byte where it matters.

---

## 1. Authentication model

### 1.1 Panel → Daemon (HTTP)
* Header: `Authorization: Bearer <token_id>.<token_body>`
* The daemon holds a static list of credentials in its config (Wings: `api_keys` map of
  `token_id → token`). Both parts are opaque strings; Wings splits the header on the first
  `.` and compares both parts in constant time.
* Every route under `/api/*`, `/download`, `/upload` requires a valid token.
* The Panel stores `daemon_token_id` and `daemon_token` per node; it always sends
  `<daemon_token_id>.<daemon_token>`.

### 1.2 Daemon → Panel (remote API)
* Header: `Authorization: Bearer <token>` where `<token>` is the node's full daemon token
  as stored by the Panel. The Panel validates it against `nodes.daemon_token_id` +
  `nodes.daemon_token` (it splits on `.` — therefore the full token has the form
  `<token_id>.<secret>`; the daemon must present it exactly as configured).
* TLS verification: Wings enforces TLS when the panel URL is https; the native daemon
  keeps the same behavior with an optional `allow_insecure` flag for testing.

### 1.3 Browser → Daemon (WebSocket / signed URLs)
* WebSocket: `GET /api/servers/{uuid}/ws?token=<JWT>` (HS256, signed by the **Panel**
  with the server's `jwt_secret` which the daemon receives from the remote server detail).
  Claims checked by Wings: `sub` == server UUID, `unique_id` non-empty, `exp`.
  Additional per-route token (files download/upload) carries `servers` (uuid→permission
  map). The daemon must reject expired tokens and emit `token expiring` /
  `token expired` events when its own re-minted permission token ages out (Wings mints
  internal tokens with 15-minute TTL for permissions; panels/browsers handle re-auth).
* File download for users: Panel signs `GET /download?token=<JWT>` (claims: `server_uuid`,
  `unique_id`, `permissions`). Backup download likewise (`/api/servers/{uuid}/backups/{b}/download`
  is called by Panel server-side, so normal bearer auth applies).
* Upload: Panel asks the daemon for a signed one-shot token (`GET /api/upload`), the
  browser then POSTs multipart to `/upload?token=<JWT>` with a `files` field and the
  target directory in `X-Data-Dir` header.

## 2. Remote endpoints the daemon may call on the Panel
Base: `<panel-url>/api/remote`
| Method | Path | Purpose |
|---|---|---|
| GET  | `/servers/{uuid}` | Full server config (id, build, allocations, environment, invocation, settings, jwt_secret…) |
| GET  | `/servers?ids=1,2,…` | Bulk fetch on boot (returns array of server detail) |
| GET  | `/servers/{uuid}/install` | Install script + container metadata for install |
| POST | `/servers/{uuid}/install/success` | Install finished OK |
| POST | `/servers/{uuid}/install/failed` | `{message}` — install failed |
| POST | `/servers/{uuid}/backups` | Backup completed: `{successful, checksum, checksum_type, size}` |
| POST | `/servers/{uuid}/backups/{backup}/restores` | Restore finished: `{successful}` |
| POST | `/servers/{uuid}/backups/{backup}/restores/failed` | `{message}` |
| GET  | `/servers/download/{token}` | Panel-mediated download (egg pulls / restore sources) |
| POST | `/servers/{uuid}/activity` | Activity event batch `[{server, event, ip, metadata…}]` |
| PATCH| `/servers/{uuid}` | Sync runtime metadata if needed (not used by native) |

Response envelope: plain JSON body (no `object` wrapper). 4xx/5xx → daemon logs + retries
with backoff where appropriate.

## 3. HTTP routes the daemon must serve (Panel & browser facing)
Default port 8080 (configurable). All JSON unless noted.

### 3.1 System
| Method | Path | Notes |
|---|---|---|
| GET | `/api/system` | `{version, docker:{versions:{containerd, docker, …}} (may be null/omitted), system:{type, release, architecture, cpu_count, kernel_version}}` — **Panel tolerates a missing/null docker block** (verified against panel source). Native daemon reports `docker: {versions: null}` + system info from uname. |
| GET | `/api/system/config` | Node runtime config echo (wings: allocations, limits, banners) — used by admin autoconfig; harmless to implement. |

### 3.2 Server lifecycle
| Method | Path | Notes |
|---|---|---|
| GET | `/api/servers` | Paginated list `{data:[…], meta:{pagination:{total, count, per_page, current_page, total_pages}}}` of lightweight server summaries (uuid, name, state) |
| GET | `/api/servers/{uuid}` | `{settings, build, allocations}` summary |
| POST | `/api/servers` | Create server: body `{uuid, settings, build, allocations, environment, invocation, container{image?}, …}` — native daemon stores it verbatim (see §4) |
| PATCH | `/api/servers/{uuid}` | Update build/allocations/settings/environment/invocation (partial) |
| PATCH | `/api/servers/{uuid}/install` | `{reinstall: bool}` — run install script |
| POST | `/api/servers/{uuid}/reinstall` | Alias of above with reinstall=true |
| DELETE | `/api/servers/{uuid}?force=` | Delete server (force kills running process, optionally wipes data) |
| POST | `/api/servers/{uuid}/power` | `{state: "start"|"stop"|"restart"|"kill"}` → 202 Accepted, async |
| POST | `/api/servers/{uuid}/commands` | `{commands:["…"]}` → 204; writes to console/stdin |
| GET | `/api/servers/{uuid}/logs?size=N` | `{"data":[…lines]}` (each line max 150 chars in wings; we cap at 4096) |
| GET | `/api/servers/{uuid}/ws` | **WS upgrade** with `?token=JWT` (§5) |

### 3.3 Files
| Method | Path | Body / Query | Response |
|---|---|---|---|
| GET | `/api/servers/{uuid}/files/list` | `?directory=/` | `{data: FileEntry[], meta:{pagination…}}` |
| GET | `/api/servers/{uuid}/files/contents` | `?file=/path` | raw bytes (Content-Type application/octet-stream) |
| POST | `/api/servers/{uuid}/files/write` | `?file=/path`, raw body | 204 |
| PUT | `/api/servers/{uuid}/files/rename` | `{root, files:[{from, to}]}` | 204 |
| POST | `/api/servers/{uuid}/files/copy` | `{location}` | 204 |
| POST | `/api/servers/{uuid}/files/create-directory` | `{root, name}` | 204 |
| POST | `/api/servers/{uuid}/files/delete` | `{root, files:[…]}` | 204 |
| POST | `/api/servers/{uuid}/files/compress` | `{root, files:[…]}` | FileEntry of the created `.tar.gz` |
| POST | `/api/servers/{uuid}/files/uncompress` | `{root, file}` | 204 |
| POST | `/api/servers/{uuid}/files/chmod` | `{root, files:[{file, mode}]}` (mode `"0755"`/`"0rwx"`) | 204 |
| POST | `/api/servers/{uuid}/files/pull` | `{url, root, file_name?, use_header?, foreground?}` | `{identifier}` |
| GET | `/api/servers/{uuid}/files/pull/{id}` | — | `{progress:{total, done, overall?}, state}` |
| DELETE | `/api/servers/{uuid}/files/pull/{id}` | — | 204 (cancel) |
| GET | `/download?token=JWT` | — | raw file stream (server file) |
| GET | `/api/upload` | — | `{upload_url:"/upload?token=JWT"}` |
| POST | `/upload?token=JWT` | multipart `files[]`, header `X-Data-Dir` | 204 |

**FileEntry JSON** (Panel React file manager consumes these fields — the `created` and
`modified` fields are REQUIRED by the frontend):
```json
{
  "name": "server.jar",
  "size": 42120960,
  "mode": 33188,
  "mode_bits": "0644",
  "created": "2026-01-15T08:30:00.000000Z",
  "modified": "2026-02-01T12:00:00.000000Z"
}
```
`mode` = decimal of Go FileMode (uint32). `mode_bits` = 4-digit octal string (e.g. "0644",
directories "0755"). `created`/`modified` = RFC3339 with microseconds + `Z` suffix.

### 3.4 Backups
| Method | Path | Notes |
|---|---|---|
| POST | `/api/servers/{uuid}/backups` | `{adapter:"local"|"s3", uuid?…}` → start async; completion POSTed to Panel (§2) |
| GET | `/api/servers/{uuid}/backups` | List backups `{data:[{uuid, name, …, successful, state}]}` (daemon-local view) |
| GET | `/api/servers/{uuid}/backups/{b}/download` | `{path}` signed URL or direct stream; native returns direct stream via `/download/backup?token=…` style endpoint |
| DELETE | `/api/servers/{uuid}/backups/{b}` | Remove backup file + state |
| POST | `/api/servers/{uuid}/backups/{b}/restore` | `{truncate_directory:bool}` (local adapter) → async; completion POSTed to Panel |

Native implementation detail: backup = `tar.gz` of the server data directory, stored under
`<data>/backups/<uuid>.tar.gz`; `checksum_type: sha256`, `checksum: <hex>`, `size: <bytes>`.
(The Panel ≥1.11 accepts `checksum_type`; older accepted sha1 — sha256 is what current
Panel stores in `checksum_sha256`.)

### 3.5 Status codes / error envelope
Errors: `{"errors":[{"code":"…","status":"…","detail":"…"}]}` with proper HTTP status
(404 unknown server, 422 validation, 500 internal, 502 when process can't start…).
Power endpoints return 202 (async work), commands 204, delete 204.

## 4. Server configuration object (what Panel POSTs to the daemon)
The daemon persists this verbatim; it is also retrievable from
`GET /api/remote/servers/{uuid}` after a daemon restart (source of truth = Panel):

```jsonc
{
  "uuid": "<server uuid>",
  "settings": {
    "uuid": "…", "user": 988,            // ignored by native (we map per-server linux users)
    "egg": { "id": 1, "name": "…" },
    "image": "ghcr.io/pterodactyl/yolks:nodejs_20",   // consumed by RUNTIME MAPPING, not by docker
    "stopped": false, "environment": {"SERVER_UUID": "…", …}
  },
  "build": {
    "memory_limit": 512, "swap": 0,
    "io_weight": 500, "cpu_limit": 0, "threads": null,
    "disk_space": 1024, "oom_disabled": false
  },
  "allocations": {
    "force_outgoing_ip": false,
    "default": {"ip": "0.0.0.0", "port": 25565, "ip_alias": null, "notes": null},
    "mappings": {"0.0.0.0": [25565, 25566]}
  },
  "environment": { "SERVER_PORT": "25565", "…": "…" },
  "invocation": "node src/index.js",   // docker-style entrypoint args (no shell)
  "container": { "image": "…", "requires_container": false }
}
```

Native interpretation:
* `settings.image` → looked up in `runtime_mappings` (see docs/ARCHITECTURE.md §Mapping);
  if a `settings.runtime` key is present (Panel patch adds it) it takes precedence.
* `invocation` — Panel's docker-args string; native converts `{{VAR}}` → `${VAR}`,
  wraps with `bash -c` and exports all `environment` variables.
* `allocations.mappings` — the daemon does NOT create network namespaces; ports are bound
  directly on the host by the server process. The daemon pre-flights availability and
  exports `SERVER_IP`/`SERVER_PORT` env vars.
* `build` limits → enforced natively: `memory_limit` via `ulimit -v` (best effort) +
  cgroup-less monitoring; `disk_space` via quota guard on writes/backups; `cpu_limit` is
  advisory (exported as env var, enforced with `nice`/`taskset` when possible).

## 5. WebSocket protocol
`wss?://node:8080/api/servers/{uuid}/ws?token=<JWT>`

Client → daemon (JSON `{event, args[]}'):
| Event | Args | Meaning |
|---|---|---|
| `auth` | [] | (Wings re-auth after token expiring) — daemon re-checks fresh token from query only; treat as no-op |
| `set state` | ["running"] | Client-initiated state set — Panel never sends this; ignore politely |
| `send commands` | ["cmd1", "cmd2"] | Write to process stdin |
| `send logs` | ["-100"] | Replay last N lines of console |
| `send stats` | [] | Emit one `stats` event immediately |
| `send install logs` | [] | Replay install output buffer |

Daemon → client:
| Event | Payload | Meaning |
|---|---|---|
| `console output` | `{line: "…"}` | stdout/stderr lines |
| `status` | `{state: "…"}` | lifecycle states: installing, install_failed, install_completed, starting, running, stopping, stopped, crashed |
| `stats` | `{data: {...}}` | resource usage (see below) |
| `install output` | `{line}` | install script output |
| `install status` | `{status: "running"|"done"|"failed"}` | install lifecycle |
| `token expiring` | `{seconds_left}` | WS token near expiry |
| `token expired` | `{}` | client must reconnect with new token |
| `daemon message` | `{message}` | human-readable daemon notices |

`stats.data` shape (Panel console parses this):
```jsonc
{
  "uptime": 12345,          // seconds
  "memory": { "current": 84934656, "limit": 536870912 },
  "cpu": { "absolute": 0.53, "used": 0.53, "limit": 0 }   // percents; limit 0 = unlimited
}
```
Emission: every 1 s while the server is running (Wings uses ~500 ms; 1 s is within
Panel tolerance), plus on-demand via `send stats`.

## 6. Process model (native replacement for docker)
* Server process: `bash -c "<invocation-with-interpolated-vars>"` executed inside the
  server data directory with its own **process group** (`setpgid`), so stop = `SIGSTOP`
  group + `SIGKILL` group after grace period (Wings behavior), kill = immediate group
  SIGKILL.
* Isolation: when the daemon runs as root, each server gets a dedicated Linux user
  `vrp_<8 first chars of uuid>`, data dir chowned 0700, and the process runs with
  `syscall.Credential{Uid, Gid, Setpgid: true}`. When the daemon runs unprivileged
  (restricted containers), fall back to the daemon user (documented limitation).
* Crash detection: non-zero exit within `crash_detect_window` (default 60 s) of start
  ⇒ counter++ ⇒ auto-restart with 5 s delay (configurable); if > `crash_loop_budget`
  (default 3) restarts within 60 s ⇒ state `crashed`, no further auto-restart.
* Logs: ring buffer (default 5000 lines) + optional on-disk log file; each line capped.

## 7. Security hardening checklist (native)
* Path traversal: every files-API path is resolved and must stay inside the server data
  root (`filepath.Clean` + prefix check + `os.Lstat` symlink check; symlink escape blocked).
* Archive safety: `uncompress` guards against zip-slip / tar traversal (reject `..`,
  absolute paths, symlink members), and enforces a size budget (server disk limit).
* Upload/pull: same path rules; pull downloads to temp then moves; total size capped by
  remaining disk quota.
* Permissions: chmod endpoint refuses to touch files outside data root; setuid bits are
  stripped (mode masked 07777 & ^06000).
* Disk quota: tracked per server (sum of data dir + backups) against `build.disk_space`;
  writes that exceed quota are refused with 507-ish error mapping.
* Tokens: WS/signing JWTs use HS256 with per-server secrets from the Panel; bearer
  comparisons constant-time; the daemon never logs tokens.

## 8. Endpoint compatibility matrix vs Wings (native daemon)
| Area | Wings | ptero-native | Note |
|---|---|---|---|
| system/update (GET/POST /api/update) | docker image pull info | returns static ok | Panel update-checker unused in native |
| transfers | yes | **not implemented** (out of scope) | Panel transfer UI unused |
| s3 backups | yes | **not implemented** (local only) | documented limitation |
| powers (start/stop/restart/kill) | yes | yes | process-group based |
| files (12 endpoints + upload/download) | yes | yes | full parity incl. pull progress |
| ws console | yes | yes | full event parity |
| backups (tar.gz, sha256) | yes (tar.gz/b2/s3) | yes (tar.gz local) | |
| install scripts | docker exec | native bash runner | egg-compatible env + callbacks |
| user isolation | docker namespaces | per-server unix users / shared fallback | |
| resource monitoring | cgroups | /proc sampling + quota guards | |
