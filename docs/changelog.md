# Changelog

## 0.5.0

- Hub owns additional RUM read surfaces: `/api/rum/detail`, `/api/rum/slo`, `/api/rum/facets`, `/api/rum/vitals/attribution`.
- Profile flame graph: `/api/profiles/flame` from `opa.profile_edges`.
- Error detail read: `/api/errors/{id}` (status/assign mutations remain on the edge agent).
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
