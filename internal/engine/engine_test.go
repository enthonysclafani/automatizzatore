package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

type fakeLLM struct {
	answers []string
	calls   atomic.Int32
	got     []llm.ProviderRequest
}

func (f *fakeLLM) Complete(_ context.Context, req llm.ProviderRequest) (llm.ProviderResponse, error) {
	i := int(f.calls.Add(1)) - 1
	f.got = append(f.got, req)
	a := f.answers[min(i, len(f.answers)-1)]
	return llm.ProviderResponse{Text: a, InputTokens: 1_000_000, OutputTokens: 0}, nil
}

type harness struct {
	t     *testing.T
	store *store.Store
	eng   *Engine
	llm   *fakeLLM
	dir   string
}

func newHarness(t *testing.T, answers ...string) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if len(answers) == 0 {
		answers = []string{"ok"}
	}
	f := &fakeLLM{answers: answers}
	gw := llm.NewWith(map[string]llm.Provider{"fake": f}, map[string]config.Model{
		"smart": {Provider: "fake", Model: "fake-1", Price: config.Price{Input: 0.10}}, // €0.10 per call
	})
	return &harness{
		t: t, store: st, llm: f, dir: t.TempDir(),
		eng: &Engine{Store: st, LLM: gw, Secrets: secrets.Static{"token": "s3cr3t-value", "hook": "unused"}},
	}
}

