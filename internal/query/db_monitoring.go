package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeDBInstances handles GET /api/db/instances — latest snapshot per DB target.
func (h *Handler) ServeDBInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	scope := tenantAnd(r, "")
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT instance_id, engine, argMax(metrics_json, scraped_at) AS metrics_json,
		       max(scraped_at) AS scraped_at
		FROM opa.db_instance_snapshots
		WHERE 1=1%s
		GROUP BY instance_id, engine
		ORDER BY scraped_at DESC
		LIMIT 100`, scope))
	if err != nil {
		writeJSON(w, map[string]any{"instances": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}

	instances := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"id":         asString(row, "instance_id"),
			"engine":     asString(row, "engine"),
			"scraped_at": asString(row, "scraped_at"),
		}
		raw := asString(row, "metrics_json")
		var metrics map[string]any
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &metrics)
		}
		item["metrics"] = metrics
		instances = append(instances, item)
	}
	writeJSON(w, map[string]any{"instances": instances, "source": "opa-hub"})
}

// ServeDBStatements handles GET /api/db/statements — DB-side statement digests.
func (h *Handler) ServeDBStatements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	scope := tenantAnd(r, "")
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	extra := ""
	if instance != "" {
		extra = fmt.Sprintf(" AND instance_id = '%s'", escapeSQL(instance))
	}

	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT instance_id, engine, native_digest, opa_fingerprint, query_preview,
		       sum(calls) AS calls, sum(total_time_ms) AS total_time_ms,
		       avg(avg_time_ms) AS avg_time_ms,
		       sum(rows_examined) AS rows_examined, sum(rows_sent) AS rows_sent,
		       sum(tmp_disk) AS tmp_disk, max(full_scan) AS full_scan,
		       max(scraped_at) AS scraped_at
		FROM opa.db_statement_stats
		WHERE 1=1%s%s
		GROUP BY instance_id, engine, native_digest, opa_fingerprint, query_preview
		ORDER BY total_time_ms DESC
		LIMIT 100`, scope, extra))
	if err != nil {
		writeJSON(w, map[string]any{"statements": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}

	statements := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, map[string]any{
			"instance_id":     asString(row, "instance_id"),
			"engine":          asString(row, "engine"),
			"native_digest":   asString(row, "native_digest"),
			"opa_fingerprint": asString(row, "opa_fingerprint"),
			"query_preview":   asString(row, "query_preview"),
			"calls":           asFloat64(row, "calls"),
			"total_time_ms":   asFloat64(row, "total_time_ms"),
			"avg_time_ms":     asFloat64(row, "avg_time_ms"),
			"rows_examined":   asFloat64(row, "rows_examined"),
			"rows_sent":       asFloat64(row, "rows_sent"),
			"tmp_disk":        asFloat64(row, "tmp_disk"),
			"full_scan":       asUint64(row, "full_scan"),
			"scraped_at":      asString(row, "scraped_at"),
		})
	}
	writeJSON(w, map[string]any{"statements": statements, "source": "opa-hub"})
}

// ServeDBUnusedIndexes handles GET /api/db/unused-indexes.
func (h *Handler) ServeDBUnusedIndexes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	scope := tenantAnd(r, "")
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT instance_id, schema_name, table_name, index_name, max(detected_at) AS detected_at
		FROM opa.db_unused_indexes
		WHERE 1=1%s
		GROUP BY instance_id, schema_name, table_name, index_name
		ORDER BY detected_at DESC
		LIMIT 100`, scope))
	if err != nil {
		writeJSON(w, map[string]any{"indexes": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}

	indexes := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		indexes = append(indexes, map[string]any{
			"instance_id": asString(row, "instance_id"),
			"schema_name": asString(row, "schema_name"),
			"table_name":  asString(row, "table_name"),
			"index_name":  asString(row, "index_name"),
			"detected_at": asString(row, "detected_at"),
		})
	}
	writeJSON(w, map[string]any{"indexes": indexes, "source": "opa-hub"})
}

// ServeDBFingerprintMatch handles GET /api/db/fingerprint-match — join rate for DB digests.
func (h *Handler) ServeDBFingerprintMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	scope := tenantAnd(r, "")
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT count() AS total,
		       countIf(matched = 1) AS matched
		FROM (
			SELECT native_digest, max(matched) AS matched
			FROM opa.db_fingerprint_map
			WHERE 1=1%s
			GROUP BY native_digest
		)`, scope))
	if err != nil || len(rows) == 0 {
		writeJSON(w, map[string]any{"total": 0, "matched": 0, "match_rate_pct": 0, "source": "opa-hub"})
		return
	}

	total := asUint64(rows[0], "total")
	matched := asUint64(rows[0], "matched")
	rate := float64(0)
	if total > 0 {
		rate = float64(matched) * 100 / float64(total)
	}
	writeJSON(w, map[string]any{
		"total":          total,
		"matched":        matched,
		"match_rate_pct": rate,
		"source":         "opa-hub",
	})
}
