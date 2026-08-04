package query

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// Deep diagnostics reads + release markers (dashboard Diagnostics page).
// Ported from OPA-Agent wave27 handlers; ingest (/v1/heap|threads|locks) stays on edge.

var (
	diagDDLMu      sync.Mutex
	diagDDLReady   bool
)

var diagnosticsDDLStmts = []string{
	`CREATE TABLE IF NOT EXISTS opa.release_markers
(
    id               String,
    organization_id  String DEFAULT '',
    project_id       String DEFAULT '',
    service          LowCardinality(String),
    release          String,
    git_sha          String DEFAULT '',
    git_repo         String DEFAULT '',
    author           String DEFAULT '',
    message          String DEFAULT '',
    commits_json     String DEFAULT '[]',
    deployed_at      DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(deployed_at)
ORDER BY (organization_id, project_id, service, release)`,
	`CREATE TABLE IF NOT EXISTS opa.heap_snapshots
(
    id               String,
    organization_id  String DEFAULT '',
    project_id       String DEFAULT '',
    service          LowCardinality(String),
    host             LowCardinality(String) DEFAULT '',
    runtime          LowCardinality(String) DEFAULT '',
    total_bytes      UInt64 DEFAULT 0,
    dominators_json  String DEFAULT '[]',
    retained_json    String DEFAULT '[]',
    captured_at      DateTime64(3) DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toDate(captured_at)
ORDER BY (organization_id, project_id, service, captured_at, id)
TTL toDateTime(captured_at) + INTERVAL 30 DAY`,
	`CREATE TABLE IF NOT EXISTS opa.thread_samples
(
    organization_id  String DEFAULT '',
    project_id       String DEFAULT '',
    service          LowCardinality(String),
    host             LowCardinality(String) DEFAULT '',
    thread_id        String DEFAULT '',
    thread_name      String DEFAULT '',
    state            LowCardinality(String) DEFAULT '',
    stack_json       String DEFAULT '[]',
    lock_name        String DEFAULT '',
    wait_ms          Float64 DEFAULT 0,
    sampled_at       DateTime64(3) DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toDate(sampled_at)
ORDER BY (organization_id, project_id, service, sampled_at)
TTL toDateTime(sampled_at) + INTERVAL 14 DAY`,
	`CREATE TABLE IF NOT EXISTS opa.lock_contention
(
    organization_id  String DEFAULT '',
    project_id       String DEFAULT '',
    service          LowCardinality(String),
    lock_name        String,
    waiters          UInt64 DEFAULT 0,
    hold_ms          Float64 DEFAULT 0,
    wait_ms          Float64 DEFAULT 0,
    deadlock         UInt8 DEFAULT 0,
    holders_json     String DEFAULT '[]',
    observed_at      DateTime64(3) DEFAULT now64(3)
)
ENGINE = SummingMergeTree
PARTITION BY toDate(observed_at)
ORDER BY (organization_id, project_id, service, lock_name, observed_at)
TTL toDateTime(observed_at) + INTERVAL 14 DAY`,
}

func (h *Handler) ensureDiagnosticsTables() error {
	if h.Writer == nil {
		return fmt.Errorf("clickhouse writer not configured")
	}
	diagDDLMu.Lock()
	defer diagDDLMu.Unlock()
	if diagDDLReady {
		return nil
	}
	for _, stmt := range diagnosticsDDLStmts {
		if err := h.Writer.Exec(stmt); err != nil {
			return err
		}
	}
	diagDDLReady = true
	return nil
}

