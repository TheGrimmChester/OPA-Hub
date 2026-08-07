package oamdir

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
	opencache "github.com/TheGrimmChester/open-cache-go"
	opencrypto "github.com/TheGrimmChester/open-crypto-go"
)

// cacheTTL bounds how stale the hub's view can be. The directory changes when a
// human creates an organization, so seconds of staleness are fine and a per-request
// peer call would make the hub chatty and couple its latency to OAM's.
const cacheTTL = 30 * time.Second

const oamdirCacheKey = "oamdir:orgs"

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

	l2 *opencache.Layered
}

var (
	hubCrypto    *opencrypto.Engine
	hubCryptoOnce sync.Once
)

func hubLayeredCache() *opencache.Layered {
	l1 := 20000
	prefix := strings.TrimSpace(os.Getenv("OPA_SEC_KEY_PREFIX"))
	if prefix == "" {
		prefix = "opa:sec:"
	}
	hubCryptoOnce.Do(func() {
		eng, err := opencrypto.NewEngineFromEnv(nil)
		if err == nil {
			hubCrypto = eng
		}
	})
	lc, err := opencache.NewLayered(opencache.Config{
		RedisURL:  os.Getenv("REDIS_URL"),
		L1Max:     l1,
		KeyPrefix: prefix,
		Crypto:    hubCrypto,
	})
	if err != nil {
		lc, _ = opencache.NewLayered(opencache.Config{L1Max: l1, KeyPrefix: prefix, Crypto: hubCrypto})
	}
	return lc
}

// New builds a Client.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 5 * time.Second}, l2: hubLayeredCache()}
}

// PeerURL is the configured OAM base URL, or "".
func PeerURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
}

// Configured reports whether the hub should consult OAM at all.
func Configured() bool { return PeerURL() != "" }

// Organizations returns the directory.
func (c *Client) Organizations() ([]Org, error) {
	if !Configured() {
		return nil, fmt.Errorf("PEER_OAM_URL not configured")
	}
	if strings.TrimSpace(os.Getenv("OPEN_SERVICE_JWT_SECRET")) == "" {
		return nil, fmt.Errorf("OPEN_SERVICE_JWT_SECRET not set; cannot authenticate to OAM")
	}
	c.mu.Lock()
	if time.Since(c.cachedAt) < cacheTTL && c.cached != nil {
		out := append([]Org(nil), c.cached...)
		c.mu.Unlock()
		return out, nil
	}
	stale := append([]Org(nil), c.cached...)
	c.mu.Unlock()

	if orgs, ok := c.loadRedisStale(); ok {
		c.mu.Lock()
		c.cached = orgs
		c.cachedAt = time.Now()
		c.mu.Unlock()
		return orgs, nil
	}

	orgs, err := c.fetch()
	if err != nil {
		if len(stale) > 0 {
			return stale, nil
		}
		return nil, err
	}
	c.storeRedis(orgs)
	c.mu.Lock()
	c.cached = orgs
	c.cachedAt = time.Now()
	c.mu.Unlock()
	return orgs, nil
}

func (c *Client) loadRedisStale() ([]Org, bool) {
	if c.l2 == nil {
		return nil, false
	}
	ctx := context.Background()
	cryptoCtx := opencrypto.CryptoContext{Scope: opencrypto.ScopePublic, LogicalKey: oamdirCacheKey}
	var raw []byte
	var ok bool
	if hubCrypto != nil {
		raw, ok = c.l2.GetEncrypted(ctx, cryptoCtx, oamdirCacheKey)
	} else {
		raw, ok = c.l2.Get(ctx, oamdirCacheKey)
	}
	if !ok {
		return nil, false
	}
	var orgs []Org
	if err := json.Unmarshal(raw, &orgs); err != nil {
		return nil, false
	}
	return orgs, true
}

func (c *Client) storeRedis(orgs []Org) {
	if c.l2 == nil {
		return
	}
	raw, err := json.Marshal(orgs)
	if err != nil {
		return
	}
	ctx := context.Background()
	cryptoCtx := opencrypto.CryptoContext{Scope: opencrypto.ScopePublic, LogicalKey: oamdirCacheKey}
	if hubCrypto != nil {
		_ = c.l2.SetEncrypted(ctx, cryptoCtx, oamdirCacheKey, raw, cacheTTL*10)
		return
	}
	c.l2.SetPlain(ctx, oamdirCacheKey, raw, cacheTTL*10)
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
