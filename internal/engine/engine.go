// Package engine executes automation runs: it walks the steps, evaluates
// conditions and expressions, applies retries, timeouts and the spending
// budget, and records everything in the store.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/runner"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// DefaultRunTimeout applies when an automation sets no budget.timeout.
const DefaultRunTimeout = 30 * time.Minute

// Default step timeouts, when a step sets none.
var defaultStepTimeout = map[string]time.Duration{
	manifest.StepHTTP:  30 * time.Second,
	manifest.StepShell: 10 * time.Minute,
	manifest.StepLLM:   5 * time.Minute,
}

// Automation is a loaded, valid automation.
type Automation struct {
	Name     string
	Dir      string
	Hash     string
	Manifest *manifest.Manifest
}

// Workspace returns the automation's workspace, "default" when unset.
func (a Automation) Workspace() string {
	if a.Manifest != nil && a.Manifest.Metadata.Workspace != "" {
		return a.Manifest.Metadata.Workspace
	}
	return store.DefaultWorkspace
}

// Notifier is told about every finished run.
type Notifier interface {
	Notify(ctx context.Context, a Automation, run *store.Run)
}

// Engine runs automations.
type Engine struct {
	Store    *store.Store
	LLM      *llm.Gateway
	Secrets  secrets.Provider
	HTTP     *http.Client
	Notifier Notifier
	// Shell runs shell steps; nil runs them as local child processes.
	Shell runner.Executor
	// SecretsFor returns the secrets a run may use; nil means Secrets for
	// every run. It lets each workspace keep its own secrets.
	SecretsFor func(a Automation, runID string) secrets.Provider
	Log        *slog.Logger
	Now        func() time.Time
}

// Outcome summarises a finished run.
type Outcome struct {
	Status    string
	Error     string
	Anomalies int
	CostEur   float64
}

var (
	errBudget      = errors.New("budget exceeded")
	errUnsupported = errors.New("not available yet")
)

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func (e *Engine) httpClient() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return http.DefaultClient
}

// execution is the state of one run.
type execution struct {
	e         *Engine
	run       *store.Run
	a         Automation
	resolver  *secrets.Resolver
	mu        sync.Mutex
	seq       int
	cost      float64
	maxCost   float64
	anomalies int
}