func diagID(prefix string, parts ...string) string {
	h := sha1.New()
	h.Write([]byte(prefix))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return prefix + "-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// ServeReleases handles GET/POST /api/releases — release markers for suspect-commit attribution.
func (h *Handler) ServeReleases(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	if err := h.ensureDiagnosticsTables(); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}

	switch r.Method {
	case http.MethodPost:
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "read error")
			return
		}
		var body struct {
			Service  string          `json:"service"`
			Release  string          `json:"release"`
			GitSHA   string          `json:"git_sha"`
			GitRepo  string          `json:"git_repo"`
			Author   string          `json:"author"`
			Message  string          `json:"message"`
			Commits  json.RawMessage `json:"commits"`
			Deployed string          `json:"deployed_at"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Service == "" || body.Release == "" {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "service and release required")
			return
		}
		commits := body.Commits
		if len(commits) == 0 {
			commits = json.RawMessage(`[]`)
		}
		org, proj := writeOrgProject(r)
		id := diagID("rel", org, body.Service, body.Release)
		ts := time.Now().UTC().Format("2006-01-02 15:04:05.000")
		if body.Deployed != "" {
			if t, err := time.Parse(time.RFC3339, body.Deployed); err == nil {
				ts = t.UTC().Format("2006-01-02 15:04:05.000")
			}
		}
		sql := fmt.Sprintf(`INSERT INTO opa.release_markers
			(id, organization_id, project_id, service, release, git_sha, git_repo, author, message, commits_json, deployed_at)
			VALUES ('%s','%s','%s','%s','%s','%s','%s','%s','%s','%s', toDateTime64('%s', 3))`,
			escapeSQL(id), escapeSQL(org), escapeSQL(proj),
			escapeSQL(body.Service), escapeSQL(body.Release),
			escapeSQL(body.GitSHA), escapeSQL(body.GitRepo),
			escapeSQL(body.Author), escapeSQL(body.Message),
			escapeSQL(string(commits)), escapeSQL(ts))
		if err := h.Writer.Exec(sql); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": id, "source": "opa-hub"})
	case http.MethodGet:
		scope := tenantAnd(r, "")
		svc := strings.TrimSpace(r.URL.Query().Get("service"))
		extra := ""
		if svc != "" {
			extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(svc))
		}
		rows, err := h.Writer.Query(fmt.Sprintf(`
			SELECT id, service, release, git_sha, git_repo, author, message, deployed_at
			FROM opa.release_markers FINAL WHERE 1=1%s%s
			ORDER BY deployed_at DESC LIMIT 100`, scope, extra))
		if err != nil {
			writeJSON(w, map[string]any{"releases": []any{}, "source": "opa-hub", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"releases": rows, "source": "opa-hub"})
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}

// ServeSuspectCommits handles GET /api/diagnostics/suspect-commits.
func (h *Handler) ServeSuspectCommits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	if err := h.ensureDiagnosticsTables(); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}

	org, proj := writeOrgProject(r)
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	windowH := clampInt(parseIntDefault(r.URL.Query().Get("hours"), 24), 1, 168)

	extra := ""
	if service != "" {
		extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	rels, err := h.Writer.Query(fmt.Sprintf(`
		SELECT service, release, git_sha, git_repo, author, message, commits_json, deployed_at
		FROM opa.release_markers FINAL
		WHERE organization_id = '%s' AND project_id = '%s'
		  AND deployed_at >= now() - INTERVAL %d HOUR%s
		ORDER BY deployed_at DESC LIMIT 50`,
		escapeSQL(org), escapeSQL(proj), windowH, extra))
	if err != nil {
		writeJSON(w, map[string]any{"suspects": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}

	type suspect struct {
		Service    string `json:"service"`
		Release    string `json:"release"`
		GitSHA     string `json:"git_sha"`
		GitRepo    string `json:"git_repo"`
		Author     string `json:"author"`
		Message    string `json:"message"`
		DeployedAt string `json:"deployed_at"`
		Score      float64
		Evidence   []string
		Commits    []any
		DiffURL    string `json:"diff_url,omitempty"`
	}
	out := make([]suspect, 0, len(rels))
	for _, row := range rels {
		svc := asString(row, "service")
		rel := asString(row, "release")
		sha := asString(row, "git_sha")
		repo := asString(row, "git_repo")
		s := suspect{
			Service: svc, Release: rel, GitSHA: sha, GitRepo: repo,
			Author: asString(row, "author"), Message: asString(row, "message"),
			DeployedAt: asString(row, "deployed_at"),
			Evidence:   []string{},
			Commits:    []any{},
		}
		_ = json.Unmarshal([]byte(asString(row, "commits_json")), &s.Commits)
		if repo != "" && sha != "" {
			s.DiffURL = strings.TrimRight(repo, "/") + "/commit/" + sha
		}

		dep := asString(row, "deployed_at")
		errAfter, totAfter := h.spanErrorShareAround(org, proj, svc, dep, true)
		errBefore, _ := h.spanErrorShareAround(org, proj, svc, dep, false)
		score := 40.0
		if totAfter > 0 && errAfter > errBefore*1.5 && errAfter > 0.01 {
			score += 40
			s.Evidence = append(s.Evidence, fmt.Sprintf("error rate %.2f%% → %.2f%% around deploy", errBefore*100, errAfter*100))
		} else if totAfter > 0 {
			s.Evidence = append(s.Evidence, fmt.Sprintf("error rate stable (%.2f%% after, n=%d)", errAfter*100, totAfter))
		}
		if len(s.Commits) > 0 {
			score += 5
		}
		s.Score = score
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })

	// Encode with explicit JSON field names for score/evidence/commits.
	payload := make([]map[string]any, 0, len(out))
	for _, s := range out {
		payload = append(payload, map[string]any{
			"service": s.Service, "release": s.Release, "git_sha": s.GitSHA, "git_repo": s.GitRepo,
			"author": s.Author, "message": s.Message, "deployed_at": s.DeployedAt,
			"score": s.Score, "evidence": s.Evidence, "commits": s.Commits, "diff_url": s.DiffURL,
		})
	}
	writeJSON(w, map[string]any{
		"suspects":   payload,
		"hours":      windowH,
		"source":     "opa-hub",
		"disclaimer": "Ranked release/commit suspects near regressions — not definitive blame.",
	})
}

func (h *Handler) spanErrorShareAround(org, proj, service, deployedAt string, after bool) (rate float64, n int) {
	if h.Writer == nil || deployedAt == "" {
		return 0, 0
	}
	// Normalize CH DateTime64 display forms that may include 'T' or zone.
	deployedAt = strings.ReplaceAll(deployedAt, "T", " ")
	if i := strings.IndexAny(deployedAt, "Z+"); i > 10 {
		deployedAt = strings.TrimSpace(deployedAt[:i])
	}
	extra := ""
	if service != "" {
		extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	var q string
	if after {
		q = fmt.Sprintf(`
			SELECT count() AS c, countIf(status_code >= 500) AS errs
			FROM opa.spans_min
			WHERE organization_id = '%s' AND project_id = '%s'%s
			  AND created_at >= toDateTime64('%s', 3)
			  AND created_at < toDateTime64('%s', 3) + INTERVAL 1 HOUR`,
			escapeSQL(org), escapeSQL(proj), extra, escapeSQL(deployedAt), escapeSQL(deployedAt))
	} else {
		q = fmt.Sprintf(`
			SELECT count() AS c, countIf(status_code >= 500) AS errs
			FROM opa.spans_min
			WHERE organization_id = '%s' AND project_id = '%s'%s
			  AND created_at < toDateTime64('%s', 3)
			  AND created_at >= toDateTime64('%s', 3) - INTERVAL 1 HOUR`,
			escapeSQL(org), escapeSQL(proj), extra, escapeSQL(deployedAt), escapeSQL(deployedAt))
	}
	rows, err := h.Writer.Query(q)
	if err != nil || len(rows) == 0 {
		return 0, 0
	}
	c := int(asFloat64(rows[0], "c"))
	e := asFloat64(rows[0], "errs")
	if c == 0 {
		return 0, 0
	}
	return e / float64(c), c
}

// ServeHeapDiagnostics handles GET /api/diagnostics/heap.
func (h *Handler) ServeHeapDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	if err := h.ensureDiagnosticsTables(); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}

	scope := tenantAnd(r, "")
	svc := strings.TrimSpace(r.URL.Query().Get("service"))
	extra := ""
	if svc != "" {
		extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(svc))
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id != "" {
		rows, err := h.Writer.Query(fmt.Sprintf(`
			SELECT id, service, host, runtime, total_bytes, dominators_json, retained_json, captured_at
			FROM opa.heap_snapshots WHERE id = '%s' LIMIT 1`, escapeSQL(id)))
		if err != nil || len(rows) == 0 {
			openhttp.WriteError(w, http.StatusNotFound, "not_found", "snapshot not found")
			return
		}
		row := rows[0]
		var dom, ret any
		_ = json.Unmarshal([]byte(asString(row, "dominators_json")), &dom)
		_ = json.Unmarshal([]byte(asString(row, "retained_json")), &ret)
		writeJSON(w, map[string]any{
			"id": asString(row, "id"), "service": asString(row, "service"),
			"host": asString(row, "host"), "runtime": asString(row, "runtime"),
			"total_bytes": asFloat64(row, "total_bytes"),
			"dominators":  dom, "retained_paths": ret,
			"captured_at": asString(row, "captured_at"),
			"source":      "opa-hub",
		})
		return
	}
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT id, service, host, runtime, total_bytes, captured_at
		FROM opa.heap_snapshots WHERE captured_at >= now() - INTERVAL 7 DAY%s%s
		ORDER BY captured_at DESC LIMIT 100`, scope, extra))
	if err != nil {
		writeJSON(w, map[string]any{"snapshots": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"snapshots": rows, "source": "opa-hub"})
}

