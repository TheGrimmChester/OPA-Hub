# Security

## Enroll token

Edge agents authenticate register, heartbeat, and push with `OPA_HUB_ENROLL_TOKEN`.

- Hub: set `OPA_HUB_ENROLL_TOKEN` to a long random secret.
- Agent: set the same value; send `X-OPA-Enroll-Token` (or Bearer) on hub calls.
- Empty token on the hub disables enroll checks — lab only. With `OPA_AUTH_REQUIRED=1`, always set an enroll token in production.

## User auth

- Issue and validate user JWTs with `JWT_SECRET` (`POST /api/auth/login`, `GET /api/auth/status`).
- Tokens are standard HS256 JWTs (`Open-Auth-Go`); `iss=opa-hub`.
- Optional claims `org_id` / `project_id` bind a token to a tenant (`MintUserJWTWithTenant`). When set, hub middleware rejects mismatched `X-Organization-ID` / `X-Project-ID` (403) and overwrites headers from the claims.
- Hub is the identity home for **co-deployed** Open-* installs: share `JWT_SECRET` with ORA/OSA/OPL so they validate hub-issued tokens.
- Solo OPA installs still use hub login (edge agents do not issue user JWTs).
- Service-to-service calls use short-lived JWTs minted with `OPEN_SERVICE_JWT_SECRET` (distinct from the user secret when possible). Service JWTs may carry `org_id`; hub applies it as `X-Organization-ID`.
- Role gate: dashboard reads need `viewer+`; mutations (POST/PUT/PATCH/DELETE) need `editor+`. `GET /api/audit` needs `admin`.
- `POST /api/auth/register`: self-reg caps at `viewer`. When `OPA_AUTH_REQUIRED=1`, registration requires an admin JWT; only admins may mint editor/admin roles.

## ClickHouse

Central telemetry lives in the `opa` database (`CLICKHOUSE_DB=opa`). Peer products use separate databases on the same server when co-deployed.

## Tenancy

- At boot, hub calls `opentenant.SetAuthEnforced(OPA_AUTH_REQUIRED)` so list/query SQL never widens to `1=1` for missing or `"all"` headers when auth is on (collapse to `default-org` / `default-project`).
- Observability reads (`/api/services`, `/api/traces`, `/api/logs`, metrics, alerts, RUM, …) apply `ScopeBool` / `ScopeAnd` from `X-Organization-ID` / `X-Project-ID`.
- Agent registry list/get and `GET /api/tenancy/organizations` filter to the request scope when auth-enforced or headers are set.
- Alert / SLO / synthetics DELETE uses `OwnedRowPredicate` so cross-tenant deletes cannot target another org’s row by id alone.
- Unauthenticated → **401** when `OPA_AUTH_REQUIRED=1`. Wrong org with unbound JWT → that org’s data only (header-driven). JWT with bound `org_id` + wrong header → **403**.
- `GET /api/audit` remains admin-global (no org columns on `opa.audit_log` today).

## Secrets

Job containers must not inherit `JWT_SECRET`, service JWTs, enroll tokens, or connector secrets.
