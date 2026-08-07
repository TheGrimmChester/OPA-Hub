# Interop

Products are optional peers. Empty peer URLs disable cross-product features.

| Variable | Purpose |
|----------|---------|
| `PEER_OPA_URL` | OPA hub base URL (for ORA/OSA/OPL callers) |
| `PEER_ORA_URL` | ORA API base URL (GitHub App/PAT credential home for OPM and OSA) |
| `PEER_OSA_URL` | OSA API base URL |
| `PEER_OPL_URL` | OPL API base URL |
| `PEER_OPM_URL` | OPM API base URL |
| `OPEN_SERVICE_JWT_SECRET` | Service JWT mint/validate secret |

## GitHub credentials

The hub does **not** store GitHub App private keys or PATs. Co-deployed stacks set `PEER_ORA_URL` on the hub so `/api/github/status` can advertise the credential home. OPM and OSA discover organizations via hub tenancy, then list repos through ORA connectors.

## ClickHouse

| Variable | Purpose |
|----------|---------|
| `CLICKHOUSE_DB` | Database name (default `opa`) |

Hub validates user JWTs with the shared `JWT_SECRET`. In co-deployed family stacks with `PEER_OAM_URL`, **OAM** issues those tokens (`iss=oam-api`) and the hub proxies login; peers do not treat hub as a second identity SoT.

## Edge → hub

| Variable | Purpose |
|----------|---------|
| `OPA_HUB_URL` | Hub base URL configured on each `opa-agent` |
| `OPA_HUB_ENROLL_TOKEN` | Shared enroll secret (hub + edge) |

Dashboards call only this product's API (the hub). Peer calls are server-side.
