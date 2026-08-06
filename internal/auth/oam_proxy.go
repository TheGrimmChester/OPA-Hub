package auth

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// oamAuthConfigured reports co-deployed family login via OAM instead of LocalIssuer.
func oamAuthConfigured(authMode string) bool {
	if peerOAMURL() == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(authMode)) {
	case "standalone", "local", "solo":
		return false
	default:
		return true
	}
}

func peerOAMURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
}

func proxyOAMAuth(w http.ResponseWriter, r *http.Request, path string) {
	base := peerOAMURL()
	if base == "" {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "oam_unconfigured", "PEER_OAM_URL not configured")
		return
	}
	target := base + path
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, strings.NewReader(string(body)))
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "proxy_failed", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		openhttp.WriteError(w, http.StatusBadGateway, "oam_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, h := range []string{"Content-Type", "Set-Cookie"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}
