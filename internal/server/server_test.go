package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/runner"
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

func TestWebhookTrigger(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("PENDOLO_SECRET_HOOK", "hook-secret")
	writeAutomation(t, cfg.AutomationsDir, "deploy", `apiVersion: pendolo/v1
kind: Automation
metadata: { name: deploy }
triggers: [ { webhook: { secret: "secret://hook" } } ]
steps:
  - id: echo
    type: shell
    run: echo "ref=$REF"
    env: { REF: "${trigger.json.ref}" }
`)
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.store.Close()
	if err := srv.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := srv.routes()
	post := func(path, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	body := `{"ref":"refs/heads/main"}`
	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	cases := []struct {
		name    string
		path    string
		headers map[string]string
		want    int
	}{
		{"no credentials", "/hooks/deploy", nil, http.StatusUnauthorized},
		{"wrong token", "/hooks/deploy", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"wrong signature", "/hooks/deploy", map[string]string{"X-Hub-Signature-256": "sha256=00"}, http.StatusUnauthorized},
		{"unknown automation", "/hooks/nope", nil, http.StatusNotFound},
		{"token", "/hooks/deploy", map[string]string{"Authorization": "Bearer hook-secret"}, http.StatusAccepted},
		{"github signature", "/hooks/deploy", map[string]string{"X-Hub-Signature-256": sig, "X-GitHub-Event": "push"}, http.StatusAccepted},
	}
	for _, tc := range cases {
		if rec := post(tc.path, body, tc.headers); rec.Code != tc.want {
			t.Errorf("%s: HTTP %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}

	runs, _ := srv.store.ListRuns(context.Background(), store.RunFilter{Automation: "deploy"})
	if len(runs) != 2 || runs[0].TriggerType != "webhook" {
		t.Fatalf("runs = %+v", runs)
	}
	data := string(runs[0].TriggerData)
	if !strings.Contains(data, `"x-github-event":"push"`) || strings.Contains(data, "hook-secret") || strings.Contains(data, sig) {
		t.Fatalf("trigger data: %s", data)
	}

	// The queued run executes with the payload available as trigger.json.
	srv.execute(context.Background(), &runs[0], "test")
	steps, _ := srv.store.ListSteps(context.Background(), runs[0].ID)
	if len(steps) != 1 || !strings.Contains(steps[0].Logs, "ref=refs/heads/main") {
		t.Fatalf("steps = %+v", steps)
	}

	_ = srv.store.SetEnabled(context.Background(), "deploy", false)
	if rec := post("/hooks/deploy", body, map[string]string{"Authorization": "Bearer hook-secret"}); rec.Code != http.StatusConflict {
		t.Fatalf("paused automation: HTTP %d", rec.Code)
	}
}

func TestShellStepsGoThroughTheRunner(t *testing.T) {
	cfg := testConfig(t)
	writeAutomation(t, cfg.AutomationsDir, "where", `apiVersion: pendolo/v1
kind: Automation
metadata: { name: where }
triggers: [ { manual: {} } ]
steps:
  - { id: who, type: shell, run: "echo ran-by-$RUNNER_MARK" }
`)
	// The runner process has its own environment; scripts never see the server's.
	var calls atomic.Int32
	inner := runner.Handler{Root: cfg.AutomationsDir, Token: "0123456789abcdef", Exec: markExec{&calls}}
	rs := httptest.NewServer(inner)
	t.Setenv("PENDOLO_SECRET_RUNNER_TOKEN", "0123456789abcdef")
	cfg.Runner = config.Runner{URL: rs.URL, Token: "secret://runner-token"}

	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.store.Close()
	if err := srv.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, _, _ := srv.store.Enqueue(context.Background(), store.NewRun{Automation: "where", TriggerType: "manual"}, time.Now())
	run, _ := srv.store.Claim(context.Background(), "w", time.Minute, time.Now())
	if run == nil || run.ID != id {
		t.Fatalf("claim = %+v", run)
	}
	srv.execute(context.Background(), run, "w")
	steps, _ := srv.store.ListSteps(context.Background(), id)
	if calls.Load() != 1 || len(steps) != 1 || !strings.Contains(steps[0].Logs, "ran-by-runner") {
		t.Fatalf("calls=%d steps=%+v", calls.Load(), steps)
	}
	if h := srv.health(context.Background()); h.Runner != "ok" {
		t.Fatalf("health with runner up: %+v", h)
	}
	rs.Close()
	if h := srv.health(context.Background()); h.OK || h.Runner != "runner unreachable" {
		t.Fatalf("health with runner down: %+v", h)
	}
}

// markExec runs jobs locally with a marker in the environment, to prove
// they went through the runner.
type markExec struct{ calls *atomic.Int32 }

func (m markExec) Exec(ctx context.Context, job runner.Job) (runner.Result, error) {
	m.calls.Add(1)
	job.Env["RUNNER_MARK"] = "runner"
	return runner.Local{}.Exec(ctx, job)
}
