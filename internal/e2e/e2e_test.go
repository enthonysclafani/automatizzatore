// Package e2e runs the bundled templates end to end against fake services:
// the phase 3 exit criterion (email triage and editorial plan) plus
// approvals and autonomy.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/testfakes"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

type world struct {
	t      *testing.T
	store  *store.Store
	hub    *connectors.Hub
	eng    *engine.Engine
	llm    *testfakes.Script
	google *testfakes.Google
	slack  *testfakes.Slack
}

func newWorld(t *testing.T, msgs ...*testfakes.Message) *world {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	v, _ := vault.New(make([]byte, 32))
	g := testfakes.NewGoogle(t, msgs...)
	sl := testfakes.NewSlack(t)
	brave := testfakes.Brave(t)
	seo := testfakes.MCP(t, "seo-key")
	hub := &connectors.Hub{
		Store: st, Vault: v, PublicURL: "http://localhost:8080", AllowPrivateFetch: true,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Endpoints: map[string]string{"gmail": g.Server.URL, "docs": g.Server.URL, "slack": sl.Server.URL, "brave": brave.URL},
	}
	w := &world{t: t, store: st, hub: hub, google: g, slack: sl, llm: &testfakes.Script{}}
	ctx := context.Background()
	_ = st.EnsureWorkspace(ctx, store.DefaultWorkspace, time.Now())
	tok, _ := json.Marshal(oauth2.Token{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", Expiry: time.Now().Add(24 * time.Hour)})
	w.connect("google", connectors.KindGoogle, map[string]any{"clientId": "id"}, map[string]string{"clientSecret": "s", "token": string(tok)})
	w.connect("slack", connectors.KindSlack, nil, map[string]string{"token": "xoxb-test"})
	w.connect("web", connectors.KindWebSearch, map[string]any{"provider": "brave"}, map[string]string{"apiKey": "k"})
	w.connect("openseo", connectors.KindMCP, map[string]any{"url": seo.URL}, map[string]string{connectors.HeaderPrefix + "Authorization": "Bearer seo-key"})

	gw := llm.NewWith(map[string]llm.Provider{"fake": w.llm}, map[string]config.Model{
		"smart": {Provider: "fake", Model: "big", Price: config.Price{Input: 4, Output: 20}},
		"fast":  {Provider: "fake", Model: "small", Price: config.Price{Input: 0.1, Output: 0.5}},
	})
	w.eng = &engine.Engine{Store: st, LLM: gw, Connectors: hub, Secrets: secrets.Static{}, Log: hub.Log}
	return w
}

func (w *world) connect(name, kind string, cfg map[string]any, sec map[string]string) {
	w.t.Helper()
	sealed, err := w.hub.Seal(store.DefaultWorkspace, name, sec)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.store.PutConnection(context.Background(), store.Connection{
		Workspace: store.DefaultWorkspace, Name: name, Kind: kind, Config: cfg, Sealed: sealed,
		Status: store.ConnReady, UpdatedAt: time.Now(), UpdatedBy: "test",
	}); err != nil {
		w.t.Fatal(err)
	}
}

func template(t *testing.T, name string) engine.Automation {
	t.Helper()
	a, inv := registry.LoadFolder(filepath.Join("..", "..", "templates", name))
	if inv != nil {
		t.Fatalf("%s invalid: %+v", name, inv.Problems)
	}
	return a
}

func inline(t *testing.T, yaml string) engine.Automation {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	a, inv := registry.LoadFolder(dir)
	if inv != nil {
		t.Fatalf("invalid: %+v", inv.Problems)
	}
	return a
}

func (w *world) run(a engine.Automation, trigger string, data any) (engine.Outcome, *store.Run, []store.Step) {
	w.t.Helper()
	ctx := context.Background()
	run, err := w.store.StartNow(ctx, store.NewRun{Automation: a.Name, Workspace: a.Workspace(), TriggerType: trigger, TriggerData: data}, "test", time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	out := w.eng.Execute(ctx, run, a)
	final, _ := w.store.GetRun(ctx, run.ID)
	steps, _ := w.store.ListSteps(ctx, run.ID)
	return out, final, steps
}

func step(steps []store.Step, path string) store.Step {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Path == path {
			return steps[i]
		}
	}
	return store.Step{}
}

func dump(steps []store.Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Path + " " + s.Status + " " + s.Error + "\n")
	}
	return b.String()
}