// run writes the manifest (and files) into a folder, validates it and
// executes it once.
func (h *harness) run(yaml string, files map[string]string) (Outcome, *store.Run, []store.Step) {
	h.t.Helper()
	for name, body := range files {
		p := filepath.Join(h.dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
	path := filepath.Join(h.dir, manifest.FileName)
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		h.t.Fatal(err)
	}
	res, err := manifest.ValidateFile(path)
	if err != nil || res.HasErrors() {
		h.t.Fatalf("invalid test manifest: %v %v", err, res.Problems)
	}
	ctx := context.Background()
	run, err := h.store.StartNow(ctx, store.NewRun{Automation: "t", TriggerType: "manual"}, "test", time.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	out := h.eng.Execute(ctx, run, Automation{Name: "t", Dir: h.dir, Manifest: res.Manifest})
	final, _ := h.store.GetRun(ctx, run.ID)
	steps, _ := h.store.ListSteps(ctx, run.ID)
	return out, final, steps
}

const head = `apiVersion: pendolo/v1
kind: Automation
metadata: { name: t }
triggers: [ { manual: {} } ]
budget: { maxCostEur: 1 }
steps:
`

func stepByPath(steps []store.Step, path string) store.Step {
	for _, s := range steps {
		if s.Path == path {
			return s
		}
	}
	return store.Step{}
}

func TestHTTPAndShellPipeline(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"items":["a","b"],"healthy":true}`)
	}))
	defer srv.Close()

	h := newHarness(t)
	out, run, steps := h.run(head+`
  - id: fetch
    type: http
    request:
      url: "`+srv.URL+`/status"
      headers: { Authorization: "secret://token" }
    expect: { status: [200], bodyContains: healthy }
  - id: echo
    type: shell
    run: 'echo "first=$FIRST token=$TOKEN"; echo "{\"n\": 2}" >&2'
    env:
      FIRST: "${steps.fetch.output.json.items[0]}"
      TOKEN: "secret://token"
  - id: json
    type: shell
    run: 'echo "{\"count\": $#}"'
    args: ["${steps.fetch.output.status}", "x"]
`, nil)

	if out.Status != store.RunSucceeded || run.Status != store.RunSucceeded {
		t.Fatalf("outcome %+v run %+v", out, run)
	}
	if gotAuth.Load() != "s3cr3t-value" {
		t.Fatalf("secret not sent as header: %v", gotAuth.Load())
	}
	echo := stepByPath(steps, "echo")
	if !strings.Contains(echo.Logs, "first=a") {
		t.Fatalf("expression not passed through env: %q", echo.Logs)
	}
	if strings.Contains(echo.Logs, "s3cr3t-value") || !strings.Contains(echo.Logs, "[secret:token]") {
		t.Fatalf("secret not redacted in logs: %q", echo.Logs)
	}
	if strings.Contains(string(echo.Output), "s3cr3t-value") {
		t.Fatalf("secret not redacted in output: %s", echo.Output)
	}
	var jsonOut map[string]any
	_ = json.Unmarshal(stepByPath(steps, "json").Output, &jsonOut)
	if j, _ := jsonOut["json"].(map[string]any); j["count"] != float64(2) {
		t.Fatalf("args or JSON output wrong: %s", stepByPath(steps, "json").Output)
	}
}

func TestShellEnvIsolated(t *testing.T) {
	t.Setenv("PENDOLO_SECRET_LEAK", "do-not-leak")
	h := newHarness(t)
	_, _, steps := h.run(head+`
  - id: env
    type: shell
    run: env
`, nil)
	logs := stepByPath(steps, "env").Logs
	if strings.Contains(logs, "do-not-leak") || !strings.Contains(logs, "PENDOLO_STEP=env") {
		t.Fatalf("shell environment not isolated:\n%s", logs)
	}
}

func TestConditionsFailuresAndRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, "fine")
	}))
	defer srv.Close()

	h := newHarness(t)
	out, run, steps := h.run(head+`
  - id: flaky
    type: http
    request: { url: "`+srv.URL+`" }
    retry: { max: 2, backoff: 10ms }
  - id: broken
    type: shell
    script: scripts/fail.sh
    continueOnError: true
  - id: on_failure
    type: shell
    if: "${steps.broken.failed}"
    run: echo handled
  - id: on_success
    type: shell
    if: "${steps.flaky.failed}"
    run: echo never
`, map[string]string{"scripts/fail.sh": "#!/bin/sh\necho boom >&2\nexit 3\n"})

	if out.Status != store.RunSucceeded || out.Anomalies != 1 || run.Anomalies != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if s := stepByPath(steps, "flaky"); s.Attempts != 3 || s.Status != store.StepSucceeded {
		t.Fatalf("retry: %+v", s)
	}
	if s := stepByPath(steps, "broken"); s.Status != store.StepFailed || !strings.Contains(s.Error, "exit status 3: boom") {
		t.Fatalf("broken: %+v", s)
	}
	if s := stepByPath(steps, "on_failure"); s.Status != store.StepSucceeded {
		t.Fatalf("on_failure: %+v", s)
	}
	if s := stepByPath(steps, "on_success"); s.Status != store.StepSkipped {
		t.Fatalf("on_success: %+v", s)
	}
}

func TestFailureStopsRun(t *testing.T) {
	h := newHarness(t)
	out, run, steps := h.run(head+`
  - id: a
    type: shell
    run: exit 1
  - id: b
    type: shell
    run: echo unreachable
`, nil)
	if out.Status != store.RunFailed || !strings.Contains(run.Error, "step a") || len(steps) != 1 {
		t.Fatalf("outcome %+v run %+v steps %d", out, run, len(steps))
	}
}

func TestStepTimeoutKillsProcessTree(t *testing.T) {
	h := newHarness(t)
	start := time.Now()
	out, run, _ := h.run(head+`
  - id: slow
    type: shell
    run: sleep 30 & wait
    timeout: 300ms
`, nil)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timeout did not stop the step quickly: %s", time.Since(start))
	}
	if out.Status != store.RunFailed || !strings.Contains(run.Error, "timed out") {
		t.Fatalf("outcome %+v error %q", out, run.Error)
	}
}

func TestLLMStepAndBudget(t *testing.T) {
	h := newHarness(t, `{"category":"client","needsReply":true}`)
	out, run, steps := h.run(head+`
  - id: classify
    type: llm
    prompt: prompts/classify.md
    input: { subject: "Invoice ${automation.name}" }
    outputSchema:
      type: object
      required: [category]
      properties: { category: { type: string }, needsReply: { type: boolean } }
  - id: use
    type: shell
    if: "${steps.classify.output.needsReply}"
    run: echo "$CAT"
    env: { CAT: "${steps.classify.output.category}" }
`, map[string]string{"prompts/classify.md": "Classify the email."})

	if out.Status != store.RunSucceeded || out.CostEur != 0.10 || run.CostEur != 0.10 {
		t.Fatalf("outcome %+v", out)
	}
	if h.llm.got[0].System != "Classify the email." || !strings.Contains(h.llm.got[0].User, "Invoice t") {
		t.Fatalf("prompt layout wrong: %+v", h.llm.got[0])
	}
	if s := stepByPath(steps, "use"); !strings.Contains(s.Logs, "client") {
		t.Fatalf("structured output not usable: %+v", s)
	}
	calls, _ := h.store.ListLLMCalls(context.Background(), run.ID)
	if len(calls) != 1 || calls[0].CostEur != 0.10 || calls[0].StepPath != "classify" {
		t.Fatalf("calls = %+v", calls)
	}

	// €0.10 per call against a €0.15 budget: the second call breaks it.
	h2 := newHarness(t, "fine")
	out2, run2, steps2 := h2.run(strings.Replace(head, "maxCostEur: 1", "maxCostEur: 0.15", 1)+`
  - { id: one, type: llm, promptText: hi }
  - { id: two, type: llm, promptText: hi, continueOnError: true }
  - { id: three, type: llm, promptText: hi }
`, nil)
	if out2.Status != store.RunFailed || !strings.Contains(run2.Error, "budget exceeded") || len(steps2) != 2 {
		t.Fatalf("budget not enforced: %+v %q steps=%d", out2, run2.Error, len(steps2))
	}
}

func TestForeach(t *testing.T) {
	h := newHarness(t)
	out, _, steps := h.run(head+`
  - id: list
    type: shell
    run: echo '["x","y","z"]'
  - id: each
    type: foreach
    items: "${steps.list.output.json}"
    steps:
      - id: say
        type: shell
        run: echo "$I-$V"
        env: { I: "${index}", V: "${item}" }
  - id: after
    type: shell
    run: echo "$N"
    env: { N: "${len(steps.each.output)}" }
`, nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("outcome %+v", out)
	}
	if s := stepByPath(steps, "each[2].say"); !strings.Contains(s.Logs, "2-z") {
		t.Fatalf("third iteration: %+v", s)
	}
	if s := stepByPath(steps, "after"); strings.TrimSpace(s.Logs) != "3" {
		t.Fatalf("foreach output: %q", s.Logs)
	}
}

func TestUnsupportedStepFailsClearly(t *testing.T) {
	h := newHarness(t)
	out, run, _ := h.run(head+`
  - id: label
    type: connector
    action: gmail.apply_labels
    retry: { max: 3, backoff: 1s }
`, nil)
	if out.Status != store.RunFailed || !strings.Contains(run.Error, "not available") {
		t.Fatalf("outcome %+v %q", out, run.Error)
	}
}

func TestRunTimeout(t *testing.T) {
	h := newHarness(t)
	out, run, _ := h.run(strings.Replace(head, "budget: { maxCostEur: 1 }", "budget: { timeout: 300ms }", 1)+`
  - id: slow
    type: shell
    run: sleep 5
`, nil)
	if out.Status != store.RunFailed || !strings.Contains(run.Error, "run timed out") {
		t.Fatalf("outcome %+v %q", out, run.Error)
	}
}

func TestCancelledOnShutdown(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.dir, manifest.FileName)
	_ = os.WriteFile(path, []byte(head+"  - { id: slow, type: shell, run: sleep 5 }\n"), 0o644)
	res, _ := manifest.ValidateFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	run, _ := h.store.StartNow(ctx, store.NewRun{Automation: "t", TriggerType: "manual"}, "test", time.Now())
	time.AfterFunc(200*time.Millisecond, cancel)
	out := h.eng.Execute(ctx, run, Automation{Name: "t", Dir: h.dir, Manifest: res.Manifest})
	if out.Status != store.RunCancelled {
		t.Fatalf("outcome %+v", out)
	}
}

func TestForeachConcurrency(t *testing.T) {
	h := newHarness(t)
	start := time.Now()
	out, _, steps := h.run(head+`
  - id: each
    type: foreach
    items: "${[1, 2, 3, 4]}"
    concurrency: 4
    steps:
      - { id: nap, type: shell, run: "sleep 0.4; echo $N", env: { N: "${item}" } }
`, nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("outcome %+v", out)
	}
	if took := time.Since(start); took > 1200*time.Millisecond {
		t.Fatalf("4 items of 0.4s with concurrency 4 took %s", took)
	}
	if s := stepByPath(steps, "each[3].nap"); strings.TrimSpace(s.Logs) != "4" {
		t.Fatalf("item 3: %+v", s)
	}

	h2 := newHarness(t)
	out2, run2, steps2 := h2.run(head+`
  - id: each
    type: foreach
    items: "${[1, 2, 3, 4, 5, 6]}"
    concurrency: 2
    steps:
      - { id: check, type: shell, run: "test \"$N\" != 2", env: { N: "${item}" } }
`, nil)
	if out2.Status != store.RunFailed || !strings.Contains(run2.Error, "item 1") {
		t.Fatalf("outcome %+v %q", out2, run2.Error)
	}
	if len(steps2) >= 7 {
		t.Fatalf("a failure did not stop the remaining items: %d steps recorded", len(steps2))
	}
}

// Data that reads "secret://name" must never be turned into the secret.
func TestSecretsAreNotResolvedFromData(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store(r.URL.Query().Get("q") + "|" + string(b))
	}))
	defer srv.Close()
	h := newHarness(t)
	out, _, steps := h.run(head+`
  - id: evil
    type: shell
    run: 'echo "secret://token"'
  - id: post
    type: http
    request:
      method: POST
      url: "`+srv.URL+`/?q=${trim(steps.evil.output.stdout)}"
      body: { text: "${steps.evil.output.stdout}", literal: "secret://token" }
  - id: env
    type: shell
    run: 'echo "$FROM_DATA"'
    env: { FROM_DATA: "${steps.evil.output.stdout}" }
`, nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("outcome %+v", out)
	}
	sent := got.Load().(string)
	if strings.Count(sent, "s3cr3t-value") != 1 || !strings.Contains(sent, `"text":"secret://token`) {
		t.Fatalf("request = %q: only the literal reference may become the secret", sent)
	}
	if s := stepByPath(steps, "env"); !strings.Contains(s.Logs, "secret://token") {
		t.Fatalf("env from data resolved a secret: %q", s.Logs)
	}
}
