# Vanilla Pterodactyl Native

**Pterodactyl Panel + native (Docker-free) runtime daemon** — run game/app servers on
NAT VPS and restricted Linux containers where Docker is unavailable: no
`CAP_NET_ADMIN`, no network namespaces, systemd optional.

* Official Pterodactyl Panel fork — **additive patches only** (UI/UX 100% intact).
* `ptero-native` — a single Go binary that speaks the **Wings HTTP/WS protocol** and
  supervises real processes instead of Docker containers.
* Original **Pterodactyl Eggs** work: docker images are mapped to native runtimes
  (Node 20/22, Python 3.11/3.12, Java 17/21, static, custom), install scripts run
  natively, startup configs are translated automatically.

## Documentation
| Doc | Content |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | system design, runtime mapping, data flow |
| [docs-audit/AUDIT-WINGS.md](docs-audit/AUDIT-WINGS.md) | full Wings protocol surface (daemon spec) |
| [docs-audit/AUDIT-PANEL.md](docs-audit/AUDIT-PANEL.md) | panel-side expectations + fork delta |
| [INSTALL.md](INSTALL.md) | installation (Ubuntu 24.04, NAT VPS) |
| [docs/NETWORKING.md](docs/NETWORKING.md) | NAT/port-forwarding guidance |
| [docs/RUNTIMES.md](docs/RUNTIMES.md) | runtime profiles & mappings |
| [docs/EGGS.md](docs/EGGS.md) | egg compatibility layer |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | common issues |
| [LIMITATIONS.md](LIMITATIONS.md) | documented deviations from Wings |

## Repository layout
```
panel/     Pterodactyl Panel fork (additive native-runtime patches)
daemon/    ptero-native Go daemon source
scripts/   service units (systemd/pm2/supervisor/runit), nginx, runtimes provisioner
tests/     eggs + E2E suite
docs*/     documentation & audits
install.sh installer entrypoint
```

## Status
v1.0.0 — see `PERSISTENCE_TEST.md` and the git history for the full build log.