// Execute runs a, recording progress under run, which must already be in
// the running state. ctx cancellation (shutdown) marks the run cancelled.
func (e *Engine) Execute(ctx context.Context, run *store.Run, a Automation) Outcome {
	m := a.Manifest
	timeout := DefaultRunTimeout
	if m.Budget != nil && m.Budget.Timeout != "" {
		if d, err := time.ParseDuration(m.Budget.Timeout); err == nil {
			timeout = d
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	provider := e.Secrets
	if e.SecretsFor != nil {
		provider = e.SecretsFor(a, run.ID)
	}
	ex := &execution{e: e, run: run, a: a, resolver: secrets.NewResolver(provider)}
	if m.Budget != nil {
		ex.maxCost = m.Budget.MaxCostEur
	}

	start := e.now()
	env := expr.Env{
		"steps":   map[string]any{},
		"trigger": triggerData(run),
		"date":    runDate(run, m, start),
		"now":     start.UTC().Format(time.RFC3339),
		"automation": map[string]any{
			"name": m.Metadata.Name, "description": m.Metadata.Description,
			"workspace": m.Metadata.Workspace, "runId": run.ID,
		},
	}
	e.log().Info("run started", "automation", a.Name, "run", run.ID, "trigger", run.TriggerType)

	err := ex.runSteps(runCtx, m.Steps, env, "")

	out := Outcome{Status: store.RunSucceeded, Anomalies: ex.anomalies, CostEur: ex.cost}
	switch {
	case err == nil:
	case ctx.Err() != nil:
		out.Status, out.Error = store.RunCancelled, "cancelled: Pendolo was shutting down"
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		out.Status, out.Error = store.RunFailed, fmt.Sprintf("run timed out after %s (budget.timeout)", timeout)
	default:
		out.Status, out.Error = store.RunFailed, ex.resolver.Redact(err.Error())
	}

	// Record with a fresh context: the run's own may be cancelled.
	rec, cancelRec := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRec()
	if ferr := e.Store.Finish(rec, run.ID, out.Status, out.Error, out.Anomalies, out.CostEur, e.now()); ferr != nil {
		e.log().Error("record run outcome", "run", run.ID, "err", ferr)
	}
	e.log().Info("run finished", "automation", a.Name, "run", run.ID, "status", out.Status,
		"anomalies", out.Anomalies, "costEur", out.CostEur, "error", out.Error)

	if e.Notifier != nil {
		if final, gerr := e.Store.GetRun(rec, run.ID); gerr == nil {
			e.Notifier.Notify(rec, a, final)
		}
	}
	return out
}

func triggerData(run *store.Run) map[string]any {
	data := map[string]any{}
	if len(run.TriggerData) > 0 {
		_ = json.Unmarshal(run.TriggerData, &data)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = run.TriggerType
	if run.ScheduledFor != nil {
		data["scheduledFor"] = run.ScheduledFor.UTC().Format(time.RFC3339)
	}
	return data
}

// runDate is the run's day in the automation's time zone, so a run
// scheduled for 00:30 in Rome is dated that day, not the UTC day before.
func runDate(run *store.Run, m *manifest.Manifest, now time.Time) string {
	t := now
	if run.ScheduledFor != nil {
		t = *run.ScheduledFor
	}
	for _, tr := range m.Triggers {
		if tr.Schedule != nil && tr.Schedule.Timezone != "" {
			if loc, err := time.LoadLocation(tr.Schedule.Timezone); err == nil {
				t = t.In(loc)
			}
			break
		}
	}
	return t.Format("2006-01-02")
}

func (ex *execution) nextSeq() int {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	ex.seq++
	return ex.seq
}

func (ex *execution) addCost(c float64) error {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	ex.cost += c
	if ex.maxCost > 0 && ex.cost > ex.maxCost {
		return fmt.Errorf("%w: spent €%.4f of €%.4f (budget.maxCostEur)", errBudget, ex.cost, ex.maxCost)
	}
	return nil
}

func (ex *execution) checkBudget() error {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.maxCost > 0 && ex.cost >= ex.maxCost {
		return fmt.Errorf("%w: spent €%.4f of €%.4f (budget.maxCostEur)", errBudget, ex.cost, ex.maxCost)
	}
	return nil
}

// stepOutcome is what one step attempt produces.
type stepOutcome struct {
	input  any
	output any
	logs   string
	cost   float64
	err    error
}

func (ex *execution) runSteps(ctx context.Context, steps []manifest.Step, env expr.Env, prefix string) error {
	results := env["steps"].(map[string]any)
	for i := range steps {
		st := &steps[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		path := prefix + st.ID
		seq := ex.nextSeq()
		ex.record(func(c context.Context) error {
			return ex.e.Store.StartStep(c, ex.run.ID, seq, path, st.ID, st.Type, ex.e.now())
		})

		if st.If != "" {
			ok, err := expr.Condition(st.If, env)
			if err != nil {
				ex.finishStep(seq, store.StepFailed, 0, stepOutcome{err: err})
				results[st.ID] = resultMap(nil, store.StepFailed, err)
				ex.countAnomaly()
				if st.ContinueOnError {
					continue
				}
				return fmt.Errorf("step %s: %w", path, err)
			}
			if !ok {
				ex.finishStep(seq, store.StepSkipped, 0, stepOutcome{})
				results[st.ID] = resultMap(nil, store.StepSkipped, nil)
				continue
			}
		}

		o, attempts := ex.runWithRetry(ctx, st, env, path)
		status := store.StepSucceeded
		if o.err != nil {
			status = store.StepFailed
		}
		ex.finishStep(seq, status, attempts, o)
		results[st.ID] = resultMap(o.output, status, o.err)

		if o.err != nil {
			ex.countAnomaly()
			if errors.Is(o.err, errBudget) || ctx.Err() != nil || !st.ContinueOnError {
				return fmt.Errorf("step %s: %w", path, o.err)
			}
		}
	}
	return nil
}

func (ex *execution) countAnomaly() {
	ex.mu.Lock()
	ex.anomalies++
	ex.mu.Unlock()
}

func resultMap(output any, status string, err error) map[string]any {
	r := map[string]any{
		"output":    output,
		"status":    status,
		"succeeded": status == store.StepSucceeded,
		"failed":    status == store.StepFailed,
		"skipped":   status == store.StepSkipped,
		"error":     "",
	}
	if err != nil {
		r["error"] = err.Error()
	}
	return r
}

func (ex *execution) runWithRetry(ctx context.Context, st *manifest.Step, env expr.Env, path string) (stepOutcome, int) {
	maxAttempts, backoff := 1, 5*time.Second
	if st.Retry != nil {
		maxAttempts += st.Retry.Max
		if st.Retry.Backoff != "" {
			if d, err := time.ParseDuration(st.Retry.Backoff); err == nil {
				backoff = d
			}
		}
	}
	var (
		o    stepOutcome
		logs string
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			wait := backoff * time.Duration(1<<min(attempt-2, 6))
			wait = min(wait, 5*time.Minute)
			select {
			case <-ctx.Done():
				o.err = ctx.Err()
				return o, attempt - 1
			case <-time.After(wait):
			}
		}
		o = ex.attempt(ctx, st, env, path)
		if maxAttempts > 1 && o.logs != "" {
			logs += "--- attempt " + strconv.Itoa(attempt) + " ---\n"
		}
		logs += o.logs
		if o.err == nil || ctx.Err() != nil || errors.Is(o.err, errBudget) || errors.Is(o.err, errUnsupported) {
			o.logs = logs
			return o, attempt
		}
		ex.e.log().Warn("step attempt failed", "run", ex.run.ID, "step", path, "attempt", attempt, "err", ex.resolver.Redact(o.err.Error()))
	}
	o.logs = logs
	return o, maxAttempts
}

func (ex *execution) attempt(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	timeout := defaultStepTimeout[st.Type]
	if st.Timeout != "" {
		if d, err := time.ParseDuration(st.Timeout); err == nil {
			timeout = d
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var o stepOutcome
	switch st.Type {
	case manifest.StepHTTP:
		o = ex.execHTTP(ctx, st, env)
	case manifest.StepShell:
		o = ex.execShell(ctx, st, env, path)
	case manifest.StepLLM:
		o = ex.execLLM(ctx, st, env, path)
	case manifest.StepForeach:
		o = ex.execForeach(ctx, st, env, path)
	default:
		o.err = fmt.Errorf("step type %q is %w: it arrives with the connectors in phase 3", st.Type, errUnsupported)
	}
	if o.err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && timeout > 0 {
		o.err = fmt.Errorf("timed out after %s: %w", timeout, o.err)
	}
	return o
}

func (ex *execution) execForeach(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	v, err := expr.String(st.Items, env)
	if err != nil {
		return stepOutcome{err: err}
	}
	var items []any
	switch t := v.(type) {
	case nil:
	case []any:
		items = t
	default:
		return stepOutcome{err: fmt.Errorf("items must be a list, got %T", v)}
	}
	concurrency := max(st.Concurrency, 1)
	input := map[string]any{"items": len(items), "concurrency": concurrency}

	// Each item gets its own copy of the environment, made before any item
	// starts, so items running in parallel never share a map they write.
	envs := make([]expr.Env, len(items))
	for i, item := range items {
		child := expr.Env{}
		for k, x := range env {
			child[k] = x
		}
		nested := map[string]any{}
		for k, x := range env["steps"].(map[string]any) {
			nested[k] = x
		}
		child["steps"], child["item"], child["index"] = nested, item, i
		envs[i] = child
	}

	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	outputs := make([]any, len(items))
	var (
		errMu    sync.Mutex
		firstErr error // the failure that stopped the loop, not the items it cancelled
	)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range items {
		select {
		case sem <- struct{}{}:
		case <-loopCtx.Done():
		}
		if loopCtx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			err := ex.runSteps(loopCtx, st.Steps, envs[i], path+"["+strconv.Itoa(i)+"].")
			nested := envs[i]["steps"].(map[string]any)
			iteration := map[string]any{}
			for _, s := range st.Steps {
				if r, ok := nested[s.ID].(map[string]any); ok {
					iteration[s.ID] = r["output"]
				}
			}
			outputs[i] = iteration
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("item %d: %w", i, err)
				}
				errMu.Unlock()
				stop() // the first failure stops the other items
			}
		}(i)
	}
	wg.Wait()

	if firstErr != nil {
		return stepOutcome{input: input, output: outputs, err: firstErr}
	}
	if err := ctx.Err(); err != nil {
		return stepOutcome{input: input, output: outputs, err: err}
	}
	return stepOutcome{input: input, output: outputs}
}

func (ex *execution) finishStep(seq int, status string, attempts int, o stepOutcome) {
	errMsg := ""
	if o.err != nil {
		errMsg = ex.resolver.Redact(o.err.Error())
	}
	res := store.StepResult{
		Status: status, Attempts: attempts,
		Input: ex.redactValue(o.input), Output: ex.redactValue(o.output),
		Error: errMsg, Logs: ex.resolver.Redact(o.logs), CostEur: o.cost,
	}
	ex.record(func(c context.Context) error {
		return ex.e.Store.FinishStep(c, ex.run.ID, seq, res, ex.e.now())
	})
}

// redactValue masks secret values anywhere inside v.
func (ex *execution) redactValue(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<unencodable: %v>", err)
	}
	red := ex.resolver.Redact(string(b))
	if red == string(b) {
		return json.RawMessage(b)
	}
	return json.RawMessage(red)
}

// record writes to the store with its own deadline; a failed write is
// logged, never fatal to the run.
func (ex *execution) record(fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		ex.e.log().Error("record step", "run", ex.run.ID, "err", err)
	}
}
