# Security

## Enroll token

Edge agents authenticate register, heartbeat, and push with `OPA_HUB_ENROLL_TOKEN`.

- Hub: set `OPA_HUB_ENROLL_TOKEN` to a long random secret.
- Agent: set the same value; send `X-OPA-Enroll-Token` (or Bearer) on hub calls.
- Empty token on the hub disables enroll checks — lab only.

## User auth

- Issue and validate user JWTs with `JWT_SECRET` (`POST /api/auth/login`, `GET /api/auth/status`).
- Tokens are standard HS256 JWTs (`Open-Auth-Go`); `iss=opa-hub`.
- Hub is the identity home for **co-deployed** Open-* installs: share `JWT_SECRET` with ORA/OSA/OPL so they validate hub-issued tokens.
- Solo OPA installs still use hub login (edge agents do not issue user JWTs).
- Service-to-service calls use short-lived JWTs minted with `OPEN_SERVICE_JWT_SECRET` (distinct from the user secret when possible).

## ClickHouse

Central telemetry lives in the `opa` database (`CLICKHOUSE_DB=opa`). Peer products use separate databases on the same server when co-deployed.

## Tenancy

- `GET /api/tenancy/organizations` and `GET /api/github/status` require a user JWT (viewer or higher) or a peer service JWT (`aud=opa-hub`, scope `health:read`) when `OPA_AUTH_REQUIRED=1`.
- Enforce `X-Organization-ID` / `X-Project-ID` on control-plane routes as query surfaces land.

## Secrets

Job containers must not inherit `JWT_SECRET`, service JWTs, enroll tokens, or connector secrets.
