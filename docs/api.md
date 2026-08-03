# API

Health `service` id: `opa-hub`.

## Health

`GET /api/health`

```json
{"status":"ok","service":"opa-hub","version":"0.2.0","agents":0,"clickhouse":true,"topology":"hub-spoke"}
```

## Agent registry

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/agents/register` | Enroll token | Register or refresh an edge agent; returns `agent` including `id` |
| `POST` | `/api/agents/heartbeat` | Enroll token | Refresh last-seen (`{"agent_id":"…"}`) |
| `GET` | `/api/agents` | — | List registered agents |
| `GET` | `/api/agents/{id}` | — | Get one agent |

Enroll token: header `X-OPA-Enroll-Token: <token>` or `Authorization: Bearer <token>` when `OPA_HUB_ENROLL_TOKEN` is set on the hub.

### Register body

```json
{
  "agent_id": "optional-stable-id",
  "hostname": "app-1",
  "version": "1.2.3",
  "organization_id": "default-org",
  "project_id": "default-project",
  "labels": {"env": "prod"}
}
```

## Ingest (edge push)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/ingest/push` | Enroll token | Accept a telemetry batch from an edge agent |

```json
{
  "agent_id": "agent-…",
  "kind": "spans",
  "batch_id": "optional",
  "event_count": 12,
  "events": []
}
```

Accepted kinds: `spans` / `traces`, `metrics`, `logs`, `profiles`, `mixed`. The hub records write-hook stats and routes table names for the Open-ClickHouse-Go writer.

## Auth (dashboard issuer)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/auth/login` | Issue user token (`username` / `password`) |
| `POST` | `/api/auth/register` | Register a local user |
| `POST` | `/api/auth/logout` | Clear client session |
| `GET` | `/api/auth/status` | Auth status / current user |

Lab default user: `admin` / `admin` (change immediately; set a strong `JWT_SECRET` for shared deployments).

## Query / admin skeleton

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/query` | Capability advertisement for dashboard wiring |
| `GET` | `/api/admin` | Operator summary (agents, ClickHouse write-hook stats) |
| `GET` | `/api/services` | Empty services list placeholder (`source: opa-hub`) |

Full trace/metric/log query surfaces continue to seed on the hub as ownership moves off the edge agent.
