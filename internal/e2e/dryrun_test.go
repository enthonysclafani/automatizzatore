package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/testfakes"
)

// A dry run of the email triage reads the email and asks the model, but
// labels nothing and drafts nothing.
func TestDryRunDoesNotWrite(t *testing.T) {
	w := newWorld(t, &testfakes.Message{
		ID: "m1", ThreadID: "t1", From: "Anna <anna@client.it>", To: "me@example.com", Subject: "Quote",
		Body: "Could you send a quote?", MessageID: "<abc@client.it>", Labels: []string{"INBOX"},
	})
	w.llm.Answers = []string{
		`{"category":"client","needsReply":true,"summary":"Anna asks for a quote","actions":["send quote"]}`,
		"Dear Anna, the quote comes Thursday.",
	}
	ctx := context.Background()
	conn, _ := w.hub.GoogleConnection(ctx, store.DefaultWorkspace, "google")
	email, _ := w.hub.GmailMessage(ctx, conn, "m1")

	out, run, steps := w.run(template(t, "gmail-triage"), engine.TriggerDryRun, connectors.EmailTriggerData(email))
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
	if len(w.llm.SeenComplete) != 2 {
		t.Fatalf("model calls = %d", len(w.llm.SeenComplete))
	}
	if labels := strings.Join(w.google.Labels("m1"), ","); strings.Contains(labels, "pendolo") {
		t.Fatalf("a dry run labelled the email: %s", labels)
	}
	if len(w.google.Drafts) != 0 || len(w.google.Sent) != 0 {
		t.Fatalf("drafts %d, sent %d", len(w.google.Drafts), len(w.google.Sent))
	}
	var skipped int
	for _, s := range steps {
		if strings.Contains(s.Logs, "dry run") {
			skipped++
		}
	}
	if skipped < 2 {
		t.Fatalf("expected the write steps to be marked as dry run:\n%s", dump(steps))
	}
}

// In a dry run, an agent may read but its writes are only pretended, and
// explicit approvals and destructive actions do not wait for anyone.
func TestDryRunAgentAndApprovals(t *testing.T) {
	w := newWorld(t)
	w.llm.Turns = []llm.ChatMessage{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "openseo__get_keyword_metrics", Input: map[string]any{"keyword": "wordpress"}},
			{ID: "c2", Name: "slack__send_message", Input: map[string]any{"channel": "#x", "text": "hi"}},
		}},
		{Role: "assistant", Text: "done"},
	}
	a := inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: dry-agent }
triggers: [ { manual: {} } ]
autonomy: auto
steps:
  - { id: research, type: agent, promptText: "Research.", tools: [openseo.get_keyword_metrics, slack.send_message] }
  - { id: check, type: approval, message: "Go on?" }
  - { id: cleanup, type: connector, action: openseo.delete_project, with: { id: "p1" } }
  - { id: notify, type: connector, action: slack.send_message, with: { channel: "#x", text: "done" } }
`)
	out, run, steps := w.run(a, engine.TriggerDryRun, nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
	if len(w.slack.Sent()) != 0 {
		t.Fatalf("a dry run posted on Slack: %v", w.slack.Sent())
	}
	approvals, _ := w.store.ListApprovals(context.Background(), store.ApprovalFilter{RunID: run.ID})
	if len(approvals) != 0 {
		t.Fatalf("a dry run asked for approval: %+v", approvals)
	}
	if s := step(steps, "cleanup"); !strings.Contains(string(s.Input), "wouldWaitForApproval") {
		t.Fatalf("cleanup = %s", s.Input)
	}
}

// A dry run never resolves a secret and never contacts a private address:
// a builder cannot use one to read secrets or probe the internal network.
func TestDryRunKeepsSecretsAndNetwork(t *testing.T) {
	var got atomic.Value
	got.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			got.Store(r.Header.Get("X-K"))
		}
	}))
	defer srv.Close()
	w := newWorld(t)
	w.eng.Secrets = secrets.Static{"k": "TOPSECRET"}
	a := inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: dry-secret }
triggers: [ { manual: {} } ]
steps:
  - { id: leak, type: http, request: { url: "`+srv.URL+`", headers: { X-K: "secret://k" } } }
  - { id: probe, type: http, request: { url: "`+srv.URL+`/admin" } }
`)
	out, run, steps := w.run(a, engine.TriggerDryRun, nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
	if v := got.Load().(string); v != "" {
		t.Fatalf("the server received %q", v)
	}
	if s := step(steps, "leak"); !strings.Contains(s.Logs, "never resolve secrets") {
		t.Fatalf("leak = %+v", s)
	}
	if s := step(steps, "probe"); !strings.Contains(s.Logs, "only public addresses") {
		t.Fatalf("probe = %+v", s)
	}
	// A real run does both.
	if out, run, steps := w.run(a, "manual", nil); out.Status != store.RunSucceeded || got.Load().(string) != "TOPSECRET" {
		t.Fatalf("real run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
}
