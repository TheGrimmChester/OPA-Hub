# API

Health `service` id: `opa-hub`.

## Health

`GET /api/health`

```json
{"status":"ok","service":"opa-hub","version":"0.3.0","agents":0,"clickhouse":true,"topology":"hub-spoke"}
```

## Agent registry

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/agents/register` | Enroll token | Register or refresh an edge agent; returns `agent` including `id` |
| `POST` | `/api/agents/heartbeat` | Enroll token | Refresh last-seen (`{"agent_id":"…"}`) |
| `GET` | `/api/agents` | User JWT when `OPA_AUTH_REQUIRED` | List registered agents |
| `GET` | `/api/agents/{id}` | User JWT when `OPA_AUTH_REQUIRED` | Get one agent |

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

## Query / admin (ClickHouse-backed)

When `OPA_AUTH_REQUIRED=1`, these routes require `Authorization: Bearer <user JWT>`. Optional tenancy headers: `X-Organization-ID`, `X-Project-ID`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/query` | Capability advertisement (`services`, `traces`, `admin`, …) |
| `GET` | `/api/admin` | Operator summary (agents, ClickHouse write-hook stats) |
| `GET` | `/api/services` | Service list + `global_totals` from `opa.spans_min` (entry spans) |
| `GET` | `/api/services/metadata` | Language/framework metadata per service |
| `GET` | `/api/traces` | Paginated trace list (`limit`, `offset`, `service`, `status`, `from`, `to`, `sort`, `order`) |
| `GET` | `/api/traces/{id}` | Trace spans from `spans_min` |
| `GET` | `/api/traces/{id}/full` | Trace spans enriched from `spans_full` when available |

The hub **owns** these reads against the central ClickHouse `opa` database. Routine dashboard traffic does not call edge agents.

### Services response (shape)

```json
{
  "source": "opa-hub",
  "count": 1,
  "global_totals": {"total_traces": 10, "error_count": 0, "avg_duration": 12.5},
  "services": [{"service": "api", "total_traces": 10, "error_rate": 0, "avg_duration": 12.5}]
}
```

### Traces response (shape)

```json
{
  "source": "opa-hub",
  "traces": [{"trace_id": "…", "service": "api", "duration_ms": 42, "span_count": 3}],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

## Tenancy and GitHub linkage (for OPM)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/tenancy/organizations` | Organizations known to the hub (from agent registry; always includes `default-org`) |
| `GET` | `/api/github/status` | Declares that GitHub App/PAT credentials live in **ORA** (`credentials_home: ora`) |
| `GET` | `/api/peer/health` | Service JWT probe (`aud=opa-hub`, scope `health:read`) |

Hub owns identity (user JWTs) and a lightweight org directory. GitHub credentials stay in ORA connectors; OPM calls both peers.

Remaining dashboard surfaces (metrics series, service map, alerts, synthetics, RUM, profiling deep APIs) continue to seed on the hub as ownership moves off the edge agent.
