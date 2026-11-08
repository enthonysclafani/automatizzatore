package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/api"
	"github.com/enthonysclafani/automatizzatore/internal/version"
	"github.com/enthonysclafani/automatizzatore/internal/webui"
)

// Health is the body of /healthz.
type Health = api.Health

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
	if s.api != nil {
		mux.Handle("/api/", s.api.Handler())
	}
	mux.Handle("/", dashboard())
	return securityHeaders(mux)
}

// securityHeaders hardens every response, the dashboard's in particular.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
			"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// dashboard serves the built web app, falling back to index.html for the
// app's own routes.
func dashboard() http.Handler {
	dist, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("The dashboard is not built into this binary. Run `make web` and rebuild, or use the official image.\n"))
		})
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(dist, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
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
