# Architecture

Open Profiling Agent uses a hub-and-spoke topology:

1. Language SDKs and collectors send telemetry to a local **edge** `opa-agent`.
2. Each edge agent **registers** and **pushes** telemetry outbound to **opa-hub**.
3. **opa-dashboard** uses one base URL and queries only the hub — never edge hosts.

```mermaid
flowchart TB
  subgraph edgeHost [Each_profiled_host]
    SDKs[SDKs_RUM_Collector]
    EdgeAgent[opa-agent]
    SDKs -->|local ingest| EdgeAgent
  end
  subgraph central [Central_OPA]
    Hub[opa-hub]
    CH[(ClickHouse)]
    UI[opa-dashboard]
    Hub --> CH
    UI -->|single API URL| Hub
  end
  EdgeAgent -->|register + push outbound| Hub
```


## Containers

| Image | Role |
|-------|------|
| `opa-hub` | API / control plane |