// ServeThreadDiagnostics handles GET /api/diagnostics/threads.
func (h *Handler) ServeThreadDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	if err := h.ensureDiagnosticsTables(); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}

	scope := tenantAnd(r, "")
	svc := strings.TrimSpace(r.URL.Query().Get("service"))
	extra := ""
	if svc != "" {
		extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(svc))
	}
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT service, thread_name, state, lock_name, count() AS samples, avg(wait_ms) AS avg_wait_ms, max(sampled_at) AS last_seen
		FROM opa.thread_samples
		WHERE sampled_at >= now() - INTERVAL 6 HOUR%s%s
		GROUP BY service, thread_name, state, lock_name
		ORDER BY samples DESC LIMIT 200`, scope, extra))
	if err != nil {
		writeJSON(w, map[string]any{"threads": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"threads": rows, "source": "opa-hub"})
}

// ServeLockDiagnostics handles GET /api/diagnostics/locks.
func (h *Handler) ServeLockDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	if err := h.ensureDiagnosticsTables(); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "schema_error", err.Error())
		return
	}

	scope := tenantAnd(r, "")
	svc := strings.TrimSpace(r.URL.Query().Get("service"))
	extra := ""
	if svc != "" {
		extra = fmt.Sprintf(" AND service = '%s'", escapeSQL(svc))
	}
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT service, lock_name, sum(waiters) AS waiters, avg(hold_ms) AS hold_ms, avg(wait_ms) AS wait_ms,
		       max(deadlock) AS deadlock, max(observed_at) AS last_seen
		FROM opa.lock_contention
		WHERE observed_at >= now() - INTERVAL 6 HOUR%s%s
		GROUP BY service, lock_name
		ORDER BY wait_ms DESC LIMIT 200`, scope, extra))
	if err != nil {
		writeJSON(w, map[string]any{"locks": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"locks": rows, "source": "opa-hub"})
}
