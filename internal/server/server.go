// Package server runs Pendolo as a long-lived process: it loads the
// automations folder, runs the scheduler and the workers, exposes a health
// endpoint, pings an external heartbeat and prunes old history.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/notify"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/runner"
	"github.com/enthonysclafani/automatizzatore/internal/scheduler"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Timings, exported so tests can shorten them.
var (
	TickInterval   = time.Second
	ReloadInterval = 30 * time.Second
	PollInterval   = time.Second
	Lease          = time.Minute
	ShutdownGrace  = 30 * time.Second
	StaleTick      = 30 * time.Second
)

// Server is a running Pendolo instance.
type Server struct {
	cfg     config.Config
	log     *slog.Logger
	store   *store.Store
	engine  *engine.Engine
	sched   *scheduler.Scheduler
	secrets secrets.Provider
	owner   string
	wake    chan struct{}

	runner *runner.Remote

	mu      sync.RWMutex
	snap    registry.Snapshot
	running atomic.Int32
	started time.Time
}

// DBPath is where the database lives inside the data folder.
func DBPath(cfg config.Config) string { return filepath.Join(cfg.DataDir, "pendolo.db") }

// SecretsProvider builds the secret backend from the configuration.
func SecretsProvider(cfg config.Config) secrets.Provider {
	return secrets.FilesAndEnv{Dir: cfg.Secrets.Dir, EnvPrefix: cfg.Secrets.EnvPrefix}
}

// NewEngine builds an engine over st from the configuration. With
// useRunner, shell steps go to the configured runner, if any.
func NewEngine(cfg config.Config, st *store.Store, log *slog.Logger, useRunner bool) (*engine.Engine, error) {
	sec := SecretsProvider(cfg)
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	e := &engine.Engine{
		Store: st, LLM: llm.New(cfg, sec), Secrets: sec, HTTP: httpClient, Log: log,
		Notifier: &notify.Notifier{Secrets: sec, HTTP: httpClient, Log: log, Config: cfg.Notify},
	}
	if useRunner && cfg.Runner.URL != "" {
		r, err := remoteRunner(cfg, sec)
		if err != nil {
			return nil, err
		}
		e.Shell = r
	}
	return e, nil
}

func remoteRunner(cfg config.Config, sec secrets.Provider) (runner.Remote, error) {
	token, err := secrets.NewResolver(sec).String(cfg.Runner.Token)
	if err != nil {
		return runner.Remote{}, fmt.Errorf("runner.token: %w", err)
	}
	return runner.Remote{URL: cfg.Runner.URL, Token: token, Root: cfg.AutomationsDir}, nil
}

// New opens the database and prepares the server.
func New(cfg config.Config, log *slog.Logger) (*Server, error) {
	st, err := store.Open(DBPath(cfg))
	if err != nil {
		return nil, err
	}
	eng, err := NewEngine(cfg, st, log, true)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	s := &Server{
		cfg: cfg, log: log, store: st, secrets: SecretsProvider(cfg),
		engine: eng, owner: ownerID(), wake: make(chan struct{}, 1),
	}
	if r, ok := eng.Shell.(runner.Remote); ok {
		s.runner = &r
	}
	s.sched = &scheduler.Scheduler{Store: st, Owner: s.owner, Automations: s.automations, Wake: s.wake, Log: log}
	return s, nil
}

func ownerID() string {
	host, _ := os.Hostname()
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}

func (s *Server) resolveSecret(ref string) (string, error) {
	return secrets.NewResolver(s.secrets).String(ref)
}

func (s *Server) automations() map[string]engine.Automation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.Valid
}

// Run serves until ctx ends, then shuts down gracefully: no new runs start,
// running ones get ShutdownGrace to finish and are then cancelled.
func (s *Server) Run(ctx context.Context) error {
	defer s.store.Close()
	s.started = time.Now()
	if info, err := os.Stat(s.cfg.AutomationsDir); err != nil || !info.IsDir() {
		return fmt.Errorf("automations folder %q not found: create it or set automationsDir", s.cfg.AutomationsDir)
	}
	if n, err := s.store.RecoverInterrupted(ctx, time.Now()); err != nil {
		return err
	} else if n > 0 {
		s.log.Warn("runs interrupted by the previous stop marked as failed", "count", n)
	}
	if err := s.reload(ctx); err != nil {
		return err
	}

	claimCtx, stopClaiming := context.WithCancel(context.Background())
	runCtx, cancelRuns := context.WithCancel(context.Background())
	defer cancelRuns()

	var bg sync.WaitGroup
	start := func(fn func()) {
		bg.Add(1)
		go func() { defer bg.Done(); fn() }()
	}
	start(func() { s.sched.Run(claimCtx, TickInterval) })
	start(func() { s.every(claimCtx, ReloadInterval, func(c context.Context) { _ = s.reload(c) }) })
	start(func() { s.every(claimCtx, Lease, s.recover) })
	start(func() { s.every(claimCtx, 24*time.Hour, s.prune) })
	if s.cfg.Heartbeat.URL != "" {
		start(func() { s.every(claimCtx, s.cfg.Heartbeat.Interval.Std(), s.heartbeat) })
	}

	var workers sync.WaitGroup
	for i := 0; i < s.cfg.Workers; i++ {
		workers.Add(1)
		go func(n int) { defer workers.Done(); s.worker(claimCtx, runCtx, fmt.Sprintf("w%d", n)) }(i + 1)
	}

	httpSrv := &http.Server{Addr: s.cfg.Server.Listen, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	httpErr := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()
	s.log.Info("pendolo started", "automations", len(s.automations()), "workers", s.cfg.Workers,
		"listen", s.cfg.Server.Listen, "owner", s.owner)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-httpErr:
		runErr = fmt.Errorf("http server: %w", runErr)
	}

	s.log.Info("shutting down", "running", s.running.Load())
	stopClaiming()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(ShutdownGrace):
		s.log.Warn("cancelling runs still in progress", "running", s.running.Load())
		cancelRuns()
		<-done
	}
	bg.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	s.log.Info("stopped")
	return runErr
}

