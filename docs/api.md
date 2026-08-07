# API

Health `service` id: `opa-hub`.

## Health

`GET /api/health`

```json
{"status":"ok","service":"opa-hub","version":"0.7.0","agents":0,"clickhouse":true,"topology":"hub-spoke","auth_mode":"codeployed"}
```

`auth_mode` mirrors `AUTH_MODE` (`standalone` | `codeployed`; defaults to `standalone` when unset).

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
| `GET` | `/api/query` | Capability advertisement (`services`, `traces`, `metrics_*`, `service_map`, `alerts`, `rum_*`, …) |
| `GET` | `/api/admin` | Operator summary (agents, ClickHouse write-hook stats) |
| `GET` | `/api/services` | Service list + `global_totals` from `opa.spans_min` (entry spans) |
| `GET` | `/api/services/metadata` | Language/framework metadata per service |
| `GET` | `/api/traces` | Paginated trace list (`limit`, `offset`, `service`, `status`, `from`, `to`, `sort`, `order`) |
| `GET` | `/api/traces/{id}` | Trace spans from `spans_min` |
| `GET` | `/api/traces/{id}/full` | Trace spans enriched from `spans_full` when available |
| `GET` | `/api/explore/facets` | Facet value counts for Trace Explorer (`signal`, `field`, `hours`). Spans fields: `service`, `status`, `host`, `environment`, `release`, `route`, `language`, `framework`, `db_system`, `url_path`, `trace_id` |
| `GET` | `/api/metrics/names` | Metric discovery from `opa.metric_series` |
| `GET` | `/api/metrics/labels` | Label names for a metric (`metric=`) |
| `GET` | `/api/metrics/label-values` | Label values (`metric=`, `label=`) |
| `GET` | `/api/metrics/query-range` | Tier-routed time series (`metric=`, `agg=`, `from`/`to`/`range`, `label=`) |
| `GET` | `/api/metrics/performance` | Entry-span latency/error/throughput buckets |
| `GET` | `/api/metrics/network` | Entry-span bytes/request buckets |
| `GET` | `/api/service-map` | Service graph nodes + edges from parent/child spans |
| `GET`/`POST` | `/api/service-map/thresholds` | Health threshold config (`opa.service_map_thresholds`) |
| `GET` | `/api/service-map/edge-traces` | Traces for a service→service edge |
| `GET`/`POST` | `/api/alerts` | List/create alert rules (`opa.alerts`) |
| `GET`/`PUT`/`DELETE`/`POST` | `/api/alerts/{id}` | Get/update/delete/test a rule. `POST` queues edge delivery via `opa.alert_test_requests` and waits briefly for `opa.alert_history` |
| `GET` | `/api/alerts/{id}/history` | Recent firings (`opa.alert_history`) |
| `GET` | `/api/rum/metrics` | RUM aggregates + Core Web Vitals |
| `GET` | `/api/rum/sessions` | Browser session list |
| `GET` | `/api/rum/sessions/{id}` | Session timeline (page views / ajax / errors) |
| `GET` | `/api/rum/detail` | Resource / AJAX / page-view aggregates |
| `GET` | `/api/rum/slo` | CWV p75 budgets + rating histograms |
| `GET` | `/api/rum/facets` | Route / geo facet chips |
| `GET` | `/api/rum/vitals/attribution` | CWV attribution by route |
| `GET` | `/api/rum/replay/{id}` | Masked session-replay chunks (`opa.rum_replay_chunks`) |
| `GET` | `/api/rum/replay-timeline/{id}` | Flattened scrubber events for SessionReplayPlayer |
| `GET` | `/api/rum/mobile/sessions` | Distinct mobile crash sessions |
| `GET` | `/api/mobile/crashes` | Mobile crash list (optional `session_id` / `platform`) |
| `GET` | `/api/profiles` | Aggregated profiling top functions |
| `GET` | `/api/errors` | Errors inbox group list |
| `GET` | `/api/errors/{id}` | Error group detail |
| `POST` | `/api/errors/groups/{id}/status` | Resolve / ignore / reopen (`opa.error_group_status`) |
| `POST` | `/api/errors/groups/{id}/assign` | Assign group |
| `GET`/`POST` | `/api/slos` | List/create SLOs (`opa.slos`) |
| `GET`/`PUT`/`DELETE` | `/api/slos/{id}` | Get/update/delete an SLO |
| `GET` | `/api/slos/{id}/compliance` | Recent compliance windows (`opa.slo_metrics`) |
| `GET` | `/api/anomalies` | Anomaly detections list (`opa.anomalies`) |
| `GET` | `/api/logs` | Logs explorer (facets, histogram, pagination) |
| `GET` | `/api/sql/queries` | SQL fingerprint aggregates (`opa.spans_min`) |
| `GET` | `/api/sql/queries/{fingerprint}` | SQL fingerprint detail + trends |
| `GET` | `/api/redis/operations` | Redis command/key aggregates (`opa.spans_full.redis`) |
| `GET` | `/api/http-calls` | Outbound HTTP aggregates (`opa.spans_full.http`) |
| `GET` | `/api/services/{name}/stats` | Service KPIs + top endpoints |
| `GET` | `/api/services/{name}/http-calls` | Per-service outbound HTTP aggregates |
| `GET` | `/api/dumps` | Flattened variable dumps from `opa.spans_full` |
| `GET` | `/api/stats` | Trace / storage / registry summary for Statistics page |
| `GET` | `/api/commands` | CLI/worker/cron entry spans (`is_cli=1`) |
| `GET`/`POST` | `/api/key-transactions` | Named key-transaction list/create |
| `GET` | `/api/infra/hosts` | Host inventory from `opa.metric_series` (reporting state) |
| `GET` | `/api/transactions/compare` | Cohort compare of entry-span aggregates by dimension |
| `GET`/`POST` | `/api/synthetics` | List/create synthetic checks (`opa.synthetic_checks`) |
| `GET`/`PUT`/`DELETE` | `/api/synthetics/{id}` | Get/update/delete a check |
| `GET` | `/api/synthetics/{id}/results` | Recent probe results (`opa.synthetic_results`) |
| `GET` | `/api/synthetics/locations` | Probe location placeholders |
| `GET`/`POST` | `/api/releases` | List/create release markers (`opa.release_markers`) for suspect-commit attribution |
| `GET` | `/api/diagnostics/suspect-commits` | Ranked release suspects near error-rate shifts (`?service=&hours=`) |
| `GET` | `/api/diagnostics/heap` | Heap dominator snapshots (`opa.heap_snapshots`; optional `?id=` detail) |
| `GET` | `/api/diagnostics/threads` | Aggregated thread samples (`opa.thread_samples`) |
| `GET` | `/api/diagnostics/locks` | Aggregated lock contention (`opa.lock_contention`) |