// Exit criterion, part 1: the gmail-triage template labels the email and
// saves a reply draft, and never sends.
func TestEmailTriageEndToEnd(t *testing.T) {
	w := newWorld(t, &testfakes.Message{
		ID: "m1", ThreadID: "t1", From: "Anna <anna@client.it>", To: "me@example.com", Subject: "Quote for the website",
		Body: "Good morning, could you send a quote for the new site by Friday?", MessageID: "<abc@client.it>",
		Labels: []string{"INBOX", "UNREAD"},
	})
	w.llm.Answers = []string{
		`{"category":"client","needsReply":true,"summary":"Anna asks for a quote by Friday","actions":["send quote"]}`,
		"Dear Anna, thanks for asking: you will have the quote by Thursday.",
	}
	ctx := context.Background()
	conn, err := w.hub.GoogleConnection(ctx, store.DefaultWorkspace, "google")
	if err != nil {
		t.Fatal(err)
	}
	email, err := w.hub.GmailMessage(ctx, conn, "m1")
	if err != nil {
		t.Fatal(err)
	}

	out, run, steps := w.run(template(t, "gmail-triage"), "email", connectors.EmailTriggerData(email))
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
	if !strings.Contains(w.llm.SeenComplete[0].User, "send a quote for the new site") {
		t.Fatalf("the classifier did not get the email body: %s", w.llm.SeenComplete[0].User)
	}
	labels := strings.Join(w.google.Labels("m1"), ",")
	if !strings.Contains(labels, "Label_pendolo/client") {
		t.Fatalf("labels = %s", labels)
	}
	if len(w.google.Drafts) != 1 || len(w.google.Sent) != 0 {
		t.Fatalf("drafts %d, sent %d", len(w.google.Drafts), len(w.google.Sent))
	}
	draft := w.google.Drafts[0]
	for _, want := range []string{"To: Anna <anna@client.it>", "In-Reply-To: <abc@client.it>", "Re: Quote for the website", "you will have the quote"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("draft lacks %q:\n%s", want, draft)
		}
	}
	if out.CostEur <= 0 {
		t.Fatalf("cost not tracked: %+v", out)
	}
}

// Exit criterion, part 2: the editorial-plan template researches with an
// agent (web search + MCP), validates on the MCP server, writes a Google
// Doc and posts the link on Slack.
func TestEditorialPlanEndToEnd(t *testing.T) {
	w := newWorld(t)
	w.llm.Turns = []llm.ChatMessage{
		{Role: "assistant", Text: "Let me research.", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "web__search", Input: map[string]any{"query": "wordpress agencies trends"}},
			{ID: "c2", Name: "openseo__research_keywords", Input: map[string]any{"seed": "wordpress hosting"}},
		}},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c3", Name: "finish", Input: map[string]any{
			"keywords": []any{"wordpress hosting guide", "core web vitals"},
			"ideas":    []any{map[string]any{"title": "Choosing WordPress hosting", "keyword": "wordpress hosting guide", "angle": "costs"}},
		}}}},
	}
	w.llm.Answers = []string{"# Editorial plan\n\n1. Choosing WordPress hosting (1000 searches/month)"}

	out, run, steps := w.run(template(t, "editorial-plan"), "manual", nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %s: %s\n%s", run.Status, run.Error, dump(steps))
	}
	agent := step(steps, "ideas")
	if !strings.Contains(agent.Logs, "web.search") || !strings.Contains(agent.Logs, "openseo.research_keywords") || !strings.Contains(agent.Logs, "finish") {
		t.Fatalf("agent logs:\n%s", agent.Logs)
	}
	toolTurn := w.llm.SeenChat[1].Messages
	results := toolTurn[len(toolTurn)-1].ToolResults
	if len(results) != 2 || !strings.Contains(results[0].Content, "Trends") || !strings.Contains(results[1].Content, "wordpress hosting guide") {
		t.Fatalf("tool results given to the model: %+v", results)
	}
	if v := string(step(steps, "validate").Output); !strings.Contains(v, `"volume":1000`) {
		t.Fatalf("metrics: %s", v)
	}
	if len(w.google.Docs) != 1 || !strings.Contains(w.google.Docs["doc-1"], "Editorial plan") {
		t.Fatalf("docs = %+v", w.google.Docs)
	}
	sent := w.slack.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0]["text"], "https://docs.google.com/document/d/doc-1/edit") || sent[0]["channel"] != "#content" {
		t.Fatalf("slack = %+v", sent)
	}
	calls, _ := w.store.ListLLMCalls(context.Background(), run.ID)
	if len(calls) != 3 { // two agent turns, one writing call
		t.Fatalf("model calls recorded: %d", len(calls))
	}
}

// With autonomy ask, sending waits for a person; approved, the run resumes
// where it stopped and sends; rejected, it is cancelled.
func TestApprovalPausesAndResumes(t *testing.T) {
	w := newWorld(t, &testfakes.Message{ID: "m1", ThreadID: "t1", From: "a@b.it", Subject: "Hi", Body: "hello", MessageID: "<x@b.it>"})
	a := inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: reply }
