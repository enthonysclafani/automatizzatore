package llm

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/enthonysclafani/automatizzatore/internal/config"
)

type fakeProvider struct {
	answers []string
	got     []ProviderRequest
}

func (f *fakeProvider) Complete(_ context.Context, req ProviderRequest) (ProviderResponse, error) {
	f.got = append(f.got, req)
	a := f.answers[0]
	if len(f.answers) > 1 {
		f.answers = f.answers[1:]
	}
	return ProviderResponse{Text: a, InputTokens: 1000, OutputTokens: 500}, nil
}

var schema = map[string]any{
	"type":     "object",
	"required": []any{"category"},
	"properties": map[string]any{
		"category": map[string]any{"enum": []any{"spam", "client"}},
	},
}

func TestGatewayAliasCostAndSchema(t *testing.T) {
	fp := &fakeProvider{answers: []string{"```json\n{\"category\": \"client\"}\n```"}}
	g := NewWith(map[string]Provider{"p": fp}, map[string]config.Model{
		"smart": {Provider: "p", Model: "big-model", Price: config.Price{Input: 4, Output: 20}, Effort: "high"},
	})
	res, err := g.Complete(context.Background(), Request{System: "sys", User: "hello", Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if res.Value.(map[string]any)["category"] != "client" {
		t.Fatalf("value = %#v", res.Value)
	}
	if want := 1000*4/1e6 + 500*20/1e6; math.Abs(res.CostEur-want) > 1e-12 {
		t.Fatalf("cost = %v, want %v", res.CostEur, want)
	}
	if len(res.Calls) != 1 || !res.Calls[0].PriceKnown || fp.got[0].Model != "big-model" || fp.got[0].Effort != "high" {
		t.Fatalf("calls = %+v, request = %+v", res.Calls, fp.got[0])
	}
}

func TestGatewayRetriesInvalidAnswerOnce(t *testing.T) {
	fp := &fakeProvider{answers: []string{`{"category": "other"}`, `{"category": "spam"}`}}
	g := NewWith(map[string]Provider{"p": fp}, map[string]config.Model{"fast": {Provider: "p", Model: "m"}})
	res, err := g.Complete(context.Background(), Request{Model: "fast", User: "x", Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Calls) != 2 || !strings.Contains(fp.got[1].User, "not valid") {
		t.Fatalf("expected a retry with feedback, calls=%d user=%q", len(res.Calls), fp.got[1].User)
	}

	fp2 := &fakeProvider{answers: []string{"no json here"}}
	g2 := NewWith(map[string]Provider{"p": fp2}, map[string]config.Model{"fast": {Provider: "p", Model: "m"}})
	res2, err := g2.Complete(context.Background(), Request{Model: "fast", User: "x", Schema: schema})
	if err == nil || len(res2.Calls) != 2 {
		t.Fatalf("expected failure after 2 calls, got err=%v calls=%d", err, len(res2.Calls))
	}
}

func TestGatewayResolve(t *testing.T) {
	fp := &fakeProvider{answers: []string{"hi"}}
	g := NewWith(map[string]Provider{"ollama": fp}, nil)
	res, err := g.Complete(context.Background(), Request{Model: "ollama/llama3.3", User: "x"})
	if err != nil || res.Text != "hi" || fp.got[0].Model != "llama3.3" || res.Calls[0].PriceKnown {
		t.Fatalf("direct provider/model: res=%+v err=%v", res, err)
	}
	if _, err := g.Complete(context.Background(), Request{Model: "smart", User: "x"}); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("unknown alias: %v", err)
	}
}

func TestStrictSchema(t *testing.T) {
	in := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags":  map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{"type": "string", "minLength": 1}},
			"inner": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "minimum": 0}}},
		},
	}
	out := strictSchema(in).(map[string]any)
	b, _ := json.Marshal(out)
	s := string(b)
	for _, gone := range []string{"maxItems", "minLength", "minimum"} {
		if strings.Contains(s, gone) {
			t.Errorf("%s not removed: %s", gone, s)
		}
	}
	if strings.Count(s, `"additionalProperties":false`) != 2 {
		t.Errorf("additionalProperties not set on both objects: %s", s)
	}
	if _, touched := in["additionalProperties"]; touched {
		t.Error("input schema was modified")
	}
}

func TestAnthropicAdapter(t *testing.T) {
	var body map[string]any
	var betaHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		betaHeader = r.Header.Get("anthropic-beta")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
			"content":[{"type":"text","text":"{\"category\":\"spam\"}"}],
			"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7,"cache_read_input_tokens":3}}`)
	}))
	defer srv.Close()

	a := NewAnthropic("test-key", srv.URL, option.WithMaxRetries(0))
	temp := 0.2
	resp, err := a.Complete(context.Background(), ProviderRequest{
		Model: "claude-opus-5-5", System: "sys", User: "hi", Schema: schema, MaxTokens: 1000,
		Effort: "high", Fallbacks: "default", Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"category":"spam"}` || resp.InputTokens != 15 || resp.OutputTokens != 7 {
		t.Fatalf("response = %+v", resp)
	}
	if body["fallbacks"] != "default" || !strings.Contains(betaHeader, "server-side-fallback-2026-07-01") {
		t.Fatalf("fallbacks not sent: body=%v beta=%q", body["fallbacks"], betaHeader)
	}
	if _, sent := body["temperature"]; sent {
		t.Fatal("temperature must not be sent to Claude")
	}
	oc, _ := body["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	sch, _ := format["schema"].(map[string]any)
	if oc["effort"] != "high" || format["type"] != "json_schema" || sch["additionalProperties"] != false {
		t.Fatalf("output_config = %#v", oc)
	}
}

func TestAnthropicRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
			"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"},"usage":{"input_tokens":5,"output_tokens":0}}`)
	}))
	defer srv.Close()
	_, err := NewAnthropic("k", srv.URL, option.WithMaxRetries(0)).Complete(context.Background(), ProviderRequest{Model: "claude-opus-5-5", User: "x", MaxTokens: 10})
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("refusal not reported: %v", err)
	}
}

func TestOpenAIAdapter(t *testing.T) {
	var calls atomic.Int32
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, `{"error":{"message":"busy"}}`, http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = io.WriteString(w, `{"model":"llama3.3","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":9,"completion_tokens":2}}`)
	}))
	defer srv.Close()

	o := NewOpenAI("k", srv.URL+"/v1/", nil)
	o.backoff = 0
	temp := 0.3
	resp, err := o.Complete(context.Background(), ProviderRequest{Model: "llama3.3", System: "s", User: "u", Schema: schema, Temperature: &temp, MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || resp.InputTokens != 9 || calls.Load() != 2 {
		t.Fatalf("resp=%+v calls=%d", resp, calls.Load())
	}
	rf, _ := body["response_format"].(map[string]any)
	msgs, _ := body["messages"].([]any)
	if rf["type"] != "json_schema" || len(msgs) != 2 || body["temperature"] != 0.3 {
		t.Fatalf("body = %#v", body)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer bad.Close()
	o2 := NewOpenAI("k", bad.URL, nil)
	if _, err := o2.Complete(context.Background(), ProviderRequest{Model: "m", User: "u"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 not reported: %v", err)
	}
}
