# Ownership: hub vs edge writers and workers

Open Profiling Agent uses hub-and-spoke topology with a **shared central ClickHouse** `opa` database. The dashboard talks only to **opa-hub**. Edge **opa-agent** processes own ingest, evaluation, and probe workers that read and write the same tables.

## Split of responsibility

| Concern | Owner | ClickHouse tables (when applicable) |
|---------|-------|-------------------------------------|
| Dashboard query / list / CRUD for UI | **Hub** | reads (and config writes) against central `opa` |
| Local telemetry ingest (socket, OTLP, ND-JSON) | **Edge agent** | forward to hub ingest; may also write when `CLICKHOUSE_URL` points at central CH |
| Alert rule CRUD / history list | **Hub** | `opa.alerts`, `opa.alert_history` |
| Alert evaluation + notification delivery | **Edge agent** | reads `opa.alerts`; writes `opa.alert_history` on fire |
| Synthetics list / CRUD / results read | **Hub** | `opa.synthetic_checks`, `opa.synthetic_results` |
| Synthetics HTTP probes | **Edge agent** | reads `opa.synthetic_checks`; writes `opa.synthetic_results` |
| RUM browser ingest (`POST /api/rum`) | **Edge agent** | `opa.rum_events` (and related) |
| RUM metrics / sessions query | **Hub** | same RUM tables |

## Coherence rules

1. **Same tables.** Hub CRUD and edge workers must use identical table and column names. Alert rules live in `opa.alerts` (ReplacingMergeTree on `updated_at`). Synthetic checks live in `opa.synthetic_checks`; probe outcomes in `opa.synthetic_results`.
2. **Edge reloads config from ClickHouse.** The alert evaluator reloads `opa.alerts` on each check tick so rules created or edited via the hub apply without restarting the agent. The synthetics scheduler already reloads checks from `opa.synthetic_checks` on each probe tick.
3. **Dashboard never calls edge hosts.** UI traffic uses the hub base URL only. Workers on the edge do not expose dashboard APIs in production topology.
4. **Shared `CLICKHOUSE_URL`.** On NAS / co-deployed stacks, hub and agent both point at the same ClickHouse service so hub-persisted rows are visible to edge workers.

## Still on the edge (not hub-owned yet)

These surfaces remain edge-owned workers or deep APIs; move them only with an explicit ownership change:

- RUM ingest and session-replay payloads
- Error group status mutations
- SLO evaluator and anomaly detector schedulers
- Alert notification channel delivery (webhook / Slack / email)

## Related docs

- [Architecture](architecture.md)
- [API](api.md)
- Edge counterpart: OPA-Agent `docs/ownership.md`
