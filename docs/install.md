# Install

## Local smoke (laptop)

Build and run the `opa-hub:smoke` image on a developer machine only.

```bash
docker build -t opa-hub:smoke .
docker run --rm -p 8080:8080 opa-hub:smoke
```

## Production / NAS

Use `opa-hub:nas` image tags only. Never deploy `*:smoke` to production hosts.
