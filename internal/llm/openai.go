package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI calls any OpenAI-compatible chat completions API: OpenAI,
// OpenRouter, Gemini's OpenAI endpoint, Ollama, LM Studio, vLLM, Groq.
type OpenAI struct {
	apiKey  string
	baseURL string
	client  *http.Client
	// retries for 429 and 5xx answers.
	retries int
	backoff time.Duration
}

// NewOpenAI returns an adapter. baseURL ends before /chat/completions,
// e.g. https://api.openai.com/v1 or http://ollama:11434/v1.
func NewOpenAI(apiKey, baseURL string, client *http.Client) *OpenAI {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &OpenAI{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), client: client, retries: 2, backoff: 2 * time.Second}
}

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaRequest struct {
	Model          string      `json:"model"`
	Messages       []oaMessage `json:"messages"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	Temperature    *float64    `json:"temperature,omitempty"`
	ResponseFormat any         `json:"response_format,omitempty"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider.
func (o *OpenAI) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	body := oaRequest{Model: req.Model, Temperature: req.Temperature, MaxTokens: req.MaxTokens}
	if req.System != "" {
		body.Messages = append(body.Messages, oaMessage{Role: "system", Content: req.System})
	}
	body.Messages = append(body.Messages, oaMessage{Role: "user", Content: req.User})
	if req.Schema != nil {
		body.ResponseFormat = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "output", "schema": req.Schema},
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ProviderResponse{}, err
	}

	var lastErr error
	for attempt := 0; attempt <= o.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ProviderResponse{}, ctx.Err()
			case <-time.After(o.backoff * time.Duration(1<<(attempt-1))):
			}
		}
		resp, retry, err := o.do(ctx, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return ProviderResponse{}, lastErr
}

func (o *OpenAI) do(ctx context.Context, payload []byte) (ProviderResponse, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ProviderResponse{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	res, err := o.client.Do(httpReq)
	if err != nil {
		return ProviderResponse{}, ctx.Err() == nil, fmt.Errorf("openai-compatible: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return ProviderResponse{}, true, fmt.Errorf("openai-compatible: read answer: %w", err)
	}
	if res.StatusCode >= 300 {
		retry := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		if s := res.Header.Get("Retry-After"); retry && s != "" {
			if secs, err := strconv.Atoi(s); err == nil && secs > 30 {
				retry = false // do not hold a run for minutes; the step retry policy decides
			}
		}
		return ProviderResponse{}, retry, fmt.Errorf("openai-compatible: HTTP %d: %s", res.StatusCode, oneLine(string(raw)))
	}
	var out oaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ProviderResponse{}, false, fmt.Errorf("openai-compatible: invalid answer: %w", err)
	}
	if out.Error != nil {
		return ProviderResponse{}, false, fmt.Errorf("openai-compatible: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return ProviderResponse{}, false, fmt.Errorf("openai-compatible: answer has no choices")
	}
	choice := out.Choices[0]
	resp := ProviderResponse{
		Text: choice.Message.Content, Model: out.Model,
		InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
	}
	if choice.Message.Refusal != "" {
		return resp, false, fmt.Errorf("openai-compatible: the model declined: %s", oneLine(choice.Message.Refusal))
	}
	if choice.FinishReason == "length" {
		return resp, false, fmt.Errorf("openai-compatible: the answer hit the token limit; raise maxTokens for this model")
	}
	return resp, false, nil
}
