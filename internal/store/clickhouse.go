package store

import (
	"fmt"
	"sync"
	"sync/atomic"

	openclickhouse "github.com/TheGrimmChester/open-clickhouse-go"
)

// Writer wraps Open-ClickHouse-Go for central hub persistence.
// Full INSERT/batch paths land as query ownership moves from OPA-Agent;
// this package owns the dial/config surface and write accounting hooks.
type Writer struct {
	client *openclickhouse.Client
	cfg    openclickhouse.Config

	mu           sync.Mutex
	batches      uint64
	lastErr      string
	ingestBytes  uint64
	ingestEvents uint64
}

// NewWriter constructs a ClickHouse writer from Open-ClickHouse-Go config.
func NewWriter(cfg openclickhouse.Config) *Writer {
	return &Writer{
		client: openclickhouse.New(cfg),
		cfg:    cfg,
	}
}

// Ping checks connectivity via the shared module client.
func (w *Writer) Ping() error {
	if w == nil || w.client == nil {
		return fmt.Errorf("clickhouse writer not configured")
	}
	return w.client.Ping()
}

// EnsureDatabase creates the configured product database when missing.
func (w *Writer) EnsureDatabase() error {
	if w == nil || w.client == nil {
		return fmt.Errorf("clickhouse writer not configured")
	}
	return w.client.EnsureDatabase()
}

// Config returns the underlying ClickHouse settings.
func (w *Writer) Config() openclickhouse.Config {
	if w == nil {
		return openclickhouse.Config{}
	}
	return w.cfg
}

// RecordIngest accounts for a successful edge push batch (hook for CH INSERT).
// When Open-ClickHouse-Go gains InsertJSONEachRow, call it here.
func (w *Writer) RecordIngest(table string, eventCount int, payloadBytes int) {
	if w == nil {
		return
	}
	atomic.AddUint64(&w.batches, 1)
	atomic.AddUint64(&w.ingestEvents, uint64(eventCount))
	atomic.AddUint64(&w.ingestBytes, uint64(payloadBytes))
	_ = table // reserved for table-scoped INSERT routing
}

// SetLastError records the most recent write failure for admin/health surfaces.
func (w *Writer) SetLastError(err error) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		w.lastErr = ""
		return
	}
	w.lastErr = err.Error()
}

// Stats returns write-hook counters for /api/admin and health extensions.
func (w *Writer) Stats() map[string]any {
	if w == nil {
		return map[string]any{"configured": false}
	}
	w.mu.Lock()
	lastErr := w.lastErr
	w.mu.Unlock()
	return map[string]any{
		"configured":    w.cfg.URL != "",
		"url":           w.cfg.URL,
		"database":      w.cfg.Database,
		"batches":       atomic.LoadUint64(&w.batches),
		"ingest_events": atomic.LoadUint64(&w.ingestEvents),
		"ingest_bytes":  atomic.LoadUint64(&w.ingestBytes),
		"last_error":    lastErr,
	}
}
