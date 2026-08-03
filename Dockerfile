FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/opa-hub .

FROM alpine:3.20
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/opa-hub /usr/local/bin/opa-hub
EXPOSE 8080
ENTRYPOINT ["opa-hub"]
