# Install

## Local smoke (laptop)

Build and run the `opa-hub:smoke` image on a developer machine only.

```bash
# From ~/Documents/repos (needs sibling Open-* module checkouts):
docker build -f OPA-Hub/Dockerfile -t opa-hub:smoke .
docker run --rm -p 8080:8080 \
  -e OPA_HUB_ENROLL_TOKEN=dev-enroll \
  -e CLICKHOUSE_URL=http://host.docker.internal:8123 \
  opa-hub:smoke
```

From source:

```bash
export OPA_HUB_ENROLL_TOKEN=dev-enroll
go run .
# GET http://127.0.0.1:8080/api/health
```

## Production / NAS

Use `opa-hub:nas` image tags only. Never deploy `*:smoke` to production hosts.

Pair each edge `opa-agent` with:

```bash
OPA_HUB_URL=https://opa-hub.example:8080
OPA_HUB_ENROLL_TOKEN=<same-as-hub>
```

Point `OPA-Dashboard` at the hub base URL only.
