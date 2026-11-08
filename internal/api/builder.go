package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/builder"
	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/templates"
)

// builderTurnTimeout bounds one conversation turn, repairs included.
const builderTurnTimeout = 5 * time.Minute

// dryRunTimeout bounds a dry run when the manifest sets no budget.timeout.
const dryRunTimeout = 15 * time.Minute

type templateInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type builderStatus struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	// Reason says why the builder is off and how to turn it on.
	Reason    string         `json:"reason,omitempty"`
	Templates []templateInfo `json:"templates"`
	// CanActivate tells whether the caller may activate drafts.
	CanActivate bool `json:"canActivate"`
}

func templateList() []templateInfo {
	out := []templateInfo{}
	for _, name := range templates.Names() {
		info := templateInfo{Name: name}
		if f := templates.Files(name); f != nil {
			res := manifest.Validate([]byte(f[manifest.FileName]), manifest.Options{})
			if res.Manifest != nil {
				info.Description = res.Manifest.Metadata.Description
			}
		}
		out = append(out, info)
	}
	return out
}

func (a *API) getBuilder(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	st := builderStatus{Templates: templateList()}
	role, _ := a.roleIn(r.Context(), p, ws)
	st.CanActivate = can(role, permActivate)
	switch {
	case a.Builder != nil:
		st.Enabled, st.Model = true, a.Builder.Model
	case a.Config.Builder.Disabled:
		st.Reason = "The AI builder is turned off (builder.disabled in the configuration). You can still start from a template and edit the files by hand."
	default:
		st.Reason = fmt.Sprintf("No model named %q is configured. Add it under models in the configuration, or set builder.model. You can still start from a template and edit the files by hand.", a.Config.Builder.Model)
	}
	writeJSON(w, http.StatusOK, st)
}

// builderWorkspace gathers what the builder needs to know about ws.
func (a *API) builderWorkspace(ctx context.Context, ws, baseName string) (builder.Workspace, error) {
	out := builder.Workspace{ID: ws, BaseName: baseName, Taken: map[string]bool{}}
	var err error
	if out.MaxAutonomy, err = a.Store.WorkspaceAutonomy(ctx, ws); err != nil {
		return out, err
	}
	conns, err := a.Store.ListConnections(ctx, ws)
	if err != nil {
		return out, err
	}
	for i := range conns {
		c := builder.Connection{Name: conns[i].Name, Kind: conns[i].Kind, Status: conns[i].Status}
		if a.Hub != nil {
			actx, cancel := context.WithTimeout(ctx, 15*time.Second)
			c.Actions = a.Hub.Actions(actx, &conns[i])
			cancel()
		}
		out.Connections = append(out.Connections, c)
	}
	secs, err := a.Store.ListSecrets(ctx, ws)
	if err != nil {
		return out, err
	}
	for _, s := range secs {
		out.Secrets = append(out.Secrets, s.Name)
	}
	for m := range a.Config.Models {
		out.Models = append(out.Models, m)
	}
	sort.Strings(out.Models)
	for _, n := range a.Snapshot().Names() {
		out.Taken[n] = true
	}
	return out, nil
}

func (a *API) tempDir() string { return filepath.Join(a.Config.DataDir, "tmp") }

// checkDraft validates files for a draft of ws and stores the outcome in d.
func (a *API) checkDraft(ctx context.Context, d *store.Draft) error {
	wsInfo, err := a.builderWorkspace(ctx, d.Workspace, d.BaseName)
	if err != nil {
		return err
	}
	applyCheck(d, builder.Check(builder.Files(d.Files), wsInfo, a.tempDir()))
	return nil
}

func applyCheck(d *store.Draft, res builder.Checked) {
	problems := res.Problems
	if problems == nil {
		problems = []builder.Problem{}
	}
	d.Problems, _ = json.Marshal(problems)
	d.Hash = res.Hash
	if res.Manifest != nil {
		d.Automation = res.Manifest.Metadata.Name
	} else if d.BaseName != "" {
		d.Automation = d.BaseName
	}
}

type draftView struct {
	*store.Draft
	Messages []store.DraftMessage `json:"messages"`
	// BaseFiles are the files of the version the draft edits, for a diff.
	BaseFiles map[string]string `json:"baseFiles,omitempty"`
	// CurrentHash is the automation's version now; when it differs from
	// BaseHash, someone changed the automation since the draft started.
	CurrentHash string     `json:"currentHash,omitempty"`
	DryRun      *store.Run `json:"dryRun,omitempty"`
	// Busy is true while the builder works on the draft.
	Busy bool `json:"busy"`
}

