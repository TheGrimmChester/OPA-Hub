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
| Logs explorer | **Hub** | `opa.logs` (optional join to `opa.spans_min` for tenancy) |
| SQL / Redis / HTTP / dumps / commands / stats / key-transactions | **Hub** | `opa.spans_min`, `opa.spans_full`, `opa.key_transactions`, `system.parts` |
| Synthetics list / CRUD / results read | **Hub** | `opa.synthetic_checks`, `opa.synthetic_results` |
| Synthetics HTTP probes | **Edge agent** | reads `opa.synthetic_checks`; writes `opa.synthetic_results` |
| RUM browser ingest (`POST /api/rum`, `POST /api/rum/replay`) | **Edge agent** | `opa.rum_events`, `opa.rum_replay_chunks` |
| RUM metrics / sessions / detail / replay reads | **Hub** | same RUM tables |
| Mobile crash ingest (`POST /api/mobile/crashes`) | **Edge agent** | `opa.mobile_crashes` |
| Mobile crash / session reads | **Hub** | `opa.mobile_crashes` |

## Coherence rules

1. **Same tables.** Hub CRUD and edge workers must use identical table and column names. Alert rules live in `opa.alerts` (ReplacingMergeTree on `updated_at`). SLO definitions live in `opa.slos`; compliance windows in `opa.slo_metrics`. Synthetic checks live in `opa.synthetic_checks`; probe outcomes in `opa.synthetic_results`. Error group status lives in `opa.error_group_status`.
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

## Related docs

- [Architecture](architecture.md)
- [API](api.md)
- Edge counterpart: OPA-Agent `docs/ownership.md`
