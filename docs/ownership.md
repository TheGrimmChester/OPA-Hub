# Ownership: hub vs edge writers and workers

Open Profiling Agent uses hub-and-spoke topology with a **shared central ClickHouse** `opa` database. The dashboard talks only to **opa-hub**. Edge **opa-agent** processes own ingest, evaluation, and probe workers that read and write the same tables.

## Split of responsibility

| Concern | Owner | ClickHouse tables (when applicable) |
|---------|-------|-------------------------------------|
| Dashboard query / list / CRUD for UI | **Hub** | reads (and config writes) against central `opa` |
| Local telemetry ingest (socket, OTLP, ND-JSON) | **Edge agent** | forward to hub ingest; may also write when `CLICKHOUSE_URL` points at central CH |
| Alert rule CRUD / history list | **Hub** | `opa.alerts`, `opa.alert_history` |
| Alert evaluation + notification delivery | **Edge agent** | reads `opa.alerts` + `opa.alert_test_requests`; writes `opa.alert_history` on fire / manual Test |
| SLO list / CRUD / compliance reads | **Hub** | `opa.slos`, `opa.slo_metrics` |
| SLO evaluation | **Edge agent** | reads `opa.slos`; writes `opa.slo_metrics` |
| Error inbox list / detail / status / assign | **Hub** | `opa.error_instances`, `opa.error_group_status` |
| Anomalies list | **Hub** | `opa.anomalies` |
| Anomaly detector / on-demand analyze | **Edge agent** | writes `opa.anomalies` |
| Logs explorer | **Hub** | `opa.logs` (optional join to `opa.spans_min` for tenancy; always joined when auth enforced) |
| Trace explorer list / service metadata / facets | **Hub** | `opa.spans_min` / related (`GET /api/traces`, `GET /api/services/metadata`, `GET /api/explore/facets`) |
| SQL / Redis / HTTP / dumps / commands / stats / key-transactions | **Hub** | `opa.spans_min`, `opa.spans_full`, `opa.key_transactions`, `system.parts` |
| Metrics host inventory (`GET /api/infra/hosts`) | **Hub** | `opa.metric_series` |
| Cohort transaction compare (`GET /api/transactions/compare`) | **Hub** | `opa.spans_min` (entry spans) |
| Platform version / topology / ops status | **Hub** | in-memory hub registry + runtime; no edge fan-out |
| Audit log list (`GET /api/audit`) | **Hub** | `opa.audit_log` |
| DB monitor reads (`GET /api/db/*` list surfaces) | **Hub** | `opa.db_instance_snapshots`, `opa.db_statement_stats`, `opa.db_fingerprint_map`, `opa.db_unused_indexes` |
| DB monitor scrape + fingerprint join worker | **Edge agent** | writes DB monitor tables when `OPA_DB_MONITOR_CONFIG` is set |
| Synthetics list / CRUD / results read | **Hub** | `opa.synthetic_checks`, `opa.synthetic_results` |
| Synthetics HTTP probes | **Edge agent** | reads `opa.synthetic_checks`; writes `opa.synthetic_results` |
| RUM browser ingest (`POST /api/rum`, `POST /api/rum/replay`) | **Edge agent** | `opa.rum_events`, `opa.rum_replay_chunks` |
| RUM metrics / sessions / detail / replay reads | **Hub** | same RUM tables |
| Mobile crash ingest (`POST /api/mobile/crashes`) | **Edge agent** | `opa.mobile_crashes` |
| Mobile crash / session reads | **Hub** | `opa.mobile_crashes` |
| Diagnostics reads + release markers | **Hub** | `opa.release_markers`, `opa.heap_snapshots`, `opa.thread_samples`, `opa.lock_contention` (+ `opa.spans_min` for suspect scoring) |
| Heap / thread / lock ingest (`POST /v1/heap`, `/v1/threads`, `/v1/locks`) | **Edge agent** | same diagnostics tables |

## Coherence rules