func (a *API) draftIn(w http.ResponseWriter, r *http.Request, ws string) (*store.Draft, bool) {
	d, err := a.Store.GetDraft(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.Workspace != ws) {
		notFound(w, "draft")
		return nil, false
	}
	if err != nil {
		a.internalError(w, err)
		return nil, false
	}
	return d, true
}

func (a *API) viewDraft(ctx context.Context, d *store.Draft) (draftView, error) {
	v := draftView{Draft: d}
	var err error
	if v.Messages, err = a.Store.ListDraftMessages(ctx, d.ID); err != nil {
		return v, err
	}
	if d.BaseName != "" {
		if ver, err := a.Store.GetVersion(ctx, d.BaseName, d.BaseHash); err == nil {
			v.BaseFiles = ver.Files
		}
		if au, inv, ok := a.automationIn(d.Workspace, d.BaseName); ok {
			v.CurrentHash = au.Hash
			if inv != nil {
				v.CurrentHash = inv.Hash
			}
		}
	}
	if d.DryRunID != "" {
		if run, err := a.Store.GetRun(ctx, d.DryRunID); err == nil {
			v.DryRun = run
		}
	}
	_, v.Busy = a.draftBusy.Load(d.ID)
	return v, nil
}

func (a *API) writeDraft(w http.ResponseWriter, r *http.Request, d *store.Draft, status int) {
	v, err := a.viewDraft(r.Context(), d)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, status, v)
}

func (a *API) listDrafts(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "all", store.DraftOpen, store.DraftProposed, store.DraftActivated, store.DraftDiscarded:
	default:
		badRequest(w, "status must be open, proposed, activated, discarded or all")
		return
	}
	drafts, err := a.Store.ListDrafts(r.Context(), ws, status, 100)
	if err != nil {
		a.internalError(w, err)
		return
	}
	for i := range drafts {
		drafts[i].Files = nil // the list stays small; GET one draft for its files
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": drafts})
}

type createDraftInput struct {
	// Message starts the conversation with a request to the builder.
	Message string `json:"message"`
	// Template starts from a bundled template.
	Template string `json:"template"`
	// Automation starts from an existing automation of the workspace, to edit it.
	Automation string `json:"automation"`
	// Version, with Automation, starts from an earlier version, to restore it.
	Version string `json:"version"`
}

var metadataLineRe = regexp.MustCompile(`(?m)^metadata:[ \t]*\n`)

// withWorkspace adds metadata.workspace to a template manifest when the
// draft is for a workspace other than the default.
func withWorkspace(yaml, ws string) string {
	if ws == store.DefaultWorkspace || strings.Contains(yaml, "\n  workspace:") {
		return yaml
	}
	loc := metadataLineRe.FindStringIndex(yaml)
	if loc == nil {
		return yaml
	}
	return yaml[:loc[1]] + "  workspace: " + ws + "\n" + yaml[loc[1]:]
}

