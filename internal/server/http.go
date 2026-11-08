package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/version"
)

// Health is the body of /healthz.
type Health struct {
	OK          bool      `json:"ok"`
	Reason      string    `json:"reason,omitempty"`
	Version     string    `json:"version"`
	StartedAt   time.Time `json:"startedAt"`
	LastTick    time.Time `json:"lastTick"`
	Leader      bool      `json:"leader"`
	Automations int       `json:"automations"`
	Invalid     int       `json:"invalid"`
	Running     int32     `json:"running"`
	Queued      int       `json:"queued"`
	Runner      string    `json:"runner"`
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		h := s.health(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if !h.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	mux.HandleFunc("POST /hooks/{name}", s.handleHook)
	return mux
}

func (s *Server) health(ctx context.Context) Health {
	lastTick, leader := s.sched.Status()
	s.mu.RLock()
	h := Health{
		OK: true, Version: version.Version, StartedAt: s.started, LastTick: lastTick, Leader: leader,
		Automations: len(s.snap.Valid), Invalid: len(s.snap.Invalid), Running: s.running.Load(),
	}
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	queued, err := s.store.CountQueued(ctx)
	switch {
	case err != nil:
		h.OK, h.Reason = false, "database: "+err.Error()
	case lastTick.IsZero() && time.Since(s.started) > StaleTick, !lastTick.IsZero() && time.Since(lastTick) > StaleTick:
		h.OK, h.Reason = false, "scheduler stalled"
	}
	h.Queued = queued
	h.Runner = "local"
	if s.runner != nil {
		h.Runner = "ok"
		if rerr := s.runner.Ping(ctx); rerr != nil {
			h.Runner = rerr.Error()
			if h.OK {
				h.OK, h.Reason = false, "runner: "+rerr.Error()
			}
		}
	}
	return h
}
