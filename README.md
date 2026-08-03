# OPA-Hub

Central control plane for **Open Profiling Agent** — agent registry, ingest aggregation, query/admin API, and auth for a single dashboard URL.

Edge `opa-agent` instances register and **push** telemetry outbound to this hub. `OPA-Dashboard` talks only to the hub.

## Documentation

See [docs/index.md](docs/index.md).

## Quick start

```bash
go run .
# GET http://127.0.0.1:8080/api/health
```

## License

EUPL-1.2 — see [LICENSE](LICENSE).
