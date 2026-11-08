package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

type triggerSummary struct {
	Type        string `json:"type"`
	Cron        string `json:"cron,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	WebhookPath string `json:"webhookPath,omitempty"`
	HasSecret   bool   `json:"hasSecret,omitempty"`
}

type automationSummary struct {
	Name        string             `json:"name"`
	Workspace   string             `json:"workspace"`
	Description string             `json:"description"`
	Path        string             `json:"path"`
	Valid       bool               `json:"valid"`
	Enabled     bool               `json:"enabled"`
	Problems    []manifest.Problem `json:"problems"`
	Triggers    []triggerSummary   `json:"triggers"`
	NextRunAt   *time.Time         `json:"nextRunAt,omitempty"`
	LastRun     *store.Run         `json:"lastRun,omitempty"`
}

func triggersOf(a engine.Automation) []triggerSummary {
	out := []triggerSummary{}
	for _, t := range a.Manifest.Triggers {
		switch {
		case t.Schedule != nil:
			out = append(out, triggerSummary{Type: "schedule", Cron: t.Schedule.Cron, Timezone: t.Schedule.Timezone})
		case t.Webhook != nil:
			out = append(out, triggerSummary{Type: "webhook", WebhookPath: "/hooks/" + a.Name, HasSecret: t.Webhook.Secret != ""})
		case t.Email != nil:
			out = append(out, triggerSummary{Type: "email"})
		case t.Manual != nil:
			out = append(out, triggerSummary{Type: "manual"})
		}
	}
	return out
}

// summaries lists the automations of a workspace, valid and invalid.
func (a *API) summaries(r *http.Request, ws string) ([]automationSummary, error) {
	ctx := r.Context()
	stored, err := a.Store.ListAutomations(ctx)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, s := range stored {
		enabled[s.Name] = s.Enabled
	}
	next, err := a.Store.NextRuns(ctx, ws, 1000)
	if err != nil {
		return nil, err
	}
	nextBy := map[string]time.Time{}
	for _, n := range next {
		nextBy[n.Automation] = n.At
	}
	isEnabled := func(name string) bool {
		if on, ok := enabled[name]; ok {
			return on
		}
		return true
	}

	snap := a.Snapshot()
	out := []automationSummary{}
	for name, au := range snap.Valid {
		if au.Workspace() != ws {
			continue
		}
		s := automationSummary{
			Name: name, Workspace: ws, Description: au.Manifest.Metadata.Description, Path: au.Dir,
			Valid: true, Enabled: isEnabled(name), Problems: []manifest.Problem{}, Triggers: triggersOf(au),
		}
		if at, ok := nextBy[name]; ok && s.Enabled {
			s.NextRunAt = &at
		}
		out = append(out, s)
	}
	for _, inv := range snap.Invalid {
		if inv.Workspace != ws {
			continue
		}
		problems := inv.Problems
		if problems == nil {
			problems = []manifest.Problem{}
		}
		out = append(out, automationSummary{
			Name: inv.Name, Workspace: ws, Path: inv.Path, Enabled: isEnabled(inv.Name),
			Problems: problems, Triggers: []triggerSummary{},
		})
	}
	for i := range out {
		if runs, err := a.Store.ListRuns(ctx, store.RunFilter{Workspace: ws, Automation: out[i].Name, Limit: 1}); err == nil && len(runs) > 0 {
			out[i].LastRun = &runs[0]
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *API) listAutomations(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	list, err := a.summaries(r, ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": list})
}

type stepRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

func (a *API) getAutomation(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	name := r.PathValue("name")
	au, inv, ok := a.automationIn(ws, name)
	if !ok {
		notFound(w, "automation")
		return
	}
	list, err := a.summaries(r, ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	var summary automationSummary
	for _, s := range list {
		if s.Name == name {
			summary = s
		}
	}
	dir := au.Dir
	if inv != nil {
		dir = inv.Path
	}
	text, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	recent, err := a.Store.ListRuns(r.Context(), store.RunFilter{Workspace: ws, Automation: name, Limit: 20})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if recent == nil {
		recent = []store.Run{}
	}
	resp := map[string]any{}
	b, _ := json.Marshal(summary)
	_ = json.Unmarshal(b, &resp)
	resp["manifest"] = string(text)
	resp["recentRuns"] = recent
	resp["steps"] = []stepRef{}
	resp["autonomy"] = manifest.AutonomyAsk
	if inv == nil {
		m := au.Manifest
		resp["autonomy"] = m.EffectiveAutonomy()
		if m.Budget != nil {
			resp["budget"] = map[string]any{"maxCostEur": m.Budget.MaxCostEur, "timeout": m.Budget.Timeout}
		}
		var steps []stepRef
		var walk func([]manifest.Step, string)
		walk = func(list []manifest.Step, prefix string) {
			for _, s := range list {
				steps = append(steps, stepRef{ID: prefix + s.ID, Type: s.Type, Name: s.Name})
				if s.Type == manifest.StepForeach {
					walk(s.Steps, prefix+s.ID+".")
				}
			}
		}
		walk(m.Steps, "")
		resp["steps"] = steps
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) triggerAutomation(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	name := r.PathValue("name")
	au, inv, ok := a.automationIn(ws, name)
	if !ok {
		notFound(w, "automation")
		return
	}
	if inv != nil {
		writeError(w, http.StatusConflict, "invalid_automation", "the automation is invalid; fix its problems first")
		return
	}
	id, _, err := a.Store.Enqueue(r.Context(), store.NewRun{
		Automation: name, Workspace: ws, ManifestHash: au.Hash, TriggerType: "manual",
		TriggerData: map[string]any{"by": p.label()},
	}, a.Now())
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.Wake()
	a.audit(r, p, "automation.trigger", ws, name, map[string]any{"run": id})
	writeJSON(w, http.StatusAccepted, map[string]string{"runId": id})
}

func (a *API) pauseAutomation(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	a.setEnabled(w, r, p, ws, false)
}

func (a *API) resumeAutomation(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	a.setEnabled(w, r, p, ws, true)
}

func (a *API) setEnabled(w http.ResponseWriter, r *http.Request, p *principal, ws string, on bool) {
	name := r.PathValue("name")
	if _, _, ok := a.automationIn(ws, name); !ok {
		notFound(w, "automation")
		return
	}
	if err := a.Store.SetEnabled(r.Context(), name, on); err != nil {
		a.internalError(w, err)
		return
	}
	action := "automation.pause"
	if on {
		action = "automation.resume"
	}
	a.audit(r, p, action, ws, name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listRuns(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs, err := a.Store.ListRuns(r.Context(), store.RunFilter{
		Workspace: ws, Automation: q.Get("automation"), Status: q.Get("status"), Before: q.Get("before"), Limit: limit + 1,
	})
	if err != nil {
		a.internalError(w, err)
		return
	}
	resp := map[string]any{"runs": []store.Run{}}
	if len(runs) > limit {
		runs = runs[:limit]
		resp["next"] = runs[limit-1].ID
	}
	if len(runs) > 0 {
		resp["runs"] = runs
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	run, err := a.Store.GetRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && run.Workspace != ws) {
		notFound(w, "run")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	steps, err := a.Store.ListSteps(r.Context(), run.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	calls, err := a.Store.ListLLMCalls(r.Context(), run.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if steps == nil {
		steps = []store.Step{}
	}
	if calls == nil {
		calls = []store.LLMCall{}
	}
	approvals, err := a.Store.ListApprovals(r.Context(), store.ApprovalFilter{RunID: run.ID})
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "steps": steps, "llmCalls": calls, "approvals": approvals})
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (a *API) overview(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	ctx := r.Context()
	now := a.Now()
	list, err := a.summaries(r, ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	counts := map[string]int{"total": len(list)}
	for _, s := range list {
		switch {
		case !s.Valid:
			counts["invalid"]++
		case !s.Enabled:
			counts["paused"]++
		default:
			counts["active"]++
		}
	}
	runs, err := a.Store.CountRuns(ctx, ws, now.Add(-24*time.Hour))
	if err != nil {
		a.internalError(w, err)
		return
	}
	cost, err := a.Store.RunCostSince(ctx, ws, monthStart(now))
	if err != nil {
		a.internalError(w, err)
		return
	}
	daily, err := a.Store.DailyRuns(ctx, ws, 14, now)
	if err != nil {
		a.internalError(w, err)
		return
	}
	failures, err := a.Store.ListRuns(ctx, store.RunFilter{Workspace: ws, Status: store.RunFailed, Limit: 5})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if failures == nil {
		failures = []store.Run{}
	}
	upcoming, err := a.Store.NextRuns(ctx, ws, 10)
	if err != nil {
		a.internalError(w, err)
		return
	}
	waiting, err := a.Store.CountPendingApprovals(ctx, ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"waitingApprovals": waiting,
		"health":           a.Health(ctx),
		"automations":      map[string]int{"total": counts["total"], "active": counts["active"], "paused": counts["paused"], "invalid": counts["invalid"]},
		"runs24h":          runs,
		"running":          runs.Running,
		"queued":           runs.Queued,
		"costMonthEur":     cost,
		"daily":            daily,
		"recentFailures":   failures,
		"upcoming":         upcoming,
	})
}

func (a *API) costs(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = 30
	}
	ctx := r.Context()
	now := a.Now()
	daily, err := a.Store.DailyRuns(ctx, ws, days, now)
	if err != nil {
		a.internalError(w, err)
		return
	}
	since := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	total := 0.0
	costDaily := make([]map[string]any, 0, len(daily))
	for _, d := range daily {
		total += d.CostEur
		costDaily = append(costDaily, map[string]any{"date": d.Date, "costEur": d.CostEur})
	}
	byAutomation, err := a.Store.CostByAutomation(ctx, ws, since)
	if err != nil {
		a.internalError(w, err)
		return
	}
	byModel, err := a.Store.CostByModel(ctx, ws, since)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days": days, "totalEur": total, "daily": costDaily, "byAutomation": byAutomation, "byModel": byModel,
	})
}

func (a *API) listSecrets(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	list, err := a.Store.ListSecrets(r.Context(), ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if list == nil {
		list = []store.SecretInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": list})
}

func (a *API) putSecret(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		badRequest(w, "secret names use letters, digits, '.', '_' and '-', up to 128 characters")
		return
	}
	var req struct{ Value string }
	if !decode(w, r, &req) {
		return
	}
	if req.Value == "" || len(req.Value) > 64<<10 {
		badRequest(w, "the value must be between 1 byte and 64 KiB")
		return
	}
	sealed, err := a.Vault.Seal([]byte(req.Value), secrets.SealContext(ws, name))
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.Store.PutSecret(r.Context(), ws, name, sealed, p.User.Email, a.Now()); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "secret.update", ws, name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteSecret(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	name := r.PathValue("name")
	err := a.Store.DeleteSecret(r.Context(), ws, name)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "secret")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "secret.delete", ws, name, nil)
	w.WriteHeader(http.StatusNoContent)
}

type member struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

func (a *API) listMembers(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	ms, err := a.Store.WorkspaceMembers(r.Context(), ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	out := []member{}
	for _, m := range ms {
		out = append(out, member{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: m.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (a *API) putMember(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var req struct{ Email, Role string }
	if !decode(w, r, &req) {
		return
	}
	if !store.ValidRole(req.Role) {
		badRequest(w, "role must be viewer, approver, builder or admin")
		return
	}
	u, err := a.Store.GetUserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no user with that email; an owner creates users first")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.Store.SetMembership(r.Context(), u.ID, ws, req.Role); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "member.set", ws, u.Email, map[string]any{"role": req.Role})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteMember(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	userID := r.PathValue("userId")
	err := a.Store.RemoveMembership(r.Context(), userID, ws)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "member")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "member.remove", ws, userID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) workspaceAudit(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	a.writeAudit(w, r, ws)
}

func (a *API) writeAudit(w http.ResponseWriter, r *http.Request, ws string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := a.Store.ListAudit(r.Context(), ws, limit)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// invalidAll lists invalid automations of every workspace, for owners.
func invalidAll(snap registry.Snapshot) []map[string]any {
	out := []map[string]any{}
	for _, inv := range snap.Invalid {
		problems := inv.Problems
		if problems == nil {
			problems = []manifest.Problem{}
		}
		out = append(out, map[string]any{"name": inv.Name, "path": inv.Path, "problems": problems})
	}
	return out
}
