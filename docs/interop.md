# Interop

Products are optional peers. Empty peer URLs disable cross-product features.

| Variable | Purpose |
|----------|---------|
| `PEER_OPA_URL` | OPA hub base URL (for ORA/OSA/OPL callers) |
| `PEER_ORA_URL` | ORA API base URL |
| `PEER_OSA_URL` | OSA API base URL |
| `PEER_OPL_URL` | OPL API base URL |
| `OPEN_SERVICE_JWT_SECRET` | Service JWT mint/validate secret |

## Edge → hub

| Variable | Purpose |
|----------|---------|
| `OPA_HUB_URL` | Hub base URL configured on each `opa-agent` |
| `OPA_HUB_ENROLL_TOKEN` | Shared enroll secret (hub + edge) |

Dashboards call only this product's API (the hub). Peer calls are server-side.
