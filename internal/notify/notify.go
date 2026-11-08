// Package notify tells people about finished runs, on the channels an
// automation lists: webhook:<url>, slack:<#channel>, telegram:<chat id>
// and email:<address>.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Notifier sends run notifications.
type Notifier struct {
	Secrets secrets.Provider
	Config  config.Notify
	HTTP    *http.Client
	Log     *slog.Logger
	// API base URLs, replaced in tests.
	SlackAPI    string
	TelegramAPI string
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
		if err := n.Send(ctx, ch, p); err != nil {
			n.log().Error("notification failed", "automation", a.Name, "run", run.ID, "channel", channelKind(ch), "err", err)
		}
	}
}

// Send delivers p to one channel.
func (n *Notifier) Send(ctx context.Context, channel string, p Payload) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	kind, target, _ := strings.Cut(channel, ":")
	switch kind {
	case "webhook":
		return n.webhook(ctx, target, p)
	case "slack":
		return n.slack(ctx, target, p)
	case "telegram":
		return n.telegram(ctx, target, p)
	case "email":
		return n.email(ctx, target, p)
	default:
		return fmt.Errorf("unknown channel %q", kind)
	}
}

func channelKind(ch string) string {
	kind, _, _ := strings.Cut(ch, ":")
	return kind
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

// longText is the message for chat and email channels.
func longText(p Payload) string {
	return fmt.Sprintf("%s\nRun %s, trigger %s, cost €%.4f.\nDetails: pendolo show %s", p.Text, p.RunID, p.Trigger, p.CostEur, p.RunID)
}

func (n *Notifier) secret(ref string) (string, error) {
	return secrets.NewResolver(n.Secrets).String(ref)
}

func (n *Notifier) client() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return http.DefaultClient
}

// postJSON sends v and returns the response body. Errors never include the
// URL, which can hold a token.
func (n *Notifier) postJSON(ctx context.Context, url string, headers map[string]string, v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "pendolo")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client().Do(req)
	if err != nil {
		return nil, errors.New("request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return raw, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return raw, nil
}

func (n *Notifier) webhook(ctx context.Context, target string, p Payload) error {
	url, err := n.secret(target)
	if err != nil {
		return err
	}
	_, err = n.postJSON(ctx, url, nil, p)
	return err
}

func (n *Notifier) slack(ctx context.Context, channel string, p Payload) error {
	if n.Config.Slack.Token == "" {
		return errors.New("slack channel used but notify.slack.token is not configured")
	}
	token, err := n.secret(n.Config.Slack.Token)
	if err != nil {
		return err
	}
	base := n.SlackAPI
	if base == "" {
		base = "https://slack.com/api"
	}
	raw, err := n.postJSON(ctx, base+"/chat.postMessage", map[string]string{"Authorization": "Bearer " + token},
		map[string]any{"channel": channel, "text": longText(p)})
	if err != nil {
		return err
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &res) != nil || !res.OK {
		return fmt.Errorf("slack: %s", res.Error)
	}
	return nil
}

func (n *Notifier) telegram(ctx context.Context, chatID string, p Payload) error {
	if n.Config.Telegram.BotToken == "" {
		return errors.New("telegram channel used but notify.telegram.botToken is not configured")
	}
	token, err := n.secret(n.Config.Telegram.BotToken)
	if err != nil {
		return err
	}
	base := n.TelegramAPI
	if base == "" {
		base = "https://api.telegram.org"
	}
	raw, err := n.postJSON(ctx, base+"/bot"+token+"/sendMessage", nil,
		map[string]any{"chat_id": chatID, "text": longText(p), "disable_web_page_preview": true})
	if err != nil {
		var res struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &res)
		return fmt.Errorf("telegram: %w %s", err, res.Description)
	}
	return nil
}

func (n *Notifier) email(ctx context.Context, to string, p Payload) error {
	cfg := n.Config.Email
	if cfg.Host == "" {
		return errors.New("email channel used but notify.email is not configured")
	}
	if strings.ContainsAny(to, "\r\n") || !strings.Contains(to, "@") {
		return fmt.Errorf("invalid email address %q", to)
	}
	password, err := n.secret(cfg.Password)
	if err != nil {
		return err
	}
	security := cfg.Security
	if security == "" {
		security = "starttls"
	}
	port := cfg.Port
	if port == 0 {
		port = map[string]int{"starttls": 587, "tls": 465, "none": 25}[security]
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: connect %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer c.Close()
	if security == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp: login: %w", err)
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(fmt.Sprintf("[Pendolo] %s: run %s", p.Automation, p.Status))
	msg := "From: " + cfg.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(longText(p), "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return c.Quit()
}

func (n *Notifier) log() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.Default()
}
