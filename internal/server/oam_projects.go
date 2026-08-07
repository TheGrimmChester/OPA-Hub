package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"

	"github.com/TheGrimmChester/opa-hub/internal/oamdir"
)

// handleOAMProjects proxies GET OAM /api/projects for the dashboard switcher.
//
// Fail-closed hook (ingest / query mutations that bind a directory project): call
// the same upstream with ?product=opa and reject when the concrete X-Project-ID
// is absent. Skip when PEER_OAM_URL is unset or the project header is empty/"all".
// Enablement writes stay on OAM only.
//
// Intentionally not wired on hub ingest/query today: OPA-Hub has no job/scan
// enqueue entrypoint that stamps a concrete OAM directory X-Project-ID the way
// OSA/OPL/ORA/OPM do. Agent→hub ingest stays unscoped by directory enablement;
// product dashboards still list via ?product=opa.
func (s *Server) handleOAMProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	base := oamdir.PeerURL()
	if base == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects":         []any{},
			"peer_unavailable": true,
			"peer":             "oam-api",
			"note":             "Set PEER_OAM_URL to discover projects.",
		})
		return
	}
	target := oamProjectsTarget(base, r.URL.Query())
	raw, status, err := proxyOAMProjectsGET(r.Context(), target, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects": []any{},
			"error":    err.Error(),
			"peer":     "oam-api",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(aliasDirectoryIDs(raw, "projects", "project_id"))
}

func oamProjectsTarget(base string, q url.Values) string {
	target := strings.TrimRight(base, "/") + "/api/projects"
	vals := url.Values{}
	if org := strings.TrimSpace(q.Get("organization_id")); org != "" && !strings.EqualFold(org, "all") {
		vals.Set("organization_id", org)
	}
	if product := strings.TrimSpace(q.Get("product")); product != "" {
		vals.Set("product", product)
	}
	if enc := vals.Encode(); enc != "" {
		target += "?" + enc
	}
	return target
}

func aliasDirectoryIDs(raw []byte, listKey, aliasKey string) []byte {
	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return raw
	}
	list, ok := payload[listKey].([]any)
	if !ok {
		return raw
	}
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := row[aliasKey]; exists {
			continue
		}
		if id, ok := row["id"].(string); ok && id != "" {
			row[aliasKey] = id
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return out
}

func proxyOAMProjectsGET(ctx context.Context, target string, r *http.Request) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	} else if c, err := r.Cookie(openauth.CookieName); err == nil && c.Value != "" {
		req.Header.Set("Authorization", "Bearer "+c.Value)
	}
	for _, h := range []string{"X-Organization-ID", "X-Project-ID", "X-Tenant-User-ID"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}
