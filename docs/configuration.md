# Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `JWT_SECRET` | `` | User JWT signing secret (≥32 bytes recommended when `OPA_AUTH_REQUIRED=1`) |
| `OPA_AUTH_REQUIRED` | `` | When `1`/`true`/`yes`/`on`, dashboard and tenancy discovery routes expect authenticated callers |
| `AUTH_MODE` | `` | Reported on `/api/health` as `auth_mode` (`standalone` \| `codeployed`; health defaults to `standalone` when unset) |
| `OPEN_SERVICE_JWT_SECRET` | `` | Service JWT mint/validate secret (peer calls; prefer distinct from `JWT_SECRET`) |
| `CLICKHOUSE_URL` | `http://clickhouse:8123` | Central ClickHouse HTTP endpoint |
| `CLICKHOUSE_DB` | `opa` | ClickHouse database name (preferred). Alias: `CLICKHOUSE_DATABASE` |
| `CLICKHOUSE_USER` | `` | Optional ClickHouse username |
| `CLICKHOUSE_PASSWORD` | `` | Optional ClickHouse password |
| `OPA_PUBLIC_URL` | `` | Public URL advertised for deep links |
| `OPA_HUB_ENROLL_TOKEN` | `` | Shared secret edge agents present on register/push (`X-OPA-Enroll-Token` or `Authorization: Bearer …`). Empty disables enroll auth (lab only). |
| `OPA_HUB_AGENT_STALE_AFTER` | `5m` | Duration after which an agent is marked `stale` without heartbeat/push |
| `CORS_ORIGIN` | `` | Optional `Access-Control-Allow-Origin` value for dashboard origins |
| `REDIS_URL` | empty | Dedicated `redis-opa` for encrypted OAM directory stale cache (`internal/oamdir`) |
| `OPA_SEC_KEY_PREFIX` | `opa:sec:` | Redis key prefix for hub security cache |
| `PEER_OAM_URL` | empty | When set, org picker reads durable OAM directory (L2 Redis + 30s in-memory TTL); project switcher uses `GET /api/oam/projects?product=opa` (proxied to OAM, `id`→`project_id` alias) |

## Edge agent pairing

On each edge `opa-agent`:

| Variable | Description |
|----------|-------------|
| `OPA_HUB_URL` | Base URL of this hub (e.g. `https://opa-hub.example:8080`) |
| `OPA_HUB_ENROLL_TOKEN` | Same enroll token as the hub |

## Dashboard

Point `OPA-Dashboard` at the hub only (`VITE_API_URL` → hub base URL). Do not configure edge agent URLs in the UI.
