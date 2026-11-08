// Package notify tells people about finished runs.
//
// Phase 1 delivers to webhook channels: a JSON POST that Slack and
// Mattermost incoming webhooks, Discord, ntfy and most chat tools accept.
// Native Slack, Telegram and email channels arrive with the connectors.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Notifier sends run notifications.
type Notifier struct {
	Secrets secrets.Provider
	HTTP    *http.Client
	Log     *slog.Logger
}

// Payload is the JSON body sent to webhooks.
type Payload struct {
	// Text is a one-line summary; Slack and Mattermost show "text",
	// Discord shows "content".
	Text       string    `json:"text"`
	Content    string    `json:"content"`
	Automation string    `json:"automation"`
	RunID      string    `json:"runId"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Anomalies  int       `json:"anomalies"`
	CostEur    float64   `json:"costEur"`
	Trigger    string    `json:"trigger"`
	FinishedAt time.Time `json:"finishedAt"`
}

// ShouldNotify applies a notifications.on policy to a run.
func ShouldNotify(on string, run *store.Run) bool {
	switch on {
	case "always":
		return true
	case "never":
		return false
	case "anomaly":
		return run.Status != store.RunSucceeded || run.Anomalies > 0
	default: // failure
		return run.Status == store.RunFailed
	}
}

// Notify implements engine.Notifier.
func (n *Notifier) Notify(ctx context.Context, a engine.Automation, run *store.Run) {
	cfg := a.Manifest.Notifications
	if cfg == nil || !ShouldNotify(cfg.On, run) {
		return
	}
	p := buildPayload(a, run)
	for _, ch := range cfg.Channels {
		kind, target, _ := strings.Cut(ch, ":")
		if kind != "webhook" {
			n.log().Warn("notification channel not available yet; use a webhook: channel until phase 3",
				"automation", a.Name, "channel", kind)
			continue
		}
		if err := n.postWebhook(ctx, target, p); err != nil {
			n.log().Error("notification failed", "automation", a.Name, "run", run.ID, "err", err)
		}
	}
}

func buildPayload(a engine.Automation, run *store.Run) Payload {
	text := fmt.Sprintf("%s: run %s", a.Name, run.Status)
	switch {
	case run.Error != "":
		text += " - " + run.Error
	case run.Anomalies > 0:
		text += fmt.Sprintf(" with %d failed step(s)", run.Anomalies)
	}
	finished := time.Now()
	if run.FinishedAt != nil {
		finished = *run.FinishedAt
	}
	return Payload{
		Text: text, Content: text, Automation: a.Name, RunID: run.ID, Status: run.Status, Error: run.Error,
		Anomalies: run.Anomalies, CostEur: run.CostEur, Trigger: run.TriggerType, FinishedAt: finished,
	}
}

func (n *Notifier) postWebhook(ctx context.Context, target string, p Payload) error {
	url, err := secrets.NewResolver(n.Secrets).String(target)
	if err != nil {
		return err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pendolo")
	client := n.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The error can contain the URL, which may be a secret.
		return fmt.Errorf("webhook: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) log() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.Default()
}
