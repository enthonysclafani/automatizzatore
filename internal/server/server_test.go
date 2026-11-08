package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

func writeAutomation(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "automation.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testConfig(t *testing.T) config.Config {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.AutomationsDir = t.TempDir()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Workers = 2
	cfg.Secrets.Dir = ""
	return cfg
}

func TestServerRunsScheduledAutomations(t *testing.T) {
	cfg := testConfig(t)
	marker := filepath.Join(t.TempDir(), "ran")
	var hook atomicBody
	hookSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hook.set(string(b))
	}))
	defer hookSrv.Close()
	t.Setenv("PENDOLO_SECRET_ALERTS", hookSrv.URL)

	writeAutomation(t, cfg.AutomationsDir, "tick", `apiVersion: pendolo/v1
kind: Automation
metadata: { name: tick }
triggers: [ { schedule: { cron: "@every 1s", timezone: UTC } } ]
steps:
  - id: touch
    type: shell
    run: echo ok >> "$MARKER"
    env: { MARKER: "`+marker+`" }
`)
	writeAutomation(t, cfg.AutomationsDir, "broken", `apiVersion: pendolo/v1
kind: Automation
metadata: { name: broken }
triggers: [ { schedule: { cron: "@every 1s", timezone: UTC } } ]
notifications: { on: failure, channels: ["webhook:secret://alerts"] }
steps:
  - { id: fail, type: shell, run: "exit 4" }
`)
	writeAutomation(t, cfg.AutomationsDir, "invalid", "apiVersion: pendolo/v1\nkind: Nope\n")

	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(marker); strings.Count(string(b), "ok") >= 2 && hook.get() != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var h Health
	_ = json.Unmarshal(rec.Body.Bytes(), &h)
	if rec.Code != http.StatusOK || !h.OK || !h.Leader || h.Automations != 2 || h.Invalid != 1 {
		t.Fatalf("health %d %+v", rec.Code, h)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(marker)
	if strings.Count(string(b), "ok") < 2 {
		t.Fatalf("scheduled automation ran %d times, want at least 2", strings.Count(string(b), "ok"))
	}
	if !strings.Contains(hook.get(), `"status":"failed"`) {
		t.Fatalf("failure notification not sent: %q", hook.get())
	}

	st, err := store.Open(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, _ := st.ListRuns(context.Background(), store.RunFilter{Automation: "tick"})
	for _, r := range runs {
		if r.Status != store.RunSucceeded {
			t.Fatalf("run %s: %s %s", r.ID, r.Status, r.Error)
		}
	}
	autos, _ := st.ListAutomations(context.Background())
	if len(autos) != 3 {
		t.Fatalf("automations recorded: %+v", autos)
	}
}

func TestShutdownCancelsLongRuns(t *testing.T) {
	cfg := testConfig(t)
	writeAutomation(t, cfg.AutomationsDir, "long", `apiVersion: pendolo/v1
kind: Automation
metadata: { name: long }
triggers: [ { schedule: { cron: "@every 1s", timezone: UTC } } ]
steps:
  - { id: wait, type: shell, run: "sleep 30" }
`)
	old := ShutdownGrace
	ShutdownGrace = 500 * time.Millisecond
	defer func() { ShutdownGrace = old }()

	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	for i := 0; i < 100 && srv.running.Load() == 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if srv.running.Load() == 0 {
		t.Fatal("run never started")
	}
	start := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("shutdown took %s", took)
	}
	st, _ := store.Open(DBPath(cfg))
	defer st.Close()
	runs, _ := st.ListRuns(context.Background(), store.RunFilter{Automation: "long", Status: store.RunCancelled})
	if len(runs) != 1 {
		t.Fatalf("cancelled runs = %d, want 1", len(runs))
	}
}

func TestMissingAutomationsFolder(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutomationsDir = filepath.Join(cfg.AutomationsDir, "nope")
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

type atomicBody struct {
	mu sync.Mutex
	v  string
}

func (a *atomicBody) set(s string) { a.mu.Lock(); a.v = s; a.mu.Unlock() }

func (a *atomicBody) get() string { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

func init() {
	// Faster ticks keep the tests short.
	TickInterval = 200 * time.Millisecond
	PollInterval = 100 * time.Millisecond
}