triggers: [ { manual: {} } ]
autonomy: ask
steps:
  - id: before
    type: shell
    run: echo prepared
  - id: send
    type: connector
    action: gmail.send
    with: { messageId: m1, body: "Thanks!" }
  - id: after
    type: shell
    run: 'echo "sent $ID"'
    env: { ID: "${steps.send.output.messageId}" }
`)
	ctx := context.Background()
	out, run, steps := w.run(a, "manual", nil)
	if out.Status != store.RunWaiting || run.Status != store.RunWaiting {
		t.Fatalf("run %+v\n%s", run, dump(steps))
	}
	if len(w.google.Sent) != 0 || step(steps, "send").Status != store.StepWaiting {
		t.Fatalf("sent before approval: %d, step %+v", len(w.google.Sent), step(steps, "send"))
	}
	pending, _ := w.store.ListApprovals(ctx, store.ApprovalFilter{RunID: run.ID})
	if len(pending) != 1 || pending[0].Status != store.ApprovalPending || !strings.Contains(pending[0].Message, "gmail.send") {
		t.Fatalf("approvals = %+v", pending)
	}

	if _, err := w.store.Decide(ctx, pending[0].ID, store.ApprovalApproved, "boss@example.com", "ok", time.Now()); err != nil {
		t.Fatal(err)
	}
	resumed, err := w.store.Claim(ctx, "w", time.Minute, time.Now())
	if err != nil || resumed == nil || resumed.ID != run.ID {
		t.Fatalf("claim after approval: %+v %v", resumed, err)
	}
	out = w.eng.Execute(ctx, resumed, a)
	final, _ := w.store.GetRun(ctx, run.ID)
	steps, _ = w.store.ListSteps(ctx, run.ID)
	if out.Status != store.RunSucceeded || len(w.google.Sent) != 1 {
		t.Fatalf("after approval: %+v sent=%d\n%s", final, len(w.google.Sent), dump(steps))
	}
	if !strings.Contains(step(steps, "after").Logs, "sent m-sent") {
		t.Fatalf("the step after the approval did not see the result: %q", step(steps, "after").Logs)
	}
	if n := strings.Count(dump(steps), "before "); n != 1 {
		t.Fatalf("the step before the approval ran %d times", n)
	}

	// Rejection cancels.
	out, run, _ = w.run(a, "manual", nil)
	pending, _ = w.store.ListApprovals(ctx, store.ApprovalFilter{RunID: run.ID})
	if _, err := w.store.Decide(ctx, pending[0].ID, store.ApprovalRejected, "boss@example.com", "not now", time.Now()); err != nil {
		t.Fatal(err)
	}
	final, _ = w.store.GetRun(ctx, run.ID)
	if out.Status != store.RunWaiting || final.Status != store.RunCancelled || !strings.Contains(final.Error, "not now") || len(w.google.Sent) != 1 {
		t.Fatalf("after rejection: %+v", final)
	}
}

func TestExplicitApprovalStep(t *testing.T) {
	w := newWorld(t)
	a := inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: gate }
triggers: [ { manual: {} } ]
steps:
  - { id: check, type: approval, message: "Publish the article?", approvers: [admin] }
  - { id: publish, type: shell, run: 'echo "approved by $BY"', env: { BY: "${steps.check.output.by}" } }
`)
	ctx := context.Background()
	_, run, _ := w.run(a, "manual", nil)
	pending, _ := w.store.ListApprovals(ctx, store.ApprovalFilter{RunID: run.ID})
	if len(pending) != 1 || pending[0].Approvers[0] != "admin" || pending[0].Message != "Publish the article?" {
		t.Fatalf("approvals = %+v", pending)
	}
	_, _ = w.store.Decide(ctx, pending[0].ID, store.ApprovalApproved, "ceo@example.com", "", time.Now())
	resumed, _ := w.store.Claim(ctx, "w", time.Minute, time.Now())
	if out := w.eng.Execute(ctx, resumed, a); out.Status != store.RunSucceeded {
		t.Fatalf("outcome %+v", out)
	}
	steps, _ := w.store.ListSteps(ctx, run.ID)
	if !strings.Contains(step(steps, "publish").Logs, "approved by ceo@example.com") || step(steps, "check").Status != store.StepSucceeded {
		t.Fatalf("steps:\n%s\n%q", dump(steps), step(steps, "publish").Logs)
	}
}

func withFor(action string) string {
	if action == "gmail.apply_labels" {
		return `{ messageId: m1, labels: [l] }`
	}
	return `{ messageId: m1, body: "x" }`
}

