// Package oamdir reads the family's authoritative organization directory from
// OAM (Open Account Manager).
//
// The hub used to answer /api/tenancy/organizations from its in-memory agent
// registry: an organization existed only if an agent had enrolled under it, and
// every org vanished on a hub restart. OPM and OSA seed their org pickers from
// that endpoint, so a freshly created organization was invisible until telemetry
// arrived.
//
// With PEER_OAM_URL set the hub reads the durable directory instead. Unset, it
// keeps the registry behaviour exactly — the same rollback switch every other OAM
// consumer has.
package oamdir

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

// cacheTTL bounds how stale the hub's view can be. The directory changes when a
// human creates an organization, so seconds of staleness are fine and a per-request
// peer call would make the hub chatty and couple its latency to OAM's.
const cacheTTL = 30 * time.Second

// Org is one directory entry.
type Org struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Client reads and caches the OAM directory.
type Client struct {
	http *http.Client

	mu       sync.Mutex
	cached   []Org
	cachedAt time.Time
}

// New builds a Client. It is safe to construct even when OAM is not configured;
// Configured() reports whether it will do anything.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 5 * time.Second}}
}

// PeerURL is the configured OAM base URL, or "".
func PeerURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
}

// Configured reports whether the hub should consult OAM at all.
func Configured() bool { return PeerURL() != "" }

// Organizations returns the directory.
//
// On any failure it returns the error and the caller falls back to the registry:
// a hub that cannot reach OAM should still serve the org list it can prove, rather
// than an empty picker. A stale cache is preferred to an error, because a brief
// OAM outage should not empty every product's org picker.
func (c *Client) Organizations() ([]Org, error) {
	if !Configured() {
		return nil, fmt.Errorf("PEER_OAM_URL not configured")
	}
	c.mu.Lock()
	if time.Since(c.cachedAt) < cacheTTL && c.cached != nil {
		out := append([]Org(nil), c.cached...)
		c.mu.Unlock()
		return out, nil
	}
	stale := append([]Org(nil), c.cached...)
	c.mu.Unlock()

	orgs, err := c.fetch()
	if err != nil {
		if len(stale) > 0 {
			// Serve the last known directory rather than failing: an OAM blip
			// must not blank out org pickers across the family.
			return stale, nil
		}
		return nil, err
	}
	c.mu.Lock()
	c.cached = orgs
	c.cachedAt = time.Now()
	c.mu.Unlock()
	return orgs, nil
}

func (c *Client) fetch() ([]Org, error) {
	secret := []byte(strings.TrimSpace(os.Getenv("OPEN_SERVICE_JWT_SECRET")))
	if len(secret) == 0 {
		return nil, fmt.Errorf("OPEN_SERVICE_JWT_SECRET not set; cannot authenticate to OAM")
	}
	token, err := openauth.MintServiceJWT(secret, "opa-hub", "oam-api", "orgs:read health:read")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, PeerURL()+"/api/tenancy/organizations", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("oam returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Organizations []Org `json:"organizations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Organizations, nil
}
