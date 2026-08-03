# Security

## Enroll token

Edge agents authenticate register, heartbeat, and push with `OPA_HUB_ENROLL_TOKEN`.

- Hub: set `OPA_HUB_ENROLL_TOKEN` to a long random secret.
- Agent: set the same value; send `X-OPA-Enroll-Token` (or Bearer) on hub calls.
- Empty token on the hub disables enroll checks — lab only.

## User auth

- Validate / issue user JWTs with `JWT_SECRET` when auth is required (`OPA_AUTH_REQUIRED`).
- Hub is the identity home for co-deployed Open Profiling Agent installs (`/api/auth/*`).
- Service-to-service calls use short-lived JWTs minted with `OPEN_SERVICE_JWT_SECRET` (distinct from the user secret when possible).

## Tenancy

Enforce `X-Organization-ID` / `X-Project-ID` on control-plane routes as query surfaces land.

## Secrets

Job containers must not inherit `JWT_SECRET`, service JWTs, enroll tokens, or connector secrets.
