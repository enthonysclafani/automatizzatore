package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"
)

// Anthropic calls the Claude Messages API through the official Go SDK.
type Anthropic struct {
	client anthropic.Client
}

// NewAnthropic returns an adapter. baseURL may be empty for the default API.
func NewAnthropic(apiKey, baseURL string, opts ...option.RequestOption) *Anthropic {
	all := []option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(2)}
	if baseURL != "" {
		all = append(all, option.WithBaseURL(baseURL))
	}
	all = append(all, opts...)
	return &Anthropic{client: anthropic.NewClient(all...)}
}

// Complete implements Provider.
func (a *Anthropic) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     req.Model,
		MaxTokens: int64(req.MaxTokens),
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User)),
		},
	}
	if req.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if req.Schema != nil {
		params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: strictSchema(req.Schema)}
	}
	if req.Effort != "" {
		params.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(req.Effort)
	}
	// Sampling parameters are rejected by current Claude models, so a step's
	// temperature is not sent here.
	if req.Fallbacks == "default" {
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	msg, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return ProviderResponse{}, fmt.Errorf("anthropic: HTTP %d: %s", apiErr.StatusCode, oneLine(apiErr.Error()))
		}
		return ProviderResponse{}, fmt.Errorf("anthropic: %w", err)
	}

	resp := ProviderResponse{
		Model: msg.Model,
		// Cache reads and writes are input too; count them for cost.
		InputTokens:  msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	resp.Text = text.String()

	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		reason := "the model declined the request"
		if c := string(msg.StopDetails.Category); c != "" {
			reason += " (category " + c + ")"
		}
		return resp, errors.New("anthropic: " + reason)
	case anthropic.BetaStopReasonMaxTokens:
		return resp, fmt.Errorf("anthropic: the answer hit the %d-token limit; raise maxTokens for this model", req.MaxTokens)
	}
	return resp, nil
}
