module github.com/TheGrimmChester/opa-hub

go 1.22

require (
	github.com/TheGrimmChester/open-auth-go v0.0.0
	github.com/TheGrimmChester/open-clickhouse-go v0.0.0
	github.com/TheGrimmChester/open-http-go v0.0.0
	github.com/TheGrimmChester/open-logger-go v0.0.0
)

replace (
	github.com/TheGrimmChester/open-auth-go => ../Open-Auth-Go
	github.com/TheGrimmChester/open-clickhouse-go => ../Open-ClickHouse-Go
	github.com/TheGrimmChester/open-http-go => ../Open-HTTP-Go
	github.com/TheGrimmChester/open-logger-go => ../Open-Logger-Go
)
