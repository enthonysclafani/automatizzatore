package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Decisions of the autonomy policy.
const (
	decideAllow   = "allow"
	decideDraft   = "draft"
	decideApprove = "approve"
	decideBlock   = "block"
)

var autonomyRank = map[string]int{
	manifest.AutonomyObserve: 0, manifest.AutonomyDraft: 1, manifest.AutonomyAsk: 2, manifest.AutonomyAuto: 3,
}

// lowerAutonomy returns the more restrictive of two levels.
func lowerAutonomy(a, b string) string {
	ra, okA := autonomyRank[a]
	rb, okB := autonomyRank[b]
	switch {
	case !okA:
		return b
	case !okB:
		return a
	case ra <= rb:
		return a
	default:
		return b
	}
}

// policy decides what an action of a given risk may do at an autonomy level.
//
//	observe: reads only
//	draft:   reads and reversible writes; messages become drafts, or are blocked
//	ask:     sending and destructive actions wait for a person
//	auto:    only destructive actions wait for a person
func policy(autonomy string, risk connectors.Risk) string {
	switch autonomy {
	case manifest.AutonomyObserve:
		if risk == connectors.RiskRead {
			return decideAllow
		}
		return decideBlock
	case manifest.AutonomyDraft:
		switch risk {
		case connectors.RiskRead, connectors.RiskWrite:
			return decideAllow
		case connectors.RiskSend:
			return decideDraft
		}
		return decideBlock
	case manifest.AutonomyAuto:
		if risk == connectors.RiskDestructive {
			return decideApprove
		}
		return decideAllow
	default: // ask
		if risk == connectors.RiskRead || risk == connectors.RiskWrite {
			return decideAllow
		}
		return decideApprove
	}
}

// needsApproval pauses a run until a person decides.
type needsApproval struct {
	message   string
	detail    map[string]any
	approvers []string
	timeout   time.Duration
	onTimeout string
	// explicit is true for approval steps, false for actions gated by autonomy.
	explicit bool
}

func (n *needsApproval) Error() string { return "waiting for approval: " + n.message }

var defaultApprovers = []string{store.RoleApprover, store.RoleAdmin}

const defaultApprovalTimeout = 24 * time.Hour

func (ex *execution) autonomy(ctx context.Context) string {
	level := ex.a.Manifest.EffectiveAutonomy()
	if ex.e.Store != nil {
		if max, err := ex.e.Store.WorkspaceAutonomy(ctx, ex.a.Workspace()); err == nil {
			level = lowerAutonomy(level, max)
		}
	}
	return level
}