func (a *API) createDraft(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in createDraftInput
	if !decode(w, r, &in) {
		return
	}
	in.Message = strings.TrimSpace(in.Message)
	ctx := r.Context()
	d := &store.Draft{Workspace: ws, CreatedBy: p.label(), Files: map[string]string{}}
	switch {
	case in.Template != "" && in.Automation != "":
		badRequest(w, "give template or automation, not both")
		return
	case in.Template != "":
		files := templates.Files(in.Template)
		if files == nil {
			badRequest(w, "no template named %q; see GET .../builder for the list", in.Template)
			return
		}
		files[manifest.FileName] = withWorkspace(files[manifest.FileName], ws)
		d.Files = files
	case in.Automation != "":
		au, inv, ok := a.automationIn(ws, in.Automation)
		if !ok {
			notFound(w, "automation")
			return
		}
		dir, hash := au.Dir, au.Hash
		if inv != nil {
			dir, hash = inv.Path, inv.Hash
		}
		d.BaseName, d.BaseHash = in.Automation, hash
		if in.Version != "" {
			ver, err := a.Store.GetVersion(ctx, in.Automation, in.Version)
			if errors.Is(err, store.ErrNotFound) {
				notFound(w, "version")
				return
			} else if err != nil {
				a.internalError(w, err)
				return
			}
			d.Files = ver.Files
		} else {
			files, err := builder.ReadFolder(dir)
			if err != nil {
				writeError(w, http.StatusConflict, "not_editable", fmt.Sprintf("%s cannot be edited here: %v. Edit its folder directly.", in.Automation, err))
				return
			}
			d.Files = files
		}
		// Keep the base version in the history, for the diff.
		if base, err := builder.ReadFolder(dir); err == nil {
			_, _ = a.Store.AddVersion(ctx, store.Version{Automation: in.Automation, Hash: hash, Workspace: ws, Files: base, Source: store.VersionFolder}, a.Now())
		}
	case in.Version != "":
		badRequest(w, "version needs automation")
		return
	case in.Message == "":
		badRequest(w, "describe the automation in message, or start from a template or an automation")
		return
	}
	if in.Message != "" && a.Builder == nil {
		writeError(w, http.StatusServiceUnavailable, "builder_off", "the AI builder has no model configured; start from a template instead")
		return
	}
	if len(d.Files) > 0 {
		if err := a.checkDraft(ctx, d); err != nil {
			a.internalError(w, err)
			return
		}
	}
	now := a.Now()
	if err := a.Store.CreateDraft(ctx, d, now); err != nil {
		a.internalError(w, err)
		return
	}
	switch {
	case in.Template != "":
		_ = a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: "Started from the template " + in.Template + ".", Hash: d.Hash}, now)
	case in.Version != "":
		_ = a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: fmt.Sprintf("Started from version %s of %s, to restore it.", in.Version, in.Automation), Hash: d.Hash}, now)
	case in.Automation != "":
		_ = a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: "Started from the current version of " + in.Automation + ".", Hash: d.Hash}, now)
	}
	a.audit(r, p, "draft.create", ws, d.ID, map[string]any{"template": in.Template, "automation": in.Automation, "version": in.Version})
	if in.Message == "" {
		a.writeDraft(w, r, d, http.StatusCreated)
		return
	}
	a.converse(w, r, p, d, in.Message, http.StatusCreated)
}

func (a *API) getDraft(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	if d, ok := a.draftIn(w, r, ws); ok {
		a.writeDraft(w, r, d, http.StatusOK)
	}
}

func editable(w http.ResponseWriter, d *store.Draft) bool {
	if d.Status != store.DraftOpen && d.Status != store.DraftProposed {
		writeError(w, http.StatusConflict, "draft_closed", "this draft is "+d.Status+"; start a new draft to make changes")
		return false
	}
	return true
}

func (a *API) postDraftMessage(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" {
		badRequest(w, "text is empty: say what the automation should do or change")
		return
	}
	if len(in.Text) > 20000 {
		badRequest(w, "text is too long: keep it under 20,000 characters")
		return
	}
	d, ok := a.draftIn(w, r, ws)
	if !ok || !editable(w, d) {
		return
	}
	if a.Builder == nil {
		writeError(w, http.StatusServiceUnavailable, "builder_off", "the AI builder has no model configured; edit the files by hand instead")
		return
	}
	a.converse(w, r, p, d, in.Text, http.StatusOK)
}

