# Changelog

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
