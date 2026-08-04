package query

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

func formatBytes(n uint64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.2f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.2f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ServeStats handles GET /api/stats — hub-side trace + storage summary.
func (h *Handler) ServeStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	scope := "WHERE " + tenantWhere(r, "") + entrySpanConjunct("")
	scope += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	scope += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	tracesSQL := fmt.Sprintf(`SELECT
		count() AS total_traces,
		(SELECT count() FROM opa.spans_min WHERE %s) AS total_spans,
		if(count() > 0, countIf(status = 'error' OR status = '0') / count() * 100, 0) AS error_rate,
		avg(duration_ms) AS avg_duration_ms,
		quantile(0.50)(duration_ms) AS p50_duration_ms,
		quantile(0.95)(duration_ms) AS p95_duration_ms,
		quantile(0.99)(duration_ms) AS p99_duration_ms
		FROM opa.spans_min %s`, tenantWhere(r, ""), scope)

	tracesStats := map[string]any{
		"total_traces":    uint64(0),
		"total_spans":     uint64(0),
		"error_rate":      0.0,
		"avg_duration_ms": 0.0,
		"p50_duration_ms": 0.0,
		"p95_duration_ms": 0.0,
		"p99_duration_ms": 0.0,
		"by_service":      []any{},
	}
	if rows, err := h.Writer.Query(tracesSQL); err == nil && len(rows) > 0 {
		row := rows[0]
		tracesStats["total_traces"] = asUint64(row, "total_traces")
		tracesStats["total_spans"] = asUint64(row, "total_spans")
		tracesStats["error_rate"] = asFloat64(row, "error_rate")
		tracesStats["avg_duration_ms"] = asFloat64(row, "avg_duration_ms")
		tracesStats["p50_duration_ms"] = asFloat64(row, "p50_duration_ms")
		tracesStats["p95_duration_ms"] = asFloat64(row, "p95_duration_ms")
		tracesStats["p99_duration_ms"] = asFloat64(row, "p99_duration_ms")
	}

	svcSQL := fmt.Sprintf(`SELECT
		service,
		count() AS traces,
		count() AS spans,
		if(count() > 0, countIf(status = 'error' OR status = '0') / count() * 100, 0) AS error_rate
		FROM opa.spans_min %s
		GROUP BY service ORDER BY traces DESC LIMIT 20`, scope)
	byService := make([]map[string]any, 0)
	if rows, err := h.Writer.Query(svcSQL); err == nil {
		for _, row := range rows {
			byService = append(byService, map[string]any{
				"service":    asString(row, "service"),
				"traces":     asUint64(row, "traces"),
				"spans":      asUint64(row, "spans"),
				"error_rate": asFloat64(row, "error_rate"),
			})
		}
	}
	tracesStats["by_service"] = byService

	dbName := "opa"
	if h.Writer != nil && h.Writer.Config().Database != "" {
		dbName = h.Writer.Config().Database
	}
	dbSQL := fmt.Sprintf(`SELECT
		table,
		sum(bytes) AS size_bytes,
		sum(rows) AS rows
		FROM system.parts
		WHERE database = '%s' AND active
		GROUP BY table
		ORDER BY size_bytes DESC`, escapeSQL(dbName))
	var totalSize uint64
	tables := make([]map[string]any, 0)
	if rows, err := h.Writer.Query(dbSQL); err == nil {
		for _, row := range rows {
			size := asUint64(row, "size_bytes")
			totalSize += size
			tables = append(tables, map[string]any{
				"name":          asString(row, "table"),
				"size_bytes":    size,
				"size_readable": formatBytes(size),
				"rows":          asUint64(row, "rows"),
			})
		}
	}

	agentStats := map[string]any{
		"queue_size":     0,
		"incoming_total": 0,
		"dropped_total":  0,
		"registered":     0,
	}
	if h.Reg != nil {
		agentStats["registered"] = h.Reg.Count()
	}
	if h.Writer != nil {
		st := h.Writer.Stats()
		if v, ok := st["ingest_events"]; ok {
			agentStats["incoming_total"] = v
		}
		if v, ok := st["batches"]; ok {
			agentStats["queue_size"] = v
		}
	}

	writeJSON(w, map[string]any{
		"traces": tracesStats,
		"agent":  agentStats,
		"database": map[string]any{
			"total_size_bytes":    totalSize,
			"total_size_readable": formatBytes(totalSize),
			"tables":              tables,
		},
		"source": "opa-hub",
	})
}

