# Changelog

## 0.7.6

- Enforce tenant scopes when `OPA_AUTH_REQUIRED=1`: wire `opentenant.SetAuthEnforced`, always scope logs under auth, filter agents / tenancy orgs, tenant-bound alert/SLO/synthetics DELETE, editor role for mutations, lock down `/api/auth/register`, bind JWT `org_id`/`project_id` headers.

## 0.7.4

- Explore facets: allowlist `language`, `framework`, `db_system`, and `url_path` on spans (NAS `opa.spans_min` has non-empty values; `environment` / `host` / `release` remain allowlisted but empty until ingest fills them).

## 0.7.3

- Trace Explorer facets: `GET /api/explore/facets` (Wave 14 agent query restored on hub against `opa.spans_min` / signal tables).

## 0.7.1

- Alert Test (`POST /api/alerts/{id}`) queues `opa.alert_test_requests` for edge force-delivery and waits briefly for `opa.alert_history` (replaces the previous accepted stub).

## 0.7.0

- Hub owns RUM session-replay reads: `GET /api/rum/replay/{id}`, `GET /api/rum/replay-timeline/{id}` from `opa.rum_replay_chunks`.
- Mobile crash↔session reads: `GET /api/rum/mobile/sessions` and `GET /api/mobile/crashes` from `opa.mobile_crashes` when present.
- Edge agent retains `POST /api/rum`, `POST /api/rum/replay`, and `POST /api/mobile/crashes` ingest.

## 0.6.0

- Hub owns SLO list/CRUD and compliance reads: `/api/slos`, `/api/slos/{id}`, `/api/slos/{id}/compliance` (`opa.slos`, `opa.slo_metrics`). Edge agent continues periodic SLO evaluation into the same metrics table.
- Error inbox mutations on hub: `POST /api/errors/groups/{id}/status` and `/assign` write `opa.error_group_status`.
- Anomalies list: `GET /api/anomalies` from `opa.anomalies` (detector and `/analyze` remain on the edge agent).
- Logs explorer: `GET /api/logs` for the dashboard main-nav surface.

## 0.5.0

- Hub owns additional RUM read surfaces: `/api/rum/detail`, `/api/rum/slo`, `/api/rum/facets`, `/api/rum/vitals/attribution`.
- Profile flame graph: `/api/profiles/flame` from `opa.profile_edges`.
- Error detail read: `/api/errors/{id}`.
- Trace log correlation: `/api/traces/{id}/logs` from `opa.logs`.
- Service map includes external dependency edges (database, HTTP, Redis, cache) from `opa.spans_full`.

## 0.4.0

- Hub owns additional dashboard query surfaces against central ClickHouse `opa`:
  metrics explorer (`/api/metrics/names|labels|label-values|query-range`), performance/network series,
  service map (`/api/service-map`, thresholds, edge-traces), alerts list/CRUD/history,
  RUM metrics and sessions, profiling summaries, errors inbox list, synthetics list.
- Auth login/register uses Open-Auth-Go `LocalIssuer`.
- Store wrapper exposes `Exec` for INSERT/ALTER via Open-ClickHouse-Go.

## 0.3.0

- Query ownership on hub: `/api/services`, `/api/services/metadata`, `/api/traces`, `/api/traces/{id}`, `/api/traces/{id}/full` read central ClickHouse `opa` (entry-span scoped service totals).
- Expanded `/api/query` capability advertisement; `/api/admin` marks `query_owner`.
- User JWT middleware on dashboard query/admin routes when `OPA_AUTH_REQUIRED` is set.
- Open-ClickHouse-Go `Query` used for SELECT paths.

## 0.2.0

- Agent registry (`/api/agents/register`, heartbeat, list/get).
- Edge ingest push (`/api/ingest/push`) with ClickHouse write hooks via Open-ClickHouse-Go.
- Auth issuer endpoints (`/api/auth/*`) for dashboard login.
- Query/admin skeleton (`/api/query`, `/api/admin`, `/api/services`).
- Document hub-and-spoke enroll token and `OPA_HUB_URL` pairing.

## 0.1.0

- Initial repository skeleton.