func (s *Server) every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func (s *Server) worker(claimCtx, runCtx context.Context, name string) {
	id := s.owner + "/" + name
	for claimCtx.Err() == nil {
		run, err := s.store.Claim(claimCtx, id, Lease, time.Now())
		if err != nil && claimCtx.Err() == nil {
			s.log.Error("claim run", "err", err)
		}
		if run == nil {
			select {
			case <-claimCtx.Done():
				return
			case <-s.wake:
			case <-time.After(PollInterval):
			}
			continue
		}
		s.execute(runCtx, run, id)
	}
}

func (s *Server) execute(ctx context.Context, run *store.Run, worker string) {
	s.running.Add(1)
	defer s.running.Add(-1)

	a, ok := s.automations()[run.Automation]
	if !ok {
		msg := "automation not found or invalid: run `pendolo validate` on its folder"
		_ = s.store.Finish(context.Background(), run.ID, store.RunFailed, msg, 0, 0, time.Now())
		s.log.Error("run failed before starting", "automation", run.Automation, "run", run.ID, "err", msg)
		return
	}

	leaseCtx, stopLease := context.WithCancel(context.Background())
	defer stopLease()
	go func() {
		t := time.NewTicker(Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-t.C:
				if err := s.store.RenewLease(leaseCtx, run.ID, worker, time.Now().Add(Lease)); err != nil && leaseCtx.Err() == nil {
					s.log.Error("renew lease", "run", run.ID, "err", err)
				}
			}
		}
	}()
	s.engine.Execute(ctx, run, a)
}

// reload re-reads the automations folder and records what it found.
func (s *Server) reload(ctx context.Context) error {
	snap, err := registry.Load(s.cfg.AutomationsDir)
	if err != nil {
		s.log.Error("load automations", "dir", s.cfg.AutomationsDir, "err", err)
		return err
	}
	s.mu.RLock()
	prev := s.snap.Valid
	s.mu.RUnlock()

	now := time.Now()
	for name, a := range snap.Valid {
		if old, ok := prev[name]; !ok || old.Hash != a.Hash {
			s.log.Info("automation loaded", "automation", name, "dir", a.Dir, "version", a.Hash)
		}
		if err := s.store.UpsertAutomation(ctx, store.Automation{Name: name, Path: a.Dir, Hash: a.Hash, Valid: true}, now); err != nil {
			return err
		}
	}
	for _, inv := range snap.Invalid {
		problems, _ := json.Marshal(inv.Problems)
		s.log.Warn("automation invalid, not scheduled", "automation", inv.Name, "dir", inv.Path, "problems", string(problems))
		if err := s.store.UpsertAutomation(ctx, store.Automation{Name: inv.Name, Path: inv.Path, Hash: inv.Hash, Problems: problems}, now); err != nil {
			return err
		}
	}
	if err := s.store.DeleteAutomationsExcept(ctx, snap.Names()); err != nil {
		return err
	}
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
	return nil
}

func (s *Server) recover(ctx context.Context) {
	if n, err := s.store.RecoverInterrupted(ctx, time.Now()); err != nil {
		s.log.Error("recover interrupted runs", "err", err)
	} else if n > 0 {
		s.log.Warn("runs with an expired lease marked as failed", "count", n)
	}
}

func (s *Server) prune(ctx context.Context) {
	keep := s.cfg.Retention.Runs.Std()
	if keep <= 0 {
		return
	}
	if n, err := s.store.DeleteRunsBefore(ctx, time.Now().Add(-keep)); err != nil {
		s.log.Error("prune history", "err", err)
	} else if n > 0 {
		s.log.Info("old runs deleted", "count", n, "olderThan", keep)
	}
}

func (s *Server) heartbeat(ctx context.Context) {
	if h := s.health(ctx); !h.OK {
		s.log.Warn("heartbeat withheld: unhealthy", "reason", h.Reason)
		return
	}
	url, err := secrets.NewResolver(s.secrets).String(s.cfg.Heartbeat.URL)
	if err != nil {
		s.log.Error("heartbeat url", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		s.log.Error("heartbeat url", "err", "invalid URL")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Warn("heartbeat failed", "err", "request failed")
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("heartbeat failed", "status", resp.StatusCode)
	}
}