The hub **owns** these reads (and config CRUD) against the central ClickHouse `opa` database. Routine dashboard traffic does not call edge agents for the paths above.

See [ownership.md](ownership.md) for the hub vs edge writer/worker split.

Alert **evaluation** (periodic condition checks and notification delivery) still runs on the edge agent, which reloads rules from `opa.alerts` each check tick. Hub owns the dashboard CRUD/list/history surface. Dashboard **Test** (`POST /api/alerts/{id}`) inserts into `opa.alert_test_requests`; the edge leader force-fires delivery (no condition/cooldown) and writes `opa.alert_history` within a few seconds.

SLO **evaluation** still runs on the edge agent and writes `opa.slo_metrics`; hub owns SLO CRUD and compliance reads from the same tables.

RUM **ingest** (`POST /api/rum`, `POST /api/rum/replay`) and mobile crash **ingest** (`POST /api/mobile/crashes`) remain on the edge agent. Synthetic **probe scheduling** also remains on the edge; check definitions and result reads are hub-owned, and the agent writes probe outcomes into the same `opa.synthetic_results` table.

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

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/tenancy/organizations` | User JWT (viewer+) or service JWT (`health:read`) when `OPA_AUTH_REQUIRED` | Organizations known to the hub (from OAM when `PEER_OAM_URL` is set; else agent registry — includes `default-org` only when agents enrolled under it) |
| `GET` | `/api/oam/projects` | User JWT (viewer+) or service JWT (`orgs:read`) when `OPA_AUTH_REQUIRED` | Proxy OAM directory projects (`?organization_id=`, `?product=opa`); adds `project_id` alongside `id`. Enablement writes stay on OAM. |
| `GET` | `/api/github/status` | User JWT (viewer+) or service JWT (`health:read`) when `OPA_AUTH_REQUIRED` | Declares that GitHub App/PAT credentials live in **ORA** (`credentials_home: ora`) or **OAM** when `PEER_OAM_URL` is set |
| `GET` | `/api/peer/health` | Service JWT (`aud=opa-hub`, scope `health:read`) | Peer probe for co-deployed products |

Hub owns identity (user JWTs) and a lightweight org directory. GitHub credentials stay in ORA connectors; OPM calls both peers.

Remaining edge-owned workers include RUM/mobile crash ingest, synthetic probe workers, alert evaluation/delivery, SLO evaluation, anomaly detection/analyze, and heap/thread/lock sample ingest (`POST /v1/heap`, `/v1/threads`, `/v1/locks`).
