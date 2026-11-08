// Package llm is the gateway between automations and language models.
//
// Automations name a model alias (fast, smart, local); the configuration
// maps each alias to a provider and a model. Providers are adapters behind
// one interface, so switching provider never touches an automation. The
// gateway also validates structured output, retries once on a bad answer,
// and computes the cost of every call.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
)

// DefaultModel is the alias used when a step names none.
const DefaultModel = "smart"

const defaultMaxTokens = 16000

// ProviderRequest is what an adapter sends to its API.
type ProviderRequest struct {
	Model       string         `json:"model"`
	System      string         `json:"system,omitempty"`
	User        string         `json:"user"`
	Schema      map[string]any `json:"schema,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   int            `json:"maxTokens"`
	Effort      string         `json:"effort,omitempty"`
	Fallbacks   string         `json:"fallbacks,omitempty"`
}

// ProviderResponse is what an adapter returns.
type ProviderResponse struct {
	Text         string
	Model        string
	InputTokens  int64
	OutputTokens int64
}

// Provider is one model API.
type Provider interface {
	Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}

// Request is what a step asks of the gateway.
type Request struct {
	// Model is an alias from the configuration, or "<provider>/<model>".
	Model       string
	System      string
	User        string
	Schema      map[string]any
	Temperature *float64
}

// Call is the record of one request to a provider.
type Call struct {
	Provider     string
	Model        string
	Request      ProviderRequest
	Response     string
	InputTokens  int64
	OutputTokens int64
	CostEur      float64
	PriceKnown   bool
	Latency      time.Duration
	Err          error
}

// Result is the answer to a Request.
type Result struct {
	// Text is the model's answer.
	Text string
	// Value is the parsed answer when a schema was given.
	Value any
	// Calls holds every provider request made, including failed ones.
	Calls   []Call
	CostEur float64
}

// Gateway routes requests to providers.
type Gateway struct {
	providers map[string]Provider
	models    map[string]config.Model
}

// New builds a gateway from the configuration. API keys are resolved here;
// a missing key only fails the calls that need it.
func New(cfg config.Config, sec secrets.Provider) *Gateway {
	g := &Gateway{providers: map[string]Provider{}, models: cfg.Models}
	for name, p := range cfg.Providers {
		key, err := secrets.NewResolver(sec).String(p.APIKey)
		if err != nil {
			g.providers[name] = failing{fmt.Errorf("provider %q: API key: %w", name, err)}
			continue
		}
		switch p.Type {
		case "anthropic":
			g.providers[name] = NewAnthropic(key, p.BaseURL)
		case "openai":
			g.providers[name] = NewOpenAI(key, p.BaseURL, nil)
		default:
			g.providers[name] = failing{fmt.Errorf("provider %q: unknown type %q", name, p.Type)}
		}
	}
	return g
}

// NewWith builds a gateway over given providers, for tests.
func NewWith(providers map[string]Provider, models map[string]config.Model) *Gateway {
	return &Gateway{providers: providers, models: models}
}

type failing struct{ err error }

func (f failing) Complete(context.Context, ProviderRequest) (ProviderResponse, error) {
	return ProviderResponse{}, f.err
}

// resolve turns an alias or "provider/model" into a provider and model.
func (g *Gateway) resolve(name string) (string, config.Model, error) {
	if name == "" {
		name = DefaultModel
	}
	if m, ok := g.models[name]; ok {
		return m.Provider, m, nil
	}
	if prov, model, ok := strings.Cut(name, "/"); ok {
		if _, known := g.providers[prov]; known {
			return prov, config.Model{Provider: prov, Model: model}, nil
		}
	}
	return "", config.Model{}, fmt.Errorf("unknown model %q: add it under models in the configuration (known: %s)", name, g.aliasList())
}

func (g *Gateway) aliasList() string {
	if len(g.models) == 0 {
		return "none configured"
	}
	names := make([]string, 0, len(g.models))
	for k := range g.models {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Complete sends req to the model it names. With a schema, the answer is
// parsed and validated; an invalid answer is retried once with the error.
func (g *Gateway) Complete(ctx context.Context, req Request) (Result, error) {
	provName, model, err := g.resolve(req.Model)
	if err != nil {
		return Result{}, err
	}
	prov, ok := g.providers[provName]
	if !ok {
		return Result{}, fmt.Errorf("model %q uses provider %q, which is not configured", req.Model, provName)
	}

	var validator *jsonschema.Schema
	if req.Schema != nil {
		validator, err = compileSchema(req.Schema)
		if err != nil {
			return Result{}, fmt.Errorf("outputSchema: %w", err)
		}
	}

	maxTokens := model.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	preq := ProviderRequest{
		Model: model.Model, System: req.System, User: req.User, Schema: req.Schema,
		Temperature: req.Temperature, MaxTokens: maxTokens, Effort: model.Effort, Fallbacks: model.Fallbacks,
	}

	var res Result
	const attempts = 2
	for attempt := 1; attempt <= attempts; attempt++ {
		call := g.call(ctx, prov, provName, model, preq)
		res.Calls = append(res.Calls, call)
		res.CostEur += call.CostEur
		if call.Err != nil {
			return res, call.Err
		}
		res.Text = call.Response
		if validator == nil {
			return res, nil
		}
		value, verr := parseAndValidate(call.Response, validator)
		if verr == nil {
			res.Value = value
			return res, nil
		}
		if attempt == attempts {
			return res, fmt.Errorf("the model's answer does not match outputSchema: %w", verr)
		}
		preq.User = req.User + "\n\nYour previous answer was not valid: " + verr.Error() +
			"\nAnswer again with only a JSON value that matches the schema."
	}
	return res, nil
}

func (g *Gateway) call(ctx context.Context, prov Provider, provName string, model config.Model, preq ProviderRequest) Call {
	start := time.Now()
	resp, err := prov.Complete(ctx, preq)
	c := Call{
		Provider: provName, Model: model.Model, Request: preq, Response: resp.Text,
		InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, Latency: time.Since(start), Err: err,
	}
	if resp.Model != "" {
		c.Model = resp.Model
	}
	c.PriceKnown = model.Price.Input > 0 || model.Price.Output > 0
	c.CostEur = float64(resp.InputTokens)*model.Price.Input/1e6 + float64(resp.OutputTokens)*model.Price.Output/1e6
	return c
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	const url = "mem://output.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, schema); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// parseAndValidate extracts JSON from a model's text and checks it.
func parseAndValidate(text string, sch *jsonschema.Schema) (any, error) {
	raw := extractJSON(text)
	if raw == "" {
		return nil, errors.New("no JSON found in the answer")
	}
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return nil, errors.New(oneLine(err.Error()))
	}
	// Decode again with plain types for expressions and storage.
	var out any
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// extractJSON finds the JSON value in an answer, tolerating code fences
// and text around it.
func extractJSON(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		t = strings.TrimPrefix(t, "```json")
		t = strings.TrimPrefix(t, "```")
		t = strings.TrimSuffix(strings.TrimSpace(t), "```")
		t = strings.TrimSpace(t)
	}
	if json.Valid([]byte(t)) {
		return t
	}
	start := strings.IndexAny(t, "{[")
	if start < 0 {
		return ""
	}
	closer := byte('}')
	if t[start] == '[' {
		closer = ']'
	}
	end := strings.LastIndexByte(t, closer)
	if end <= start {
		return ""
	}
	candidate := t[start : end+1]
	if json.Valid([]byte(candidate)) {
		return candidate
	}
	return ""
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}
