module github.com/TheGrimmChester/opa-hub

go 1.22

require (
	github.com/TheGrimmChester/open-auth-go v0.0.0
	github.com/TheGrimmChester/open-cache-go v0.0.0
	github.com/TheGrimmChester/open-clickhouse-go v0.2.0
	github.com/TheGrimmChester/open-crypto-go v0.0.0
	github.com/TheGrimmChester/open-http-go v0.0.0
	github.com/TheGrimmChester/open-logger-go v0.0.0
	github.com/TheGrimmChester/open-tenant-go v0.2.0
)

require github.com/golang-jwt/jwt/v5 v5.3.1 // indirect

replace (
	github.com/TheGrimmChester/open-auth-go => ../Open-Auth-Go
	github.com/TheGrimmChester/open-cache-go => ../Open-Cache-Go
	github.com/TheGrimmChester/open-clickhouse-go => ../Open-ClickHouse-Go
	github.com/TheGrimmChester/open-crypto-go => ../Open-Crypto-Go
	github.com/TheGrimmChester/open-http-go => ../Open-HTTP-Go
	github.com/TheGrimmChester/open-logger-go => ../Open-Logger-Go
	github.com/TheGrimmChester/open-tenant-go => ../Open-Tenant-Go
)
