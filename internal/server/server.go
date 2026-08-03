package server

import (
	"encoding/json"
	"net/http"
	"time"

	openclickhouse "github.com/TheGrimmChester/open-clickhouse-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
	openlogger "github.com/TheGrimmChester/open-logger-go"

	"github.com/TheGrimmChester/opa-hub/internal/auth"
	"github.com/TheGrimmChester/opa-hub/internal/config"
	"github.com/TheGrimmChester/opa-hub/internal/ingest"
	"github.com/TheGrimmChester/opa-hub/internal/query"
	"github.com/TheGrimmChester/opa-hub/internal/registry"
	"github.com/TheGrimmChester/opa-hub/internal/store"
)

const version = "0.2.0"

// Server is the opa-hub HTTP control plane.
type Server struct {
	cfg     config.Config
	log     *openlogger.Logger
	mux     *http.ServeMux
	reg     *registry.Registry
	writer  *store.Writer
	started time.Time
}

// New builds a fully wired hub server.
func New(cfg config.Config) *Server {
	log := openlogger.New("opa-hub")
	reg := registry.New(cfg.AgentStaleAfter)
	writer := store.NewWriter(openclickhouse.Config{
		URL:      cfg.ClickHouseURL,
		Database: cfg.ClickHouseDatabase,
		Username: cfg.ClickHouseUser,
		Password: cfg.ClickHousePassword,
	})
	s := &Server{
		cfg:     cfg,
		log:     log,
		mux:     http.NewServeMux(),
		reg:     reg,
		writer:  writer,
		started: time.Now().UTC(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	regH := &registry.Handler{Reg: s.reg, EnrollToken: s.cfg.EnrollToken}
	ingH := &ingest.Handler{Reg: s.reg, Writer: s.writer, EnrollToken: s.cfg.EnrollToken}
	authH := auth.New(s.cfg.JWTSecret, s.cfg.AuthRequired, s.cfg.OPAPublicURL)
	queryH := &query.Handler{
		Reg:       s.reg,
		Writer:    s.writer,
		StartedAt: s.started,
		Version:   version,
	}

	s.mux.HandleFunc("/api/health", s.handleHealth)

	s.mux.HandleFunc("/api/agents/register", regH.ServeRegister)
	s.mux.HandleFunc("/api/agents/heartbeat", regH.ServeHeartbeat)
	s.mux.HandleFunc("/api/agents", regH.ServeList)
	s.mux.HandleFunc("/api/agents/", regH.ServeGet)

	s.mux.HandleFunc("/api/ingest/push", ingH.ServePush)

	s.mux.HandleFunc("/api/auth/login", authH.ServeLogin)
	s.mux.HandleFunc("/api/auth/register", authH.ServeRegister)
	s.mux.HandleFunc("/api/auth/logout", authH.ServeLogout)
	s.mux.HandleFunc("/api/auth/status", authH.ServeStatus)

	s.mux.HandleFunc("/api/query", queryH.ServeQueryRoot)
	s.mux.HandleFunc("/api/admin", queryH.ServeAdmin)
	s.mux.HandleFunc("/api/services", queryH.ServeServicesSkeleton)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"service":    "opa-hub",
		"version":    version,
		"agents":     s.reg.Count(),
		"clickhouse": chOK,
		"topology":   "hub-spoke",
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
