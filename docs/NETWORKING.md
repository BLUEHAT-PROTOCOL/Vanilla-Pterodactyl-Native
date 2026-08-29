# Networking — NAT VPS & restricted environments

The native daemon binds game ports **directly on the host** — there are no
network namespaces, no docker bridges, no userland proxies.

## Port map
| Port | Component | Reachability |
|---|---|---|
| 80/443 | Panel (nginx) | must be reachable by browsers |
| 8080 | ptero-native API + WebSocket | must be reachable by browsers AND panel |
| 20200-20250 | allocations (example pool) | must be **forwarded** from the public IP (NAT VPS) |
| 3306 | MariaDB | localhost only |

## NAT VPS setup
1. Forward a contiguous public range (e.g. `PUBLIC:20200-20250 ->
   10.0.0.5:20200-20250`) in your provider's panel.
2. Create allocations with `ip = <local bind IP>` (usually `0.0.0.0` or the
   private interface IP) and set `ip_alias = <public IP>` so the panel renders
   the public connect string.
3. Keep the daemon `listen: 0.0.0.0:8080`; forward `PUBLIC:8080 -> local:8080`
   for browser console/WS access.

## No CAP_NET_ADMIN?
Nothing here uses iptables/namespaces — you only need normal bind permissions
(ports >1024 by unprivileged server processes; the daemon binds nothing itself).

## Verifying reachability
```bash
curl -H "Authorization: Bearer <token_id>.<token>" http://<public>:8080/api/system
```
If this works from your laptop, the browser console WS and file manager will too.