func (ex *execution) execConnector(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	if ex.e.Connectors == nil {
		return stepOutcome{err: fmt.Errorf("connectors are %w in this process", errUnsupported)}
	}
	withV, err := ex.render(any(st.With), env)
	if err != nil {
		return stepOutcome{err: err}
	}
	input, _ := withV.(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	bound, err := ex.e.Connectors.Resolve(ctx, ex.a.Workspace(), st.Action, st.Connection)
	if err != nil {
		return stepOutcome{err: err}
	}
	rec := map[string]any{"action": st.Action, "connection": bound.Connection, "risk": bound.Risk, "input": input}

	level := ex.autonomy(ctx)
	switch policy(level, bound.Risk) {
	case decideBlock:
		return stepOutcome{input: rec, err: fmt.Errorf("%s is a %s action, which autonomy %q does not allow", st.Action, bound.Risk, level)}
	case decideDraft:
		if bound.DraftAlternative == "" {
			return stepOutcome{input: rec, err: fmt.Errorf("%s sends to others and has no draft form; autonomy %q blocks it", st.Action, level)}
		}
		alt, err := ex.e.Connectors.Resolve(ctx, ex.a.Workspace(), bound.DraftAlternative, st.Connection)
		if err != nil {
			return stepOutcome{input: rec, err: err}
		}
		rec["draftInsteadOf"], rec["action"] = st.Action, alt.Name
		bound = alt
	case decideApprove:
		if _, ok := ex.approved[path]; !ok {
			return stepOutcome{input: rec, err: &needsApproval{
				message:   fmt.Sprintf("%s wants to run %s (%s)", ex.a.Name, st.Action, bound.Risk),
				detail:    rec,
				approvers: defaultApprovers, timeout: defaultApprovalTimeout, onTimeout: "reject",
			}}
		}
	}
	out, err := bound.Call(ctx, input)
	return stepOutcome{input: rec, output: out, err: err}
}

func (ex *execution) execApproval(st *manifest.Step, env expr.Env, path string) stepOutcome {
	if d, ok := ex.approved[path]; ok {
		return stepOutcome{output: map[string]any{"approved": true, "by": d.By, "comment": d.Comment, "at": d.At}}
	}
	msgV, err := expr.String(st.Message, env)
	if err != nil {
		return stepOutcome{err: err}
	}
	timeout := defaultApprovalTimeout
	if st.Timeout != "" {
		if d, err := time.ParseDuration(st.Timeout); err == nil {
			timeout = d
		}
	}
	approvers := st.Approvers
	if len(approvers) == 0 {
		approvers = defaultApprovers
	}
	onTimeout := st.OnTimeout
	if onTimeout == "" {
		onTimeout = "reject"
	}
	msg := expr.Text(msgV)
	return stepOutcome{input: map[string]any{"message": msg}, err: &needsApproval{
		message: msg, detail: map[string]any{"message": msg}, approvers: approvers,
		timeout: timeout, onTimeout: onTimeout, explicit: true,
	}}
}

// decision is a granted approval, kept in the checkpoint.
type decision struct {
	By      string    `json:"by"`
	Comment string    `json:"comment,omitempty"`
	At      time.Time `json:"at"`
}

// checkpoint is what a waiting run needs to resume.
type checkpoint struct {
	// Next is the index of the top-level step to run on resume.
	Next      int                 `json:"next"`
	StepID    string              `json:"stepId"`
	Explicit  bool                `json:"explicit"`
	Steps     map[string]any      `json:"steps"`
	Approved  map[string]decision `json:"approved"`
	CostEur   float64             `json:"costEur"`
	Anomalies int                 `json:"anomalies"`
}

var errPaused = errors.New("paused for approval")

// pause stores the run's progress and the approval request.
func (ex *execution) pause(index int, st *manifest.Step, seq int, path string, results map[string]any, na *needsApproval, input any) error {
	steps := map[string]any{}
	if b, err := json.Marshal(ex.redactValue(results)); err == nil {
		_ = json.Unmarshal(b, &steps)
	}
	cp := checkpoint{Next: index, StepID: st.ID, Explicit: na.explicit, Steps: steps, Approved: ex.approved, CostEur: ex.cost, Anomalies: ex.anomalies}
	if na.explicit {
		cp.Next = index + 1
	}
	cpJSON, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	now := ex.e.now()
	detail := map[string]any{}
	if b, err := json.Marshal(ex.redactValue(na.detail)); err == nil {
		_ = json.Unmarshal(b, &detail)
	}
	appr := store.Approval{
		ID: store.NewID(now), RunID: ex.run.ID, Workspace: ex.a.Workspace(), Automation: ex.a.Name, StepPath: path,
		Message: ex.resolver.Redact(na.message), Detail: detail, Approvers: na.approvers, OnTimeout: na.onTimeout,
		RequestedAt: now, ExpiresAt: now.Add(na.timeout),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ex.e.Store.MarkStepWaiting(ctx, ex.run.ID, seq, ex.redactValue(input), now); err != nil {
		return err
	}
	if err := ex.e.Store.Pause(ctx, appr, string(cpJSON), ex.cost, ex.anomalies); err != nil {
		return err
	}
	ex.paused = &appr
	return errPaused
}

// resume loads a waiting run's progress, with the decisions taken since.
func (ex *execution) resume(ctx context.Context, env expr.Env) (int, error) {
	raw, err := ex.e.Store.Checkpoint(ctx, ex.run.ID)
	if err != nil || raw == "" {
		return 0, err
	}
	var cp checkpoint
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return 0, fmt.Errorf("unreadable checkpoint: %w", err)
	}
	ex.approved = cp.Approved
	if ex.approved == nil {
		ex.approved = map[string]decision{}
	}
	approvals, err := ex.e.Store.ListApprovals(ctx, store.ApprovalFilter{RunID: ex.run.ID, Status: store.ApprovalApproved})
	if err != nil {
		return 0, err
	}
	for _, a := range approvals {
		at := a.RequestedAt
		if a.DecidedAt != nil {
			at = *a.DecidedAt
		}
		ex.approved[a.StepPath] = decision{By: a.DecidedBy, Comment: a.Comment, At: at}
	}
	results := env["steps"].(map[string]any)
	for k, v := range cp.Steps {
		results[k] = v
	}
	if cp.Explicit {
		d := ex.approved[cp.StepID]
		results[cp.StepID] = resultMap(map[string]any{"approved": true, "by": d.By, "comment": d.Comment, "at": d.At}, store.StepSucceeded, nil)
	}
	ex.cost, ex.anomalies = cp.CostEur, cp.Anomalies
	seq, err := ex.e.Store.MaxStepSeq(ctx, ex.run.ID)
	if err != nil {
		return 0, err
	}
	ex.seq = seq
	return cp.Next, nil
}

// approvalInsideLoop explains why approvals cannot pause inside foreach.
func approvalInsideLoop(na *needsApproval) error {
	return fmt.Errorf("%s; runs cannot pause inside foreach: move the action out of the loop, or raise autonomy",
		strings.TrimPrefix(na.Error(), "waiting for approval: "))
}