1. **Same tables.** Hub CRUD and edge workers must use identical table and column names. Alert rules live in `opa.alerts` (ReplacingMergeTree on `updated_at`). SLO definitions live in `opa.slos`; compliance windows in `opa.slo_metrics`. Synthetic checks live in `opa.synthetic_checks`; probe outcomes in `opa.synthetic_results`. Error group status lives in `opa.error_group_status`. DB monitor snapshots and digests use the Wave 17 table set above.
2. **Edge reloads config from ClickHouse.** The alert evaluator reloads `opa.alerts` on each check tick so rules created or edited via the hub apply without restarting the agent. The SLO evaluator lists `opa.slos` each tick. The synthetics scheduler already reloads checks from `opa.synthetic_checks` on each probe tick.
3. **Dashboard never calls edge hosts.** UI traffic uses the hub base URL only. Workers on the edge do not expose dashboard APIs in production topology.
4. **Shared `CLICKHOUSE_URL`.** On NAS / co-deployed stacks, hub and agent both point at the same ClickHouse service so hub-persisted rows are visible to edge workers.

## Still on the edge (not hub-owned yet)

These surfaces remain edge-owned workers or deep APIs; move them only with an explicit ownership change:

- RUM ingest (`POST /api/rum`, `POST /api/rum/replay`) and mobile crash ingest (`POST /api/mobile/crashes`)
- Synthetic probe workers
- Alert notification channel delivery (webhook / Slack / email)
- SLO evaluator ticker (writes `opa.slo_metrics`)
- Anomaly detector scheduler and `POST /api/anomalies/analyze`
- Filter suggestions (`GET /api/filter-suggestions/*`) — **edge agent only** (keys are static; values query ClickHouse). Dashboard does not call these paths today; do not add hub stubs.
- DB monitor scrape targets admin (`GET/POST /api/db/targets`) — edge-only config surface; dashboard does not call it
- Heap / thread / lock sample ingest (`POST /v1/heap`, `/v1/threads`, `/v1/locks` and `/api/diagnostics/*/ingest`) — dashboard reads are hub-owned

## Unimplemented dashboard scaffolds (batch 4 audit)

These pages call hub URLs today but **no backend exists** on hub or edge (NAS `192.168.100.101`: hub `:18080` and edge `:18081` both return **404**). Do **not** add fake hub routes — implement the agent (or hub) handler and ClickHouse tables first, then move reads to the hub in a follow-on batch.

| Surface | Dashboard page | Example routes | Agent backend? | Hub backend? | Decision |
|---------|----------------|----------------|----------------|--------------|----------|
| Network observability | `Network.jsx` | `GET /api/network/{summary,flows,dependencies,dns,tls,discovered,host-profiles}`, `POST /api/network/probe-dns` | No | No | **Deferred** — UI scaffold only |
| Cloud inventory / cost | `Cloud.jsx` | `GET /api/cloud/{summary,resources,cost,tags,scrapes}`, `POST /api/cloud/scrape-now`, `POST /api/cloud/cost/ingest` | No | No | **Deferred** — UI scaffold only |
| Service catalog | `Catalog.jsx` | `GET /api/catalog`, `/scorecards`, `/teams`, `/groups`, `/entities/{id}`, `POST /api/catalog/discover`, `/apply`, `/teams/upsert` | No | No | **Deferred** — UI scaffold only |
| Declarative mgmt (GitOps) | `Automation.jsx` | `GET /api/mgmt/v1`, `/revisions`, `/export`, `/openapi.json`, `POST /api/mgmt/v1/{plan,apply,import,promote}` | No | No | **Deferred** — UI scaffold only |
| Call-graph window compare | `CompareTraces.jsx` → `CallgraphWindowCompare.jsx` | `GET /api/callgraph/compare` | No | No | **Deferred** — UI scaffold only (`opa.callgraph_agg` table exists; no compare handler yet) |

**Edge-only, dashboard not wired:** `GET /api/filter-suggestions/{keys,values}` — agent returns **200** on NAS edge `:18081`; hub correctly returns **404**. Stay on edge until a dashboard surface calls it; then proxy or re-implement on hub against the same ClickHouse queries.

