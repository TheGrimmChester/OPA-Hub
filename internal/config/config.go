package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime settings for opa-hub.
type Config struct {
	ListenAddr         string
	JWTSecret          string
	ServiceJWTSecret   string
	ClickHouseURL      string
	ClickHouseDatabase string
	ClickHouseUser     string
	ClickHousePassword string
	OPAPublicURL       string
	EnrollToken        string
	AuthRequired       bool
	AgentStaleAfter    time.Duration
	CORSOrigin         string
}

// Load reads configuration from the environment.
func Load() Config {
	return Config{
		ListenAddr:         env("LISTEN_ADDR", ":8080"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		ServiceJWTSecret:   os.Getenv("OPEN_SERVICE_JWT_SECRET"),
		ClickHouseURL:      env("CLICKHOUSE_URL", "http://clickhouse:8123"),
		ClickHouseDatabase: env("CLICKHOUSE_DATABASE", "opa"),
		ClickHouseUser:     os.Getenv("CLICKHOUSE_USER"),
		ClickHousePassword: os.Getenv("CLICKHOUSE_PASSWORD"),
		OPAPublicURL:       os.Getenv("OPA_PUBLIC_URL"),
		EnrollToken:        os.Getenv("OPA_HUB_ENROLL_TOKEN"),
		AuthRequired:       truthy(os.Getenv("OPA_AUTH_REQUIRED")),
		AgentStaleAfter:    durationEnv("OPA_HUB_AGENT_STALE_AFTER", 5*time.Minute),
		CORSOrigin:         os.Getenv("CORS_ORIGIN"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func durationEnv(k string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return def
}