// ServeCommands handles GET /api/commands — CLI/worker/cron entry spans.
func (h *Handler) ServeCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	where := "WHERE " + tenantWhere(r, "") + " AND is_entry = 1 AND is_cli = 1"
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT
		service,
		name,
		count() AS requests,
		count() AS stored,
		avg(duration_ms) AS avg_duration_ms,
		quantile(0.50)(duration_ms) AS p50_duration_ms,
		quantile(0.95)(duration_ms) AS p95_duration_ms,
		countIf(status = 'error' OR status = '0') AS error_count
		FROM opa.spans_min
		%s
		GROUP BY service, name
		ORDER BY requests DESC
		LIMIT 500`, where)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	commands := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		requests := asFloat64(row, "requests")
		stored := float64(asUint64(row, "stored"))
		suppressed := requests - stored
		if suppressed < 0 {
			suppressed = 0
		}
		ratio := 1.0
		if requests > 0 {
			ratio = stored / requests
		}
		errCount := asFloat64(row, "error_count")
		errRate := 0.0
		if requests > 0 {
			errRate = errCount / requests * 100
		}
		commands = append(commands, map[string]any{
			"service":         asString(row, "service"),
			"name":            asString(row, "name"),
			"requests":        requests,
			"stored":          stored,
			"suppressed":      suppressed,
			"sample_ratio":    ratio,
			"avg_duration_ms": asFloat64(row, "avg_duration_ms"),
			"p50_duration_ms": asFloat64(row, "p50_duration_ms"),
			"p95_duration_ms": asFloat64(row, "p95_duration_ms"),
			"error_rate":      errRate,
		})
	}
	writeJSON(w, map[string]any{
		"commands":   commands,
		"population": "command",
		"source":     "opa-hub",
	})
}

// ServeKeyTransactions handles GET/POST /api/key-transactions.
func (h *Handler) ServeKeyTransactions(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sql := fmt.Sprintf(`SELECT transaction_id, name, service, pattern, description, enabled, created_at, updated_at
			FROM opa.key_transactions
			WHERE %s
			ORDER BY created_at DESC`, tenantWhere(r, ""))
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		txs := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			txs = append(txs, map[string]any{
				"transaction_id": asString(row, "transaction_id"),
				"name":           asString(row, "name"),
				"service":        asString(row, "service"),
				"pattern":        asString(row, "pattern"),
				"description":    asString(row, "description"),
				"enabled":        asUint64(row, "enabled") > 0,
				"created_at":     asString(row, "created_at"),
				"updated_at":     asString(row, "updated_at"),
			})
		}
		writeJSON(w, map[string]any{"transactions": txs, "source": "opa-hub"})
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid body")
			return
		}
		var tx struct {
			Name        string `json:"name"`
			Service     string `json:"service"`
			Pattern     string `json:"pattern"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(body, &tx); err != nil || strings.TrimSpace(tx.Name) == "" || strings.TrimSpace(tx.Service) == "" {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "name and service required")
			return
		}
		org, proj := writeOrgProject(r)
		txID := newID()
		insert := fmt.Sprintf(`INSERT INTO opa.key_transactions
			(organization_id, project_id, transaction_id, name, service, pattern, description)
			VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
			escapeSQL(org), escapeSQL(proj), escapeSQL(txID),
			escapeSQL(tx.Name), escapeSQL(tx.Service), escapeSQL(tx.Pattern), escapeSQL(tx.Description))
		if err := h.Writer.Exec(insert); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "insert_error", err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"transaction_id": txID,
			"name":           tx.Name,
			"service":        tx.Service,
			"pattern":        tx.Pattern,
			"description":    tx.Description,
			"enabled":        true,
			"created_at":     time.Now().UTC().Format("2006-01-02 15:04:05"),
			"source":         "opa-hub",
		})
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}