// converse runs one builder turn on a draft and answers with the draft.
func (a *API) converse(w http.ResponseWriter, r *http.Request, p *principal, d *store.Draft, text string, status int) {
	if _, busy := a.draftBusy.LoadOrStore(d.ID, struct{}{}); busy {
		writeError(w, http.StatusConflict, "busy", "the builder is already working on this draft; wait for its answer")
		return
	}
	defer a.draftBusy.Delete(d.ID)

	// The turn outlives a closed browser tab: what it writes is kept.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), builderTurnTimeout)
	defer cancel()
	history, err := a.Store.ListDraftMessages(ctx, d.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleUser, Author: p.label(), Text: text}, a.Now()); err != nil {
		a.internalError(w, err)
		return
	}
	wsInfo, err := a.builderWorkspace(ctx, d.Workspace, d.BaseName)
	if err != nil {
		a.internalError(w, err)
		return
	}
	res, runErr := a.Builder.Run(ctx, builder.Turn{Workspace: wsInfo, History: history, Files: builder.Files(d.Files), Message: text})
	now := a.Now()
	for _, c := range res.Calls {
		bc := store.BuilderCall{DraftID: d.ID, Workspace: d.Workspace, Provider: c.Provider, Model: c.Model,
			InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, CostEur: c.CostEur, PriceKnown: c.PriceKnown,
			LatencyMs: c.Latency.Milliseconds()}
		if c.Err != nil {
			bc.Error = c.Err.Error()
		}
		if err := a.Store.AddBuilderCall(ctx, bc, now); err != nil {
			a.Log.Error("record builder call", "draft", d.ID, "err", err)
		}
	}
	if res.CostEur > 0 {
		_ = a.Store.AddDraftCost(ctx, d.ID, res.CostEur)
	}
	if runErr != nil {
		msg := "The builder could not answer: " + runErr.Error()
		_ = a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleEvent, Text: msg}, now)
		a.Log.Warn("builder turn failed", "draft", d.ID, "err", runErr)
		writeError(w, http.StatusBadGateway, "builder_failed", msg+". Try again, or rephrase the request.")
		return
	}
	if len(res.Files) > 0 {
		d.Files = res.Files
		applyCheck(d, res.Checked)
		if err := a.Store.SaveDraftContent(ctx, d, now); err != nil {
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, "draft_closed", "the draft was activated or discarded meanwhile")
				return
			}
			a.internalError(w, err)
			return
		}
	}
	reply := res.Reply
	if reply == "" {
		reply = "Done."
	}
	if err := a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleAssistant, Text: reply, Hash: d.Hash}, now); err != nil {
		a.internalError(w, err)
		return
	}
	fresh, err := a.Store.GetDraft(ctx, d.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.writeDraft(w, r, fresh, status)
}

func (a *API) putDraftFiles(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in struct {
		Files map[string]string `json:"files"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, ok := a.draftIn(w, r, ws)
	if !ok || !editable(w, d) {
		return
	}
	if _, busy := a.draftBusy.Load(d.ID); busy {
		writeError(w, http.StatusConflict, "busy", "the builder is working on this draft; wait for its answer")
		return
	}
	if in.Files == nil {
		in.Files = map[string]string{}
	}
	d.Files = in.Files
	if err := a.checkDraft(r.Context(), d); err != nil {
		a.internalError(w, err)
		return
	}
	now := a.Now()
	if err := a.Store.SaveDraftContent(r.Context(), d, now); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "draft_closed", "the draft was activated or discarded meanwhile")
			return
		}
		a.internalError(w, err)
		return
	}
	_ = a.Store.AddDraftMessage(r.Context(), d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: p.label() + " edited the files by hand.", Hash: d.Hash}, now)
	fresh, err := a.Store.GetDraft(r.Context(), d.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.writeDraft(w, r, fresh, http.StatusOK)
}

func draftProblems(d *store.Draft) []builder.Problem {
	var ps []builder.Problem
	_ = json.Unmarshal(d.Problems, &ps)
	return ps
}

func requireClean(w http.ResponseWriter, d *store.Draft, what string) bool {
	if len(d.Files) == 0 {
		writeError(w, http.StatusConflict, "empty_draft", "the draft has no files yet; ask the builder for an automation first")
		return false
	}
	for _, p := range draftProblems(d) {
		if p.Severity == manifest.SeverityError {
			writeError(w, http.StatusConflict, "draft_invalid", fmt.Sprintf("fix the draft's errors before you %s: %s", what, p))
			return false
		}
	}
	return true
}

func (a *API) dryRunDraft(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in struct {
		// TriggerData is the sample input of the run, as trigger.* in expressions.
		TriggerData map[string]any `json:"triggerData"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	d, ok := a.draftIn(w, r, ws)
	if !ok || !editable(w, d) || !requireClean(w, d, "a dry run") {
		return
	}
	if a.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "dry runs need the server's engine")
		return
	}
	if d.DryRunID != "" {
		if run, err := a.Store.GetRun(r.Context(), d.DryRunID); err == nil && run.Status == store.RunRunning {
			writeError(w, http.StatusConflict, "busy", "a dry run of this draft is still in progress")
			return
		}
	}
	tmp := filepath.Join(a.tempDir(), "dry-"+d.ID+"-"+store.NewID(a.Now())[:10])
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		a.internalError(w, err)
		return
	}
	if err := builder.Write(tmp, builder.Files(d.Files)); err != nil {
		_ = os.RemoveAll(tmp)
		a.internalError(w, err)
		return
	}
	au, inv := registry.LoadFolder(tmp)
	if inv != nil {
		_ = os.RemoveAll(tmp)
		writeError(w, http.StatusConflict, "draft_invalid", "the draft does not validate: "+inv.Problems[0].String())
		return
	}
	au.Name = d.Automation
	trigger := in.TriggerData
	note := ""
	if trigger == nil {
		trigger, note = a.sampleTrigger(r.Context(), au)
	}
	run, err := a.Store.StartNow(r.Context(), store.NewRun{Automation: au.Name, Workspace: ws, ManifestHash: d.Hash,
		TriggerType: engine.TriggerDryRun, TriggerData: trigger}, "dry-run", a.Now())
	if err != nil {
		_ = os.RemoveAll(tmp)
		a.internalError(w, err)
		return
	}
	if err := a.Store.SetDraftDryRun(r.Context(), d.ID, run.ID, d.Hash, a.Now()); err != nil {
		a.Log.Error("link dry run", "draft", d.ID, "err", err)
	}
	text := p.label() + " started a dry run."
	if note != "" {
		text += " " + note
	}
	_ = a.Store.AddDraftMessage(r.Context(), d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: text, Hash: d.Hash}, a.Now())
	a.audit(r, p, "draft.dry_run", ws, d.ID, map[string]any{"run": run.ID})
	// The dry run outlives this request; the server's lifetime bounds it.
	bg := context.WithoutCancel(r.Context())
	go func() {
		defer os.RemoveAll(tmp)
		ctx, cancel := context.WithTimeout(bg, dryRunTimeout)
		defer cancel()
		a.Engine.Execute(ctx, run, au)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": run.ID, "note": note})
}

