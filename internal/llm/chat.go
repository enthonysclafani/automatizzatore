package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"
)

// Tool is a function the model may call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// ToolCall is the model asking to run a tool.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// ChatMessage is one turn of a conversation with tools.
type ChatMessage struct {
	Role        string       `json:"role"` // user, assistant, tool
	Text        string       `json:"text,omitempty"`
	ToolCalls   []ToolCall   `json:"toolCalls,omitempty"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`
	// provider keeps the provider's own form of an assistant turn (Claude's
	// thinking blocks must be sent back unchanged).
	provider any
}

// ChatRequest is one model turn in an agent loop.
type ChatRequest struct {
	Model    string
	System   string
	Messages []ChatMessage
	Tools    []Tool
}

// ChatProvider is a Provider that supports tools.
type ChatProvider interface {
	Chat(ctx context.Context, model string, req ChatRequest, maxTokens int, effort, fallbacks string) (ChatMessage, ProviderResponse, error)
}

// Chat runs one turn: the model answers with text, tool calls, or both.
func (g *Gateway) Chat(ctx context.Context, req ChatRequest) (ChatMessage, Call, error) {
	provName, model, err := g.resolve(req.Model)
	if err != nil {
		return ChatMessage{}, Call{}, err
	}
	prov, ok := g.providers[provName].(ChatProvider)
	if !ok {
		return ChatMessage{}, Call{}, fmt.Errorf("provider %q does not support tools", provName)
	}
	maxTokens := model.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	start := time.Now()
	msg, resp, err := prov.Chat(ctx, model.Model, req, maxTokens, model.Effort, model.Fallbacks)
	last := ""
	if n := len(req.Messages); n > 0 {
		b, _ := json.Marshal(req.Messages[n-1])
		last = string(b)
	}
	answer, _ := json.Marshal(msg)
	call := Call{
		Provider: provName, Model: model.Model, Response: string(answer),
		Request:     ProviderRequest{Model: model.Model, System: req.System, User: last, MaxTokens: maxTokens, Effort: model.Effort, Fallbacks: model.Fallbacks},
		InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, Latency: time.Since(start), Err: err,
	}
	if resp.Model != "" {
		call.Model = resp.Model
	}
	call.PriceKnown = model.Price.Input > 0 || model.Price.Output > 0
	call.CostEur = float64(resp.InputTokens)*model.Price.Input/1e6 + float64(resp.OutputTokens)*model.Price.Output/1e6
	return msg, call, err
}

// Chat implements ChatProvider for Claude.
func (a *Anthropic) Chat(ctx context.Context, model string, req ChatRequest, maxTokens int, effort, fallbacks string) (ChatMessage, ProviderResponse, error) {
	params := anthropic.BetaMessageNewParams{Model: model, MaxTokens: int64(maxTokens)}
	if req.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if effort != "" {
		params.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(effort)
	}
	if fallbacks == "default" {
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}
	for _, t := range req.Tools {
		props, required := t.Schema["properties"], strList(t.Schema["required"])
		tool := anthropic.BetaToolParam{
			Name: t.Name, Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: props, Required: required},
		}
		params.Tools = append(params.Tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			if p, ok := m.provider.(anthropic.BetaMessageParam); ok {
				params.Messages = append(params.Messages, p)
				continue
			}
			var blocks []anthropic.BetaContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewBetaTextBlock(m.Text))
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewBetaToolUseBlock(c.ID, c.Input, c.Name))
			}
			params.Messages = append(params.Messages, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{Content: blocks}})
		case "tool":
			var blocks []anthropic.BetaContentBlockParamUnion
			for _, r := range m.ToolResults {
				blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.ID, r.Content, r.IsError))
			}
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(blocks...))
		default:
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Text)))
		}
	}

	msg, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return ChatMessage{}, ProviderResponse{}, fmt.Errorf("anthropic: HTTP %d: %s", apiErr.StatusCode, oneLine(apiErr.Error()))
		}
		return ChatMessage{}, ProviderResponse{}, fmt.Errorf("anthropic: %w", err)
	}
	resp := ProviderResponse{
		Model:        msg.Model,
		InputTokens:  msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	out := ChatMessage{Role: "assistant", provider: msg.ToParam()}
	var text strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			text.WriteString(b.Text)
		case anthropic.BetaToolUseBlock:
			var input map[string]any
			raw, _ := json.Marshal(b.Input)
			_ = json.Unmarshal(raw, &input)
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: input})
		}
	}
	out.Text = text.String()
	resp.Text = out.Text
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return out, resp, errors.New("anthropic: the model declined the request")
	case anthropic.BetaStopReasonMaxTokens:
		return out, resp, fmt.Errorf("anthropic: the answer hit the %d-token limit; raise maxTokens for this model", maxTokens)
	}
	return out, resp, nil
}

func strList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaChatMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

// Chat implements ChatProvider for OpenAI-compatible APIs.
func (o *OpenAI) Chat(ctx context.Context, model string, req ChatRequest, maxTokens int, _, _ string) (ChatMessage, ProviderResponse, error) {
	text := func(s string) *string { return &s }
	var msgs []oaChatMessage
	if req.System != "" {
		msgs = append(msgs, oaChatMessage{Role: "system", Content: text(req.System)})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			am := oaChatMessage{Role: "assistant", Content: text(m.Text)}
			for _, c := range m.ToolCalls {
				args, _ := json.Marshal(c.Input)
				tc := oaToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name, tc.Function.Arguments = c.Name, string(args)
				am.ToolCalls = append(am.ToolCalls, tc)
			}
			msgs = append(msgs, am)
		case "tool":
			for _, r := range m.ToolResults {
				content := r.Content
				if r.IsError {
					content = "ERROR: " + content
				}
				msgs = append(msgs, oaChatMessage{Role: "tool", ToolCallID: r.ID, Content: text(content)})
			}
		default:
			msgs = append(msgs, oaChatMessage{Role: "user", Content: text(m.Text)})
		}
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	body := map[string]any{"model": model, "messages": msgs}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChatMessage{}, ProviderResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatMessage{}, ProviderResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	res, err := o.client.Do(httpReq)
	if err != nil {
		return ChatMessage{}, ProviderResponse{}, fmt.Errorf("openai-compatible: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return ChatMessage{}, ProviderResponse{}, fmt.Errorf("openai-compatible: HTTP %d: %s", res.StatusCode, oneLine(string(raw)))
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string       `json:"content"`
				ToolCalls []oaToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return ChatMessage{}, ProviderResponse{}, fmt.Errorf("openai-compatible: invalid answer")
	}
	choice := out.Choices[0]
	msg := ChatMessage{Role: "assistant", Text: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			input = map[string]any{"_invalid_arguments": tc.Function.Arguments}
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	resp := ProviderResponse{Text: msg.Text, Model: out.Model, InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}
	if choice.FinishReason == "length" {
		return msg, resp, errors.New("openai-compatible: the answer hit the token limit; raise maxTokens for this model")
	}
	return msg, resp, nil
}
