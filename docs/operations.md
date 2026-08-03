# Operations

## Health

Probe `GET /api/health` on `opa-hub`.

## Logs

Structured JSON logs on stdout. Collect via the container runtime.

## Upgrades

Roll the `opa-hub:nas` image. Keep ClickHouse migrations versioned and applied before cutting traffic.
