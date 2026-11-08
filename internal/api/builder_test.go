package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/builder"
	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/testfakes"
)

type draftResp struct {
	ID          string
	Status      string
	Automation  string
	BaseHash    string
	Hash        string
	Files       map[string]string
	Problems    []builder.Problem
	Messages    []store.DraftMessage
	BaseFiles   map[string]string
	CurrentHash string
	Busy        bool
}

func answerJSON(t *testing.T, reply string, files map[string]string) string {
	t.Helper()
	type file struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	var fs []file
	for p, c := range files {
		fs = append(fs, file{p, c})
	}
	b, err := json.Marshal(map[string]any{"reply": reply, "files": fs})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// withBuilder gives the env a builder that answers with script, and an
// engine for dry runs.
func (e *env) withBuilder(script *testfakes.Script) {
	gw := llm.NewWith(map[string]llm.Provider{"fake": script}, map[string]config.Model{"smart": {Provider: "fake", Model: "fake-1"}})
	e.api.Builder = &builder.Builder{LLM: gw, Model: "smart", TempDir: filepath.Join(e.api.Config.DataDir, "tmp")}
	e.api.Config.Models = map[string]config.Model{"smart": {Provider: "fake", Model: "fake-1"}}
	// Tests serve "public" sites on localhost.
	e.api.Engine = &engine.Engine{Store: e.store, LLM: gw, Connectors: e.api.Hub, Log: e.api.Log, DryRunHTTP: http.DefaultClient}
}

func waitRun(t *testing.T, st *store.Store, id string) *store.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(context.Background(), id)
		if err == nil && run.Status != store.RunRunning && run.Status != store.RunQueued {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", id)
	return nil
}

func TestBuilderSentenceToActiveAutomation(t *testing.T) {
	var gets, posts atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		} else {
			gets.Add(1)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer site.Close()

	good := `apiVersion: pendolo/v1
kind: Automation
metadata: { name: site-watch, workspace: ws-a, description: "Checks the site every morning." }
triggers: [ { schedule: { cron: "0 8 * * *", timezone: Europe/Rome } } ]
autonomy: observe
steps:
  - { id: home, type: http, request: { url: "` + site.URL + `" }, expect: { status: [200] } }
  - { id: ping, type: http, request: { method: POST, url: "` + site.URL + `/hook" } }
`
	// The first answer has an error, so the builder must repair it.
	bad := strings.Replace(good, "type: http, request: { url", "type: htttp, request: { url", 1)
	script := &testfakes.Script{Answers: []string{
		answerJSON(t, "Ho creato un controllo del sito.", map[string]string{"automation.yaml": bad}),
		answerJSON(t, "Ho creato un controllo del sito ogni mattina alle 8.", map[string]string{"automation.yaml": good}),
	}}
	e := newEnv(t)
	e.withBuilder(script)
	owner := e.setupOwner()
	bld := e.userWithRole(owner, "builder@example.com", "ws-a", "builder")

	// A sentence becomes a draft.
	var d draftResp
	if code := bld.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"message": "Controlla il sito ogni mattina alle 8"}, &d); code != 201 {
		t.Fatalf("create = %d", code)
	}
	if d.Automation != "site-watch" || d.Status != store.DraftOpen || d.Busy || builder.HasErrors(d.Problems) || d.Files["automation.yaml"] != good {
		t.Fatalf("draft = %+v", d)
	}
	if len(script.SeenComplete) != 2 || !strings.Contains(script.SeenComplete[1].User, "Pendolo found problems") {
		t.Fatalf("calls = %d", len(script.SeenComplete))
	}
	if len(d.Messages) != 2 || d.Messages[0].Role != store.RoleUser || d.Messages[1].Text != "Ho creato un controllo del sito ogni mattina alle 8." {
		t.Fatalf("messages = %+v", d.Messages)
	}
	base := "/api/v1/workspaces/ws-a/drafts/" + d.ID

	// A dry run reads but does not write.
	var dr struct{ RunID string }
	if code := bld.do("POST", base+"/dry-run", nil, &dr); code != 202 {
		t.Fatalf("dry run = %d", code)
	}
	run := waitRun(t, e.store, dr.RunID)
	if run.Status != store.RunSucceeded || run.TriggerType != engine.TriggerDryRun || gets.Load() != 1 || posts.Load() != 0 {
		t.Fatalf("dry run = %+v, gets %d, posts %d", run, gets.Load(), posts.Load())
	}
	steps, _ := e.store.ListSteps(context.Background(), run.ID)
	if len(steps) != 2 || !strings.Contains(steps[1].Logs, "dry run") {
		t.Fatalf("steps = %+v", steps)
	}
	var ov struct{ Runs24h struct{ Succeeded int } }
	owner.do("GET", "/api/v1/workspaces/ws-a/overview", nil, &ov)
	if ov.Runs24h.Succeeded != 0 {
		t.Fatalf("dry runs count in the overview: %+v", ov)
	}

	// A builder proposes; only an admin activates.
	bld.expect("POST", base+"/activate", nil, 403)
	bld.expect("POST", base+"/propose", nil, 200)
	var ov2 struct{ ProposedDrafts int }
	owner.do("GET", "/api/v1/workspaces/ws-a/overview", nil, &ov2)
	if ov2.ProposedDrafts != 1 {
		t.Fatalf("proposed = %d", ov2.ProposedDrafts)
	}
	owner.expect("POST", base+"/activate", map[string]string{"comment": "no hash"}, 400)
	owner.expect("POST", base+"/activate", map[string]string{"hash": "0123456789abcdef"}, 409)
	if code := owner.do("POST", base+"/activate", map[string]string{"hash": d.Hash, "comment": "ok"}, &d); code != 200 || d.Status != store.DraftActivated {
		t.Fatalf("activate = %d %+v", code, d)
	}
	written, err := os.ReadFile(filepath.Join(e.autos, "site-watch", "automation.yaml"))
	if err != nil || string(written) != good {
		t.Fatalf("written = %q, %v", written, err)
	}
	au, ok := e.snapshot().Valid["site-watch"]
	if !ok {
		t.Fatalf("not loaded: %+v", e.snapshot().Invalid)
	}
	var vers struct {
		Current  string
		Versions []store.Version
	}
	owner.do("GET", "/api/v1/workspaces/ws-a/automations/site-watch/versions", nil, &vers)
	if vers.Current != au.Hash || len(vers.Versions) != 1 || vers.Versions[0].Source != store.VersionBuilder || vers.Versions[0].ActivatedBy != "owner@example.com" {
		t.Fatalf("versions = %+v", vers)
	}
	owner.expect("POST", base+"/messages", map[string]string{"text": "more"}, 409)

	// Editing: two drafts from the same version; the second to activate is stale.
	var ed, ed2 draftResp
	bld.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"automation": "site-watch"}, &ed)
	bld.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"automation": "site-watch"}, &ed2)
	if ed.BaseHash != au.Hash || ed.Files["automation.yaml"] != good || len(ed.BaseFiles) != 1 {
		t.Fatalf("edit draft = %+v", ed)
	}
	changed := strings.Replace(good, "0 8 * * *", "0 9 * * *", 1)
	if code := bld.do("PUT", "/api/v1/workspaces/ws-a/drafts/"+ed.ID+"/files", map[string]any{"files": map[string]string{"automation.yaml": changed}}, &ed); code != 200 || builder.HasErrors(ed.Problems) {
		t.Fatalf("edit = %d %+v", code, ed.Problems)
	}
	// Renaming is refused.
	renamed := strings.Replace(changed, "name: site-watch", "name: site-watch-2", 1)
	var rn draftResp
	bld.do("PUT", "/api/v1/workspaces/ws-a/drafts/"+ed2.ID+"/files", map[string]any{"files": map[string]string{"automation.yaml": renamed}}, &rn)
	if !builder.HasErrors(rn.Problems) {
		t.Fatalf("rename accepted: %+v", rn.Problems)
	}
	owner.expect("POST", "/api/v1/workspaces/ws-a/drafts/"+ed2.ID+"/activate", map[string]string{"hash": rn.Hash}, 409)
	owner.expect("POST", "/api/v1/workspaces/ws-a/drafts/"+ed.ID+"/activate", map[string]string{"hash": ed.Hash}, 200)
	owner.do("GET", "/api/v1/workspaces/ws-a/automations/site-watch/versions", nil, &vers)
	if len(vers.Versions) != 2 || vers.Current == au.Hash {
		t.Fatalf("versions after edit = %+v", vers)
	}
	bld.do("PUT", "/api/v1/workspaces/ws-a/drafts/"+ed2.ID+"/files", map[string]any{"files": map[string]string{"automation.yaml": changed}}, &ed2)
	var stale errBody
	if code := owner.do("POST", "/api/v1/workspaces/ws-a/drafts/"+ed2.ID+"/activate", map[string]string{"hash": ed2.Hash}, &stale); code != 409 || !strings.Contains(stale.Error, "changed since") {
		t.Fatalf("stale activate = %d %+v", code, stale)
	}

	// Restoring the first version.
	var rs draftResp
	bld.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"automation": "site-watch", "version": au.Hash}, &rs)
	if rs.Files["automation.yaml"] != good {
		t.Fatalf("restore = %+v", rs)
	}

	// A new draft may not take an existing name.
	var dup draftResp
	bld.do("PUT", "/api/v1/workspaces/ws-a/drafts/"+d.ID+"/files", map[string]any{"files": map[string]string{"automation.yaml": good}}, &dup)
	var nd draftResp
	bld.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"template": "site-checks"}, &nd)
	bld.do("PUT", "/api/v1/workspaces/ws-a/drafts/"+nd.ID+"/files", map[string]any{"files": map[string]string{"automation.yaml": good}}, &nd)
	found := false
	for _, p := range nd.Problems {
		found = found || strings.Contains(p.Message, "already exists")
	}
	if !found {
		t.Fatalf("problems = %+v", nd.Problems)
	}

	// The builder's model calls count in the costs.
	var costs struct{ ByModel []store.ModelCost }
	owner.do("GET", "/api/v1/workspaces/ws-a/costs", nil, &costs)
	if len(costs.ByModel) != 1 || costs.ByModel[0].Calls != 2 {
		t.Fatalf("costs = %+v", costs)
	}
	// Discarding closes a draft.
	bld.expect("POST", "/api/v1/workspaces/ws-a/drafts/"+nd.ID+"/discard", nil, 200)
	bld.expect("POST", "/api/v1/workspaces/ws-a/drafts/"+nd.ID+"/propose", nil, 409)
}

func TestBuilderTemplatesAndNoModel(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	var st builderStatus
	owner.do("GET", "/api/v1/workspaces/ws-a/builder", nil, &st)
	if st.Enabled || st.Reason == "" || len(st.Templates) < 4 || !st.CanActivate {
		t.Fatalf("status = %+v", st)
	}
	owner.expect("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"message": "hello"}, 503)
	for _, tpl := range st.Templates {
		var d draftResp
		if code := owner.do("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"template": tpl.Name}, &d); code != 201 {
			t.Fatalf("%s: %d", tpl.Name, code)
		}
		if builder.HasErrors(d.Problems) || !strings.Contains(d.Files["automation.yaml"], "workspace: ws-a") {
			t.Fatalf("%s: %+v", tpl.Name, d.Problems)
		}
	}
	owner.expect("POST", "/api/v1/workspaces/ws-a/drafts", map[string]string{"template": "nope"}, 400)
}
