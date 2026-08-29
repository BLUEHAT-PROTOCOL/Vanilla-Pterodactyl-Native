# Vanilla Pterodactyl Native — Architecture

Pterodactyl Panel + **native (Docker-free) runtime daemon** for NAT VPS and restricted
Linux containers. Goal: keep the official Panel UI/UX, Egg compatibility, and client
APIs 100% intact, while replacing the Docker/Wings backend with a native Go daemon
(`ptero-native`).

```
┌────────────────────────────────────────────────────────────────────┐
│ Browser (unmodified Pterodactyl frontend)                          │
│  • admin + client UI, file manager, console (WS), schedules, …     │
└───────────────┬──────────────────────────────┬─────────────────────┘
                │ HTTPS (Laravel)              │ WS + files + upload
┌───────────────▼──────────────┐   ┌───────────▼─────────────────────┐
│ Pterodactyl Panel (fork)     │   │ ptero-native daemon (Go)        │
│  • additive patches only     │   │  • Wings HTTP/WS protocol clone │
│  • runtime_profiles          │◄──┤  • native process supervisor    │
│  • runtime_mappings          │   │  • per-server unix user isol.   │
│  • eggs.native_compat        │   │  • files / backups / installs   │
│  • p:native:* tooling        │   │  • runtime mapping + egg compat │
└───────────────┬──────────────┘   └───────────┬─────────────────────┘
                │ MariaDB (source of truth)    │ direct bind (no netns)
┌───────────────▼──────────────────────────────▼─────────────────────┐
│ Linux host (Ubuntu 24.04, NAT VPS, restricted container OK)         │
│  node20/22 · python3.11/3.12 · java17/21 · static · custom          │
└─────────────────────────────────────────────────────────────────────┘
```

## 1. Components

### 1.1 Panel fork (Laravel, additive patch policy)
* Only **additions** + **3 tiny edits** (see docs-audit/AUDIT-PANEL.md §3.5). All
  upstream features keep working; upgrades remain feasible via rebase.
* New tables: `runtime_profiles`, `runtime_mappings`; new egg columns `native_compat`,
  `native_notes`; new api-key permission `r_runtime`.
* New tooling: `p:native:setup-node` (node + allocation pool bootstrap),
  `p:eggs:compat-audit` (egg docker→native classification).
* New application API: `/api/application/runtime/{profiles,mappings,resolve}`.

### 1.2 Native daemon (`ptero-native`, single Go binary)
* Serves the **Wings-compatible** HTTP + WebSocket API (full matrix in
  docs-audit/AUDIT-WINGS.md §8): power/commands/logs/install/reinstall, files (list,
  contents, write, rename, copy, mkdir, delete, compress, uncompress, chmod, pull),
  backups (tar.gz + sha256 + signed download + restore), upload, system info.
* **Runtime mapping** replaces Docker images:

| Docker image (egg) | native runtime | notes |
|---|---|---|
| `ghcr.io/pterodactyl/yolks:nodejs_18/20/22` | node 20 / 22 from `/opt/runtimes/node2x` | invocation passed through |
| `yolks:python_3.11/3.12` | python3.11/3.12 | |
| `yolks:java_17/21/22` | java 17/21 (22→21 fallback) | |
| `yolks:debian / ubuntu / alpine` | static runner (busybox httpd / python -m http.server) or custom command | |
| anything else | `custom` profile: run egg's startup line with system PATH | graceful degradation |

* Mapping resolution order: `settings.runtime` (Panel patch) → `runtime_mappings`
  (daemon-side copy of Panel's mapping table, synced from Panel API on boot) →
  built-in defaults (yolks) → `custom`.
* **Process model**: `bash -c` with interpolated `{{VAR}}`→`${VAR}` invocation, own
  process group, per-server unix user (`vrp_<short-uuid>`) when root, graceful
  `SIGSTOP→SIGKILL` stop, crash detection with restart budget, ring-buffer console.
* **Isolation without namespaces**: per-user file ownership + `umask 077`; no
  CAP_NET_ADMIN, no netns, no cgroups required (limits enforced natively:
  disk quota guard, ulimit memory best-effort, CPU advisory).
* **Egg compatibility layer** (`internal/eggcompat`):
  - startup translator: egg startup strings → native invocation;
  - config find/replace engine: supports `file` (plain), `yaml`, `properties`, `ini`,
    `json`, `xml` matchers from egg `config_files`;
  - docker path translation: `/mnt/server` and `/home/container` → server data dir;
  - install script runner: native bash, egg env, timeout + disk guard, output streamed
    to WS + install-success/failed callbacks to Panel.

### 1.3 Data flow (server create → running)
```
admin creates server (Panel UI)
  → Panel resolves runtime profile from egg image (ServerCreationService patch)
  → POST /api/servers (daemon) with settings{image, runtime}, build, allocations,
    environment, invocation
  → daemon persists config, creates vrp_* user, prepares data dir /var/lib/ptero-native/volumes/<uuid>
  → daemon PATCHes nothing; Panel triggers install (PATCH /install)
  → daemon runs egg install script natively → callbacks install/success → Panel marks installed
  → user presses Start → daemon interpolates invocation, binds nothing (proc binds),
    exports SERVER_IP/PORT env, process group starts, WS console live
```

### 1.4 Persistence & recovery
* Daemon state is **derived**: server configs come from the Panel (`/api/remote/servers`),
  backups live on disk, per-server metadata (installed, backups index) in
  `<data>/.ptero-native/state.json` per server. A daemon restart re-syncs everything;
  running processes (if the daemon itself survived, e.g. under a supervisor restart)
  are re-attached by PID file; otherwise states reset to `stopped` + auto-install check.

## 2. Directory layout (host)
```
/etc/ptero-native/config.yml          daemon config (panel url, tokens, ports)
/var/lib/ptero-native/volumes/<uuid>/ server data (chown vrp_*, 0700)
/var/lib/ptero-native/backups/        tar.gz backups
/var/log/ptero-native/                daemon logs (when not journald)
/opt/runtimes/{node20,node22,python311,python312,java17,java21}/
```

## 3. Security posture
* Panel↔daemon: bearer `<id>.<token>` both ways; WS/signing: HS256 JWTs
  (per-server `jwt_secret`, short TTL, permission-scoped claims).
* Path safety everywhere (traversal + symlink escape blocked), zip/tar slip guarded,
  upload/pull disk-quota guarded, setuid bits stripped on chmod.
* Daemon never logs tokens; per-server users prevent cross-server file access when
  running privileged; unprivileged mode documented (LIMITATIONS.md).

## 4. Compatibility guarantees (acceptance)
1. Official Panel frontend works unmodified (login → console → files → backups).
2. Official eggs (nodejs/python/java/generic) import and run natively.
3. `GET /api/system` renders the admin node dashboard.
4. All client APIs (power/commands/files/backups/schedules/subusers) behave as upstream.
5. Full E2E (tests/e2e) covers the acceptance chain end-to-end.
