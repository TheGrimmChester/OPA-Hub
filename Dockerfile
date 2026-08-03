# Build from the family repos root so Open-* module replaces resolve:
#   docker build -f OPA-Hub/Dockerfile -t opa-hub:smoke .
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY Open-Auth-Go /modules/Open-Auth-Go
COPY Open-ClickHouse-Go /modules/Open-ClickHouse-Go
COPY Open-HTTP-Go /modules/Open-HTTP-Go
COPY Open-Logger-Go /modules/Open-Logger-Go
COPY OPA-Hub/ /src/OPA-Hub/
WORKDIR /src/OPA-Hub
RUN sed -i \
  -e 's|=> ../Open-Auth-Go|=> /modules/Open-Auth-Go|' \
  -e 's|=> ../Open-ClickHouse-Go|=> /modules/Open-ClickHouse-Go|' \
  -e 's|=> ../Open-HTTP-Go|=> /modules/Open-HTTP-Go|' \
  -e 's|=> ../Open-Logger-Go|=> /modules/Open-Logger-Go|' \
  go.mod \
  && CGO_ENABLED=0 go build -o /out/opa-hub .

FROM alpine:3.20
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/opa-hub /usr/local/bin/opa-hub
EXPOSE 8080
ENTRYPOINT ["opa-hub"]
