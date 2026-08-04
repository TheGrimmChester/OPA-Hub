package query

import (
	"fmt"
	"net/http"
	"strings"
)

// appendExternalDependencyEdges enriches the map with DB/HTTP/Redis/cache edges
// extracted from spans_full JSON payloads (ClickHouse-side ARRAY JOIN).
func (h *Handler) appendExternalDependencyEdges(
	r *http.Request,
	timeFrom, timeTo, tenantRoot string,
	calcHealth func(float64, float64) string,
	services map[string]bool,
	externalDeps map[string]bool,
	edges *[]map[string]any,
) {
	_ = r
	type agg struct {
		from, target, depType, host, port, scheme string
		calls                                     uint64
		totalDur                                  float64
		errors                                    uint64
	}
	merge := map[string]*agg{}
	add := func(from, target, depType, host, port, scheme string, calls uint64, totalDur float64, errors uint64) {
		if invalidServiceName(from) || invalidServiceName(target) || calls == 0 {
			return
		}
		key := from + "->" + target + "|" + depType
		if cur, ok := merge[key]; ok {
			cur.calls += calls
			cur.totalDur += totalDur
			cur.errors += errors
			return
		}
		merge[key] = &agg{from: from, target: target, depType: depType, host: host, port: port, scheme: scheme, calls: calls, totalDur: totalDur, errors: errors}
	}

	dbSQL := fmt.Sprintf(`SELECT
		service,
		coalesce(nullIf(JSONExtractString(item, 'db_system'), ''), JSONExtractString(item, 'dbSystem')) AS db_system,
		JSONExtractString(item, 'db_host') AS db_host,
		JSONExtractString(item, 'db_port') AS db_port,
		count() AS call_count,
		sum(coalesce(nullIf(JSONExtractFloat(item, 'duration_ms'), 0), JSONExtractFloat(item, 'duration') * 1000)) AS total_duration,
		sum(JSONExtractString(item, 'status') IN ('error', '0')) AS error_count
		FROM opa.spans_full
		ARRAY JOIN JSONExtractArrayRaw(sql) AS item
		WHERE %s AND sql NOT IN ('', '[]', 'null') AND start_ts >= %s AND start_ts <= %s
		GROUP BY service, db_system, db_host, db_port LIMIT 5000`, tenantRoot, timeFrom, timeTo)
	if rows, err := h.Writer.Query(dbSQL); err == nil {
		for _, row := range rows {
			sys := asString(row, "db_system")
			if invalidServiceName(sys) {
				continue
			}
			host := asString(row, "db_host")
			if invalidServiceName(host) {
				host = ""
			}
			port := asString(row, "db_port")
			if port == "" {
				port = defaultDBPort(sys)
			}
			target := fmt.Sprintf("db:%s", sys)
			if host != "" {
				target = fmt.Sprintf("%s://%s", sys, host)
			}
			add(asString(row, "service"), target, "database", host, port, sys, asUint64(row, "call_count"), asFloat64(row, "total_duration"), asUint64(row, "error_count"))
		}
	}

	httpSQL := fmt.Sprintf(`SELECT
		service,
		coalesce(nullIf(JSONExtractString(item, 'url'), ''), nullIf(JSONExtractString(item, 'URL'), ''),
			concat(coalesce(nullIf(JSONExtractString(item, 'scheme'), ''), 'http'), '://', JSONExtractString(item, 'host'))) AS url,
		JSONExtractString(item, 'type') AS req_type,
		count() AS call_count,
		sum(coalesce(nullIf(JSONExtractFloat(item, 'duration_ms'), 0), JSONExtractFloat(item, 'duration') * 1000)) AS total_duration,
		sum(JSONExtractInt(item, 'status') >= 400 OR JSONExtractString(item, 'status') IN ('error', '0')) AS error_count
		FROM opa.spans_full
		ARRAY JOIN JSONExtractArrayRaw(http) AS item
		WHERE %s AND http NOT IN ('', '[]', 'null') AND start_ts >= %s AND start_ts <= %s
		GROUP BY service, url, req_type LIMIT 5000`, tenantRoot, timeFrom, timeTo)
	if rows, err := h.Writer.Query(httpSQL); err == nil {
		for _, row := range rows {
			from := asString(row, "service")
			url := asString(row, "url")
			if url == "" || invalidServiceName(url) {
				continue
			}
			if url == from || strings.HasPrefix(url, from+":") || strings.HasPrefix(url, from+"/") {
				continue
			}
			baseURL := url
			if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
				rest := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
				hostPart := strings.Split(rest, "/")[0]
				scheme := "http"
				if strings.HasPrefix(url, "https://") {
					scheme = "https"
				}
				baseURL = scheme + "://" + hostPart
			}
			if invalidServiceName(baseURL) {
				continue
			}
			depType := "http"
			if asString(row, "req_type") == "curl" {
				depType = "curl"
			}
			host, port, scheme := parseHTTPTarget(baseURL)
			add(from, baseURL, depType, host, port, scheme, asUint64(row, "call_count"), asFloat64(row, "total_duration"), asUint64(row, "error_count"))
		}
	}

	redisSQL := fmt.Sprintf(`SELECT
		service, coalesce(nullIf(JSONExtractString(item, 'host'), ''), 'redis') AS host,
		JSONExtractString(item, 'port') AS port, count() AS call_count,
		sum(coalesce(nullIf(JSONExtractFloat(item, 'duration_ms'), 0), JSONExtractFloat(item, 'duration') * 1000)) AS total_duration,
		sum(JSONExtractString(item, 'status') IN ('error', '0')) AS error_count
		FROM opa.spans_full ARRAY JOIN JSONExtractArrayRaw(redis) AS item
		WHERE %s AND redis NOT IN ('', '[]', 'null') AND start_ts >= %s AND start_ts <= %s
		GROUP BY service, host, port LIMIT 2000`, tenantRoot, timeFrom, timeTo)
	if rows, err := h.Writer.Query(redisSQL); err == nil {
		for _, row := range rows {
			host := asString(row, "host")
			if invalidServiceName(host) {
				host = "redis"
			}
			port := asString(row, "port")
			if port == "" {
				port = "6379"
			}
			add(asString(row, "service"), "redis://"+host, "redis", host, port, "redis", asUint64(row, "call_count"), asFloat64(row, "total_duration"), asUint64(row, "error_count"))
		}
	}

	cacheSQL := fmt.Sprintf(`SELECT
		service,
		coalesce(nullIf(JSONExtractString(item, 'system'), ''), nullIf(JSONExtractString(item, 'cache_system'), ''), 'cache') AS system,
		JSONExtractString(item, 'host') AS host, count() AS call_count,
		sum(coalesce(nullIf(JSONExtractFloat(item, 'duration_ms'), 0), JSONExtractFloat(item, 'duration') * 1000)) AS total_duration,
		sum(JSONExtractString(item, 'status') IN ('error', '0')) AS error_count
		FROM opa.spans_full ARRAY JOIN JSONExtractArrayRaw(cache) AS item
		WHERE %s AND cache NOT IN ('', '[]', 'null') AND start_ts >= %s AND start_ts <= %s
		GROUP BY service, system, host LIMIT 2000`, tenantRoot, timeFrom, timeTo)
	if rows, err := h.Writer.Query(cacheSQL); err == nil {
		for _, row := range rows {
			sys := asString(row, "system")
			host := asString(row, "host")
			target := "cache:" + sys
			if host != "" && !invalidServiceName(host) {
				target = "cache://" + host
			}
			add(asString(row, "service"), target, "cache", host, "", sys, asUint64(row, "call_count"), asFloat64(row, "total_duration"), asUint64(row, "error_count"))
		}
	}

	for _, a := range merge {
		services[a.from] = true
		externalDeps[a.target] = true
		avgLatency, errorRate := 0.0, 0.0
		if a.calls > 0 {
			avgLatency = a.totalDur / float64(a.calls)
			errorRate = float64(a.errors) / float64(a.calls) * 100
		}
		successRate := 100.0 - errorRate
		if successRate < 0 {
			successRate = 0
		}
		*edges = append(*edges, map[string]any{
			"from": a.from, "to": a.target,
			"avg_latency_ms": avgLatency, "min_latency_ms": avgLatency, "max_latency_ms": avgLatency,
			"p95_latency_ms": avgLatency, "p99_latency_ms": avgLatency,
			"error_rate": errorRate, "success_rate": successRate, "call_count": a.calls,
			"throughput": 0.0, "bytes_sent": uint64(0), "bytes_received": uint64(0),
			"health_status": calcHealth(errorRate, avgLatency), "dependency_type": a.depType,
			"dependency_target": a.target, "host": a.host, "port": a.port, "resolved_host": "", "scheme": a.scheme,
		})
	}
}