// sampleTrigger returns example trigger data for a dry run: the newest
// matching email for an email trigger, nothing otherwise.
func (a *API) sampleTrigger(ctx context.Context, au engine.Automation) (map[string]any, string) {
	for _, t := range au.Manifest.Triggers {
		if t.Email == nil || a.Hub == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		conn, err := a.Hub.GoogleConnection(ctx, au.Workspace(), t.Email.Connection)
		if err != nil {
			return nil, "No sample email: " + err.Error() + "."
		}
		query := t.Email.Query
		if query == "" {
			query = "in:inbox"
		}
		ids, err := a.Hub.GmailList(ctx, conn, query, 1)
		if err != nil || len(ids) == 0 {
			return nil, "No email matches the trigger's query, so the run has no sample email."
		}
		email, err := a.Hub.GmailMessage(ctx, conn, ids[0])
		if err != nil {
			return nil, "No sample email: " + err.Error() + "."
		}
		return connectors.EmailTriggerData(email), "It uses the newest matching email as its input."
	}
	return nil, ""
}

func (a *API) proposeDraft(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	d, ok := a.draftIn(w, r, ws)
	if !ok || !requireClean(w, d, "propose it") {
		return
	}
	if err := a.Store.SetDraftStatus(r.Context(), d.ID, store.DraftProposed, p.label(), "", []string{store.DraftOpen}, a.Now()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "draft_closed", "only an open draft can be proposed; this one is "+d.Status)
			return
		}
		a.internalError(w, err)
		return
	}
	_ = a.Store.AddDraftMessage(r.Context(), d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: p.label() + " proposed this draft for activation.", Hash: d.Hash}, a.Now())
	a.audit(r, p, "draft.propose", ws, d.ID, map[string]any{"automation": d.Automation, "hash": d.Hash})
	fresh, _ := a.Store.GetDraft(r.Context(), d.ID)
	a.writeDraft(w, r, fresh, http.StatusOK)
}

func (a *API) discardDraft(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	d, ok := a.draftIn(w, r, ws)
	if !ok {
		return
	}
	if err := a.Store.SetDraftStatus(r.Context(), d.ID, store.DraftDiscarded, p.label(), in.Comment, []string{store.DraftOpen, store.DraftProposed}, a.Now()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "draft_closed", "this draft is already "+d.Status)
			return
		}
		a.internalError(w, err)
		return
	}
	text := p.label() + " discarded this draft."
	if in.Comment != "" {
		text += " " + in.Comment
	}
	_ = a.Store.AddDraftMessage(r.Context(), d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: text, Hash: d.Hash}, a.Now())
	a.audit(r, p, "draft.discard", ws, d.ID, nil)
	fresh, _ := a.Store.GetDraft(r.Context(), d.ID)
	a.writeDraft(w, r, fresh, http.StatusOK)
}