func TestAutonomyLevels(t *testing.T) {
	w := newWorld(t, &testfakes.Message{ID: "m1", ThreadID: "t1", From: "a@b.it", Subject: "Hi", Body: "hello", MessageID: "<x@b.it>"})
	manifestFor := func(autonomy, action string) engine.Automation {
		return inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: a }
triggers: [ { manual: {} } ]
autonomy: `+autonomy+`
steps:
  - { id: act, type: connector, action: `+action+`, with: `+withFor(action)+` }
`)
	}
	// draft: a send becomes a draft.
	out, _, steps := w.run(manifestFor("draft", "gmail.send"), "manual", nil)
	if out.Status != store.RunSucceeded || len(w.google.Sent) != 0 || len(w.google.Drafts) != 1 ||
		!strings.Contains(string(step(steps, "act").Input), `"draftInsteadOf":"gmail.send"`) {
		t.Fatalf("draft: %+v sent=%d drafts=%d %s", out, len(w.google.Sent), len(w.google.Drafts), step(steps, "act").Input)
	}
	// observe: writes are blocked.
	out, run, _ := w.run(manifestFor("observe", "gmail.apply_labels"), "manual", nil)
	if out.Status != store.RunFailed || !strings.Contains(run.Error, `autonomy "observe"`) {
		t.Fatalf("observe: %+v", run)
	}
	// auto: sends.
	out, _, _ = w.run(manifestFor("auto", "gmail.send"), "manual", nil)
	if out.Status != store.RunSucceeded || len(w.google.Sent) != 1 {
		t.Fatalf("auto: %+v sent=%d", out, len(w.google.Sent))
	}
	// The workspace cap wins over the manifest: auto in an "observe" workspace.
	_ = w.store.SetWorkspaceSettings(context.Background(), store.WorkspaceSettings{ID: "default", Name: "Default", MaxAutonomy: "observe"})
	out, run, _ = w.run(manifestFor("auto", "gmail.send"), "manual", nil)
	if out.Status != store.RunFailed || len(w.google.Sent) != 1 || !strings.Contains(run.Error, "observe") {
		t.Fatalf("workspace cap: %+v sent=%d", run, len(w.google.Sent))
	}
	// MCP destructive tools wait for approval even with auto.
	_ = w.store.SetWorkspaceSettings(context.Background(), store.WorkspaceSettings{ID: "default", Name: "Default", MaxAutonomy: "auto"})
	out, _, _ = w.run(inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: a }
triggers: [ { manual: {} } ]
autonomy: auto
steps:
  - { id: del, type: connector, action: openseo.delete_project, with: { seed: x } }
`), "manual", nil)
	if out.Status != store.RunWaiting {
		t.Fatalf("destructive MCP tool did not wait: %+v", out)
	}
}

// An agent cannot use tools beyond its autonomy, and data that reads
// "secret://..." in its arguments stays text.
func TestAgentGuardrails(t *testing.T) {
	w := newWorld(t, &testfakes.Message{ID: "m1", ThreadID: "t1", From: "a@b.it", Subject: "Hi", Body: "hello", MessageID: "<x@b.it>"})
	w.eng.Secrets = secrets.Static{"api-key": "TOPSECRET"}
	w.llm.Turns = []llm.ChatMessage{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "gmail__send", Input: map[string]any{"messageId": "m1", "body": "secret://api-key"}},
			{ID: "2", Name: "slack__send_message", Input: map[string]any{"channel": "#x", "text": "secret://api-key"}},
		}},
		{Role: "assistant", Text: "Done."},
	}
	a := inline(t, `apiVersion: pendolo/v1
kind: Automation
metadata: { name: agent }
triggers: [ { manual: {} } ]
autonomy: draft
budget: { maxCostEur: 1 }
steps:
  - id: work
    type: agent
    promptText: Reply to the email.
    tools: [gmail.send, slack.send_message]
`)
	out, run, steps := w.run(a, "manual", nil)
	if out.Status != store.RunSucceeded {
		t.Fatalf("run %+v\n%s", run, dump(steps))
	}
	if len(w.google.Sent) != 0 || len(w.google.Drafts) != 1 || len(w.slack.Sent()) != 0 {
		t.Fatalf("sent=%d drafts=%d slack=%d", len(w.google.Sent), len(w.google.Drafts), len(w.slack.Sent()))
	}
	if strings.Contains(w.google.Drafts[0], "TOPSECRET") || !strings.Contains(w.google.Drafts[0], "secret://api-key") {
		t.Fatalf("a secret leaked into the draft:\n%s", w.google.Drafts[0])
	}
	results := w.llm.SeenChat[1].Messages[2].ToolResults
	if !results[1].IsError || !strings.Contains(results[1].Content, "only allows drafts") {
		t.Fatalf("slack result = %+v", results[1])
	}
}
