# Limitations vs stock Wings (native runtime)

The native daemon implements the full Panel-facing Wings surface that the UI
and client APIs exercise. Deviations, all intentional:

1. **No Docker layer**
   * `container.image` is *translated*, not pulled. Images without a mapping
     run under the `custom` profile with the system PATH.
   * Docker-specific egg features (image entrypoint args, GPU, device
     passthrough, docker network modes) are ignored.

2. **Isolation model**
   * Root daemon: per-server unix users (`vrp_<short-uuid>`), 0700 data dirs,
     process groups. This is OS-level user isolation, not namespaces.
   * Unprivileged daemon: shared isolation (documented; for restricted
     containers where root is impossible).
   * No cgroups: memory/CPU limits are best-effort (ulimit + monitoring);
     **disk quota is enforced** (write/backups/upload guards).

3. **Backups**
   * Local `tar.gz` + sha256 only. S3/B2 adapters are not implemented
     (Panel still accepts the backup and stores metadata).

4. **Transfers / archives**
   * Server transfers and `POST /api/servers/{uuid}/archive` are accepted and
     no-op'd (`archived: false`).

5. **SFTP**
   * Not implemented (Panel's SFTP subsystem and the daemon's `/api/remote/sftp`
     callback are unused). File manager covers the same files over HTTPS.

6. **Resource monitoring**
   * CPU/memory sampled from `/proc` per process group every second (Wings
     uses cgroups). Values are accurate per-process; page-cache is not
     attributed.

7. **Install scripts**
   * Run under the daemon host's runtimes (`/opt/runtimes/...`), as the
     server's unix user when privileged. Scripts assuming the docker base
     image's package manager may need packages pre-provisioned via
     `scripts/provision-runtimes.sh` or the host OS.

Everything else — power lifecycle, console WS (with done-line detection and
stdin), files API incl. upload/pull/compress/chmod, signed downloads,
schedules (panel-side), activity, user management, sub-users — matches the
stock behavior.