func appendExternalDepNodes(nodes []map[string]any, edges []map[string]any, externalDeps map[string]bool, calcHealth func(float64, float64) string) []map[string]any {
	for extDep := range externalDeps {
		depType := "external"
		switch {
		case strings.HasPrefix(extDep, "db:"):
			depType = "database"
		case strings.HasPrefix(extDep, "http://"), strings.HasPrefix(extDep, "https://"):
			depType = "http"
		case strings.HasPrefix(extDep, "redis://"), extDep == "redis":
			depType = "redis"
		case strings.HasPrefix(extDep, "cache:"), strings.HasPrefix(extDep, "cache://"):
			depType = "cache"
		}
		var totalCalls uint64
		var totalLatency, weightedErr float64
		for _, e := range edges {
			if e["to"] != extDep {
				continue
			}
			c := e["call_count"].(uint64)
			totalCalls += c
			totalLatency += e["avg_latency_ms"].(float64) * float64(c)
			weightedErr += e["error_rate"].(float64) * float64(c)
		}
		errorRate, avgLatency := 0.0, 0.0
		if totalCalls > 0 {
			errorRate = weightedErr / float64(totalCalls)
			avgLatency = totalLatency / float64(totalCalls)
		}
		nodes = append(nodes, map[string]any{
			"id": extDep, "service": extDep, "health_status": calcHealth(errorRate, avgLatency),
			"node_type": depType, "avg_duration": avgLatency, "error_rate": errorRate,
			"incoming_calls": totalCalls, "outgoing_calls": uint64(0),
		})
	}
	return nodes
}

func defaultDBPort(dbSystem string) string {
	switch strings.ToLower(dbSystem) {
	case "mysql", "mariadb":
		return "3306"
	case "postgres", "postgresql":
		return "5432"
	case "mongodb", "mongo":
		return "27017"
	case "mssql", "sqlserver":
		return "1433"
	case "clickhouse":
		return "9000"
	default:
		return ""
	}
}

func parseHTTPTarget(rawURL string) (host, port, scheme string) {
	scheme = "http"
	rest := rawURL
	if strings.HasPrefix(rawURL, "https://") {
		scheme = "https"
		rest = strings.TrimPrefix(rawURL, "https://")
	} else if strings.HasPrefix(rawURL, "http://") {
		rest = strings.TrimPrefix(rawURL, "http://")
	}
	hostPort := strings.Split(rest, "/")[0]
	if i := strings.LastIndex(hostPort, ":"); i > 0 && !strings.Contains(hostPort[i+1:], "]") {
		host, port = hostPort[:i], hostPort[i+1:]
	} else {
		host = hostPort
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port, scheme
}
