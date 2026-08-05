package server

import (
	"encoding/json"
	"net/http"
	"time"

	openclickhouse "github.com/TheGrimmChester/open-clickhouse-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
	openlogger "github.com/TheGrimmChester/open-logger-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"

	"github.com/TheGrimmChester/opa-hub/internal/auth"
	"github.com/TheGrimmChester/opa-hub/internal/config"
	"github.com/TheGrimmChester/opa-hub/internal/ingest"
	"github.com/TheGrimmChester/opa-hub/internal/oamdir"
	"github.com/TheGrimmChester/opa-hub/internal/query"
	"github.com/TheGrimmChester/opa-hub/internal/registry"
	"github.com/TheGrimmChester/opa-hub/internal/store"
)

const version = "0.7.6"

// Server is the opa-hub HTTP control plane.
type Server struct {
	cfg     config.Config
	log     *openlogger.Logger
	mux     *http.ServeMux
	reg     *registry.Registry
	writer  *store.Writer
	started time.Time
	authH   *auth.Handler
	// oamDir reads the authoritative organization directory from OAM. Nil-safe:
	// when PEER_OAM_URL is unset the hub keeps answering from its agent registry.
	oamDir *oamdir.Client
}

// New builds a fully wired hub server.
func New(cfg config.Config) *Server {
	// Mirror OPA_AUTH_REQUIRED into Open-Tenant-Go so missing/"all" headers
	// cannot widen list/query scope when auth is on (same as OPA-Agent / ORA/OSA/OPL).
	opentenant.SetAuthEnforced(cfg.AuthRequired)

	log := openlogger.New("opa-hub")
	reg := registry.New(cfg.AgentStaleAfter)
	writer := store.NewWriter(openclickhouse.Config{
		URL:      cfg.ClickHouseURL,
		Database: cfg.ClickHouseDatabase,
		Username: cfg.ClickHouseUser,
		Password: cfg.ClickHousePassword,
	})
	if err := writer.EnsureDatabase(); err != nil {
		log.Warn("clickhouse ensure database", map[string]any{"error": err.Error(), "database": cfg.ClickHouseDatabase})
	}
	s := &Server{
		cfg:     cfg,
		log:     log,
		mux:     http.NewServeMux(),
		reg:     reg,
		writer:  writer,
		started: time.Now().UTC(),
		oamDir:  oamdir.New(),
	}
	if oamdir.Configured() {
		log.Info("oam directory", map[string]any{
			"peer_oam_url": oamdir.PeerURL(),
			"note":         "organizations come from the OAM directory; the agent registry is the fallback",
		})
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	regH := &registry.Handler{Reg: s.reg, EnrollToken: s.cfg.EnrollToken}
	ingH := &ingest.Handler{Reg: s.reg, Writer: s.writer, EnrollToken: s.cfg.EnrollToken}
	authH := auth.New(s.cfg.JWTSecret, s.cfg.AuthRequired, s.cfg.OPAPublicURL, s.cfg.ServiceJWTSecret)
	s.authH = authH
	queryH := &query.Handler{
		Reg:                 s.reg,
		Writer:              s.writer,
		StartedAt:           s.started,
		Version:             version,
		EnrollTokenRequired: s.cfg.EnrollToken != "",
	}

	s.mux.HandleFunc("/api/health", s.handleHealth)

	s.mux.HandleFunc("/api/agents/register", regH.ServeRegister)
	s.mux.HandleFunc("/api/agents/heartbeat", regH.ServeHeartbeat)
	s.mux.HandleFunc("/api/agents", authH.Middleware(regH.ServeList))
	s.mux.HandleFunc("/api/agents/", authH.Middleware(regH.ServeGet))

	s.mux.HandleFunc("/api/ingest/push", ingH.ServePush)

	s.mux.HandleFunc("/api/auth/login", authH.ServeLogin)
	s.mux.HandleFunc("/api/auth/register", authH.ServeRegister)
	s.mux.HandleFunc("/api/auth/logout", authH.ServeLogout)
	s.mux.HandleFunc("/api/auth/status", authH.ServeStatus)

	// Dashboard query surface — hub owns ClickHouse reads (not edge agents).
	s.mux.HandleFunc("/api/query", authH.Middleware(queryH.ServeQueryRoot))
	s.mux.HandleFunc("/api/admin", authH.Middleware(queryH.ServeAdmin))
	s.mux.HandleFunc("/api/services/metadata", authH.Middleware(queryH.ServeServicesMetadata))
	s.mux.HandleFunc("/api/services/", authH.Middleware(queryH.ServeServicesSubpath))
	s.mux.HandleFunc("/api/services", authH.Middleware(queryH.ServeServices))
	s.mux.HandleFunc("/api/traces/", authH.Middleware(queryH.ServeTracesSubpath))
	s.mux.HandleFunc("/api/traces", authH.Middleware(queryH.ServeTraces))
	s.mux.HandleFunc("/api/explore/facets", authH.Middleware(queryH.ServeExploreFacets))

	// Span-derived observability reads (SQL / Redis / HTTP / dumps / stats / commands / KT)
	s.mux.HandleFunc("/api/sql/queries/", authH.Middleware(queryH.ServeSQLQueriesSubpath))
	s.mux.HandleFunc("/api/sql/queries", authH.Middleware(queryH.ServeSQLQueries))
	s.mux.HandleFunc("/api/redis/operations", authH.Middleware(queryH.ServeRedisOperations))
	s.mux.HandleFunc("/api/http-calls", authH.Middleware(queryH.ServeHTTPCalls))
	s.mux.HandleFunc("/api/dumps", authH.Middleware(queryH.ServeDumps))
	s.mux.HandleFunc("/api/stats", authH.Middleware(queryH.ServeStats))
	s.mux.HandleFunc("/api/commands", authH.Middleware(queryH.ServeCommands))
	s.mux.HandleFunc("/api/key-transactions", authH.Middleware(queryH.ServeKeyTransactions))

	// Metrics explorer + performance charts
	s.mux.HandleFunc("/api/infra/hosts", authH.Middleware(queryH.ServeInfraHosts))
	s.mux.HandleFunc("/api/metrics/names", authH.Middleware(queryH.ServeMetricNames))
	s.mux.HandleFunc("/api/metrics/labels", authH.Middleware(queryH.ServeMetricLabels))
	s.mux.HandleFunc("/api/metrics/label-values", authH.Middleware(queryH.ServeMetricLabelValues))
	s.mux.HandleFunc("/api/metrics/query-range", authH.Middleware(queryH.ServeMetricQueryRange))
	s.mux.HandleFunc("/api/metrics/performance", authH.Middleware(queryH.ServeMetricsPerformance))
	s.mux.HandleFunc("/api/metrics/network", authH.Middleware(queryH.ServeMetricsNetwork))

	// Service map
	s.mux.HandleFunc("/api/service-map/thresholds", authH.Middleware(queryH.ServeServiceMapThresholds))
	s.mux.HandleFunc("/api/service-map/edge-traces", authH.Middleware(queryH.ServeServiceMapEdgeTraces))
	s.mux.HandleFunc("/api/service-map", authH.Middleware(queryH.ServeServiceMap))

	// Alerts (rules + history in ClickHouse; evaluation remains on edge agent)
	s.mux.HandleFunc("/api/alerts/", authH.Middleware(queryH.ServeAlertsSubpath))
	s.mux.HandleFunc("/api/alerts", authH.Middleware(queryH.ServeAlerts))

	// RUM (browser sessions / vitals / detail / replay reads)
	s.mux.HandleFunc("/api/rum/metrics", authH.Middleware(queryH.ServeRUMMetrics))
	s.mux.HandleFunc("/api/rum/detail", authH.Middleware(queryH.ServeRUMDetail))
	s.mux.HandleFunc("/api/rum/slo", authH.Middleware(queryH.ServeRUMSLO))
	s.mux.HandleFunc("/api/rum/facets", authH.Middleware(queryH.ServeRUMFacets))
	s.mux.HandleFunc("/api/rum/vitals/attribution", authH.Middleware(queryH.ServeRUMVitalsAttribution))
	s.mux.HandleFunc("/api/rum/replay-timeline/", authH.Middleware(queryH.ServeRUMReplayTimeline))
	s.mux.HandleFunc("/api/rum/replay/", authH.Middleware(queryH.ServeRUMReplay))
	s.mux.HandleFunc("/api/rum/mobile/sessions", authH.Middleware(queryH.ServeRUMMobileSessions))
	s.mux.HandleFunc("/api/rum/sessions/", authH.Middleware(queryH.ServeRUMSessionsSubpath))
	s.mux.HandleFunc("/api/rum/sessions", authH.Middleware(queryH.ServeRUMSessions))
	// Mobile crash reads (POST ingest stays on edge agent)
	s.mux.HandleFunc("/api/mobile/crashes", authH.Middleware(queryH.ServeMobileCrashes))

	// Profiling + errors (list/detail + group status/assign mutations)
	s.mux.HandleFunc("/api/profiles/flame", authH.Middleware(queryH.ServeProfilesFlame))
	s.mux.HandleFunc("/api/profiles", authH.Middleware(queryH.ServeProfiles))
	s.mux.HandleFunc("/api/errors/", authH.Middleware(queryH.ServeErrorsSubpath))
	s.mux.HandleFunc("/api/errors", authH.Middleware(queryH.ServeErrors))
	// Synthetics: hub owns list/CRUD/results against CH; edge agent runs probes
	s.mux.HandleFunc("/api/synthetics/locations", authH.Middleware(queryH.ServeSyntheticsLocations))
	s.mux.HandleFunc("/api/synthetics/", authH.Middleware(queryH.ServeSyntheticsSubpath))
	s.mux.HandleFunc("/api/synthetics", authH.Middleware(queryH.ServeSynthetics))

	// SLOs (CRUD + compliance reads; evaluation remains on edge agent)
	s.mux.HandleFunc("/api/slos/", authH.Middleware(queryH.ServeSLOsSubpath))
	s.mux.HandleFunc("/api/slos", authH.Middleware(queryH.ServeSLOs))

	// Anomalies list (detector/analyze remain on edge agent)
	s.mux.HandleFunc("/api/anomalies", authH.Middleware(queryH.ServeAnomalies))

	// Logs explorer (main nav)
	s.mux.HandleFunc("/api/logs", authH.Middleware(queryH.ServeLogs))

	// Cohort compare (Compare Traces page)
	s.mux.HandleFunc("/api/transactions/compare", authH.Middleware(queryH.ServeTransactionsCompare))

	// Platform ops (System page) + DB monitoring reads (Databases page)
	s.mux.HandleFunc("/api/version", authH.Middleware(queryH.ServeVersion))
	s.mux.HandleFunc("/api/topology", authH.Middleware(queryH.ServeTopology))
	s.mux.HandleFunc("/api/ops/status", authH.Middleware(queryH.ServeOpsStatus))
	s.mux.HandleFunc("/api/audit", authH.Require("admin", queryH.ServeAudit))
	s.mux.HandleFunc("/api/db/instances", authH.Middleware(queryH.ServeDBInstances))
	s.mux.HandleFunc("/api/db/statements", authH.Middleware(queryH.ServeDBStatements))
	s.mux.HandleFunc("/api/db/fingerprint-match", authH.Middleware(queryH.ServeDBFingerprintMatch))
	s.mux.HandleFunc("/api/db/unused-indexes", authH.Middleware(queryH.ServeDBUnusedIndexes))

	// Deep diagnostics (Diagnostics page) — reads + release markers; heap/thread/lock ingest stays on edge
	s.mux.HandleFunc("/api/releases", authH.Middleware(queryH.ServeReleases))
	s.mux.HandleFunc("/api/diagnostics/suspect-commits", authH.Middleware(queryH.ServeSuspectCommits))
	s.mux.HandleFunc("/api/diagnostics/heap", authH.Middleware(queryH.ServeHeapDiagnostics))
	s.mux.HandleFunc("/api/diagnostics/threads", authH.Middleware(queryH.ServeThreadDiagnostics))
	s.mux.HandleFunc("/api/diagnostics/locks", authH.Middleware(queryH.ServeLockDiagnostics))

	s.registerTenancyAndPeerRoutes()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	openhttp.CORSAllowOrigins(w, s.cfg.CORSOrigin)
	chOK := s.cfg.ClickHouseURL != ""
	if err := s.writer.Ping(); err != nil {
		chOK = false
		_ = err
	}
	authMode := s.cfg.AuthMode
	if authMode == "" {
		authMode = "standalone"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"service":    "opa-hub",
		"version":    version,
		"agents":     s.reg.Count(),
		"clickhouse": chOK,
		"topology":   "hub-spoke",
		"auth_mode":  authMode,
	})
}

// Handler returns the root HTTP handler with optional CORS preflight.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openhttp.CORSAllowOrigins(w, s.cfg.CORSOrigin)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	s.log.Info("listening", map[string]any{
		"addr":          s.cfg.ListenAddr,
		"enroll_token":  s.cfg.EnrollToken != "",
		"auth_required": s.cfg.AuthRequired,
		"clickhouse":    s.cfg.ClickHouseURL,
	})
	return http.ListenAndServe(s.cfg.ListenAddr, s.Handler())
}