**Trace Explorer facets (hub-owned, not deferred):** `GET /api/explore/facets` is on **opa-hub** as of [OPA-Hub #20](https://github.com/TheGrimmChester/OPA-Hub/pull/20) / **v0.7.3+** (Wave 14 agent query restored against `opa.spans_min` / signal tables; **v0.7.4** adds language/framework/db_system/url_path). NAS hub `:18080` returns **200** (JWT); edge `:18081` remains **404** by design. Do not list this route as deferred.

### NAS facet dimension audit (`opa.spans_min`, 7d window)

Audited on `192.168.100.101` ClickHouse. Gate: only ship chips for columns with real non-empty values.

| Column / facet field | Non-empty rows | Distinct | Ship as chip? |
|----------------------|----------------|----------|---------------|
| `service` | ~all | 5 | Yes (existing) |
| `status` | ~all | 2 | Yes (existing) |
| `language` | ~all | 2 (`php`, `python`) | **Yes — enriched** |
| `framework` | ~all | 1 (`symfony`) | **Yes — enriched** |
| `db_system` | sparse (~172) | 1 (`mysql`) | **Yes — enriched** |
| `url_path` | ~1k | 20 | **Yes — enriched** |
| `hostname` / `host` | 0 | 0 | Allowlisted; empty on NAS today |
| `environment` | 0 | 0 | Allowlisted; empty on NAS today |
| `release` | 0 | 0 | Allowlisted; empty on NAS today |
| `route` | 0 | 0 | Allowlisted; empty on NAS today |
| `container_id` / `pod` / `instance` | 0 | 0 | Not allowlisted (no data) |

Dashboard Trace Explorer should prefer `service` / `language` / `framework` / `status` (and optionally `db_system` / `url_path`) over empty `environment` / `host` until edge ingest fills those identity columns.

## Hub migration history (batches 2–5)

| Route / surface | Dashboard calls? | Agent backend? | Decision |
|-----------------|------------------|----------------|----------|
| `GET /api/infra/hosts` | Yes (`Infrastructure.jsx`) | Yes | **Hub-owned** (batch 2) |
| `GET /api/transactions/compare` | Yes (`CohortCompare.jsx`) | Yes | **Hub-owned** (batch 2) |
| `GET /api/version` | Yes (`PlatformOps.jsx`) | Yes (wave15) | **Hub-owned** (batch 3) |
| `GET /api/topology` | Yes (`PlatformOps.jsx`) | Yes (wave15) | **Hub-owned** — hub-spoke contract (batch 3) |
| `GET /api/ops/status` | Yes (`PlatformOps.jsx`) | Yes (wave15) | **Hub-owned** (batch 3) |
| `GET /api/audit` | Yes (`PlatformOps.jsx`) | Yes (wave15) | **Hub-owned** — admin role (batch 3) |
| `GET /api/db/instances` | Yes (`Databases.jsx`) | Yes (wave17) | **Hub-owned** (batch 3) |
| `GET /api/db/statements` | Yes (`Databases.jsx`) | Yes (wave17) | **Hub-owned** (batch 3) |
| `GET /api/db/fingerprint-match` | Yes (`Databases.jsx`) | Yes (wave17) | **Hub-owned** (batch 3) |
| `GET /api/db/unused-indexes` | Yes (`Databases.jsx`) | Yes (wave17) | **Hub-owned** (batch 3) |
| `GET /api/filter-suggestions/{keys,values}` | No | Yes (edge `:18081`) | **Stay on edge** (batch 4) — no hub route |
| `GET /api/db/statements/{fp}` | No | Yes (wave17) | Skip — dashboard navigates via `/sql/{fp}` instead |
| `GET /api/network/*`, `GET /api/cloud/*`, `GET /api/catalog*`, `GET /api/mgmt/v1*` | Yes | No | **Deferred** (batch 4) — no backend yet |
| `GET /api/callgraph/compare` | Yes (`CallgraphWindowCompare.jsx`) | No | **Deferred** (batch 4) — implement compare handler first |
| `GET /api/traces`, `GET /api/services/metadata` | Yes (`TraceExplorer.jsx`) | Yes (legacy) | **Already hub-owned** (batch 5) — NAS hub **200** |
| `GET /api/explore/facets` | Yes (`FacetSidebar.jsx`) | No (removed; was Wave 14) | **Hub-owned** ([#20](https://github.com/TheGrimmChester/OPA-Hub/pull/20), v0.7.3) — NAS hub **200** |
| `GET/POST /api/releases`, `GET /api/diagnostics/{suspect-commits,heap,threads,locks}` | Yes (`Diagnostics.jsx`) | Yes (agent wave27 branch; not on NAS edge main) | **Hub-owned** (batch 6) — hub ensures CH tables; edge keeps `/v1/{heap,threads,locks}` ingest |

## Related docs

- [Architecture](architecture.md)
- [API](api.md)
- Edge counterpart: OPA-Agent `docs/ownership.md`
