package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

func (ex *execution) execLLM(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	if ex.e.LLM == nil {
		return stepOutcome{err: fmt.Errorf("no language models are configured")}
	}
	if err := ex.checkBudget(); err != nil {
		return stepOutcome{err: err}
	}

	instructions := ""
	if st.Prompt != "" {
		b, err := os.ReadFile(filepath.Join(ex.a.Dir, filepath.FromSlash(st.Prompt)))
		if err != nil {
			return stepOutcome{err: fmt.Errorf("prompt %s: %w", st.Prompt, err)}
		}
		instructions = string(b)
	} else {
		v, err := expr.String(st.PromptText, env)
		if err != nil {
			return stepOutcome{err: err}
		}
		instructions = expr.Text(v)
	}

	// With an input, the prompt is the instruction and the input is the
	// message; without one, the prompt is the message.
	system, user := strings.TrimSpace(st.System), instructions
	if st.Input != nil {
		v, err := expr.Value(st.Input, env)
		if err != nil {
			return stepOutcome{err: err}
		}
		system = strings.TrimSpace(joinNonEmpty(instructions, st.System))
		user = inputText(v)
	}

	req := llm.Request{Model: st.Model, System: system, User: user, Schema: st.OutputSchema, Temperature: st.Temperature}
	model := st.Model
	if model == "" {
		model = llm.DefaultModel
	}
	input := map[string]any{"model": model, "system": system, "user": user}

	res, err := ex.e.LLM.Complete(ctx, req)
	for _, c := range res.Calls {
		ex.recordCall(path, c)
	}
	o := stepOutcome{input: input, cost: res.CostEur}
	if res.Value != nil {
		o.output = res.Value
	} else if res.Text != "" {
		o.output = res.Text
	}
	if berr := ex.addCost(res.CostEur); berr != nil && err == nil {
		err = berr
	}
	o.err = err
	return o
}

func (ex *execution) recordCall(path string, c llm.Call) {
	reqJSON, _ := json.Marshal(c.Request)
	call := store.LLMCall{
		RunID: ex.run.ID, StepPath: path, Provider: c.Provider, Model: c.Model,
		Request:     json.RawMessage(ex.resolver.Redact(string(reqJSON))),
		Response:    ex.resolver.Redact(c.Response),
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens,
		CostEur: c.CostEur, PriceKnown: c.PriceKnown, LatencyMs: c.Latency.Milliseconds(),
		CreatedAt: time.Now(),
	}
	if c.Err != nil {
		call.Error = ex.resolver.Redact(c.Err.Error())
	}
	ex.record(func(ctx context.Context) error { return ex.e.Store.AddLLMCall(ctx, call) })
}

func inputText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func joinNonEmpty(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, strings.TrimSpace(p))
		}
	}
	return strings.Join(keep, "\n\n")
}