func (a *API) activateDraft(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var in struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	d, ok := a.draftIn(w, r, ws)
	if !ok || !editable(w, d) {
		return
	}
	if _, busy := a.draftBusy.LoadOrStore(d.ID, struct{}{}); busy {
		writeError(w, http.StatusConflict, "busy", "the builder is working on this draft; wait for its answer")
		return
	}
	defer a.draftBusy.Delete(d.ID)
	ctx := r.Context()
	// Check again: connections, secrets and names may have changed.
	if err := a.checkDraft(ctx, d); err != nil {
		a.internalError(w, err)
		return
	}
	if !requireClean(w, d, "activate it") {
		_ = a.Store.SaveDraftContent(ctx, d, a.Now())
		return
	}

	folder, replace := d.Automation, false
	if d.BaseName != "" {
		au, inv, ok := a.automationIn(ws, d.BaseName)
		if !ok {
			writeError(w, http.StatusConflict, "stale", d.BaseName+" no longer exists; start a new draft to create it again")
			return
		}
		dir, hash := au.Dir, au.Hash
		if inv != nil {
			dir, hash = inv.Path, inv.Hash
		}
		if hash != d.BaseHash {
			writeError(w, http.StatusConflict, "stale", fmt.Sprintf("%s changed since this draft started (now version %s, draft based on %s); start a new draft from the current version", d.BaseName, hash, d.BaseHash))
			return
		}
		folder, replace = filepath.Base(dir), true
	}
	dir, err := builder.Install(a.Config.AutomationsDir, folder, builder.Files(d.Files), replace)
	switch {
	case errors.Is(err, builder.ErrFolderExists):
		writeError(w, http.StatusConflict, "folder_exists", fmt.Sprintf("the automations folder already has a folder named %s; rename the automation or remove that folder", folder))
		return
	case err != nil:
		a.Log.Error("install automation", "draft", d.ID, "err", err)
		writeError(w, http.StatusConflict, "not_writable", "could not write the automation to the automations folder: "+err.Error()+". The server needs write access to it (see docs/running.md).")
		return
	}
	au, inv := registry.LoadFolder(dir)
	hash := d.Hash
	if inv == nil {
		hash = au.Hash
	} else {
		a.Log.Error("activated automation does not validate", "automation", d.Automation, "problems", inv.Problems)
	}
	now := a.Now()
	if _, err := a.Store.AddVersion(ctx, store.Version{Automation: d.Automation, Hash: hash, Workspace: ws, Files: d.Files,
		Source: store.VersionBuilder, DraftID: d.ID, ActivatedBy: p.label()}, now); err != nil {
		a.Log.Error("record version", "automation", d.Automation, "err", err)
	}
	if err := a.Store.SetDraftStatus(ctx, d.ID, store.DraftActivated, p.label(), in.Comment, []string{store.DraftOpen, store.DraftProposed}, now); err != nil {
		a.Log.Error("mark draft activated", "draft", d.ID, "err", err)
	}
	text := p.label() + " activated this draft as version " + hash + "."
	if in.Comment != "" {
		text += " " + in.Comment
	}
	_ = a.Store.AddDraftMessage(ctx, d.ID, store.DraftMessage{Role: store.RoleEvent, Author: p.label(), Text: text, Hash: hash}, now)
	a.audit(r, p, "automation.activate", ws, d.Automation, map[string]any{"draft": d.ID, "version": hash, "proposedBy": d.ProposedBy})
	if a.Reload != nil {
		if err := a.Reload(ctx); err != nil {
			a.Log.Error("reload after activation", "err", err)
		}
	}
	fresh, _ := a.Store.GetDraft(ctx, d.ID)
	a.writeDraft(w, r, fresh, http.StatusOK)
}

func (a *API) listVersions(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	name := r.PathValue("name")
	au, inv, ok := a.automationIn(ws, name)
	if !ok {
		notFound(w, "automation")
		return
	}
	current := au.Hash
	if inv != nil {
		current = inv.Hash
	}
	all, err := a.Store.ListVersions(r.Context(), name)
	if err != nil {
		a.internalError(w, err)
		return
	}
	versions := []store.Version{}
	for _, v := range all {
		if v.Workspace == ws {
			versions = append(versions, v)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": current, "versions": versions})
}

func (a *API) getVersion(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	name := r.PathValue("name")
	if _, _, ok := a.automationIn(ws, name); !ok {
		notFound(w, "automation")
		return
	}
	v, err := a.Store.GetVersion(r.Context(), name, r.PathValue("hash"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.Workspace != ws) {
		notFound(w, "version")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
