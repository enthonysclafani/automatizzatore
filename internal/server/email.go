package server

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Email trigger limits.
const (
	defaultEmailInterval = 5 * time.Minute
	minEmailInterval     = time.Minute
	maxEmailsPerPoll     = 20
)

// pollEmail checks the email triggers that are due. The first poll of a
// trigger only remembers what is already in the mailbox, so turning an
// automation on does not process the whole inbox.
func (s *Server) pollEmail(ctx context.Context) {
	if s.hub == nil {
		return
	}
	if _, leader := s.sched.Status(); !leader {
		return
	}
	now := time.Now()
	for name, a := range s.automations() {
		for i, t := range a.Manifest.Triggers {
			if t.Email == nil {
				continue
			}
			if err := s.pollTrigger(ctx, name, a, i, t.Email.Connection, t.Email.Query, t.Email.Interval, now); err != nil && ctx.Err() == nil {
				s.log.Warn("email trigger", "automation", name, "err", err)
			}
		}
	}
}

func (s *Server) pollTrigger(ctx context.Context, name string, a engine.Automation, index int, connName, query, intervalText string, now time.Time) error {
	key := "email-" + strconv.Itoa(index)
	interval := defaultEmailInterval
	if d, err := time.ParseDuration(intervalText); err == nil && intervalText != "" {
		interval = max(d, minEmailInterval)
	}
	if query == "" {
		query = "in:inbox"
	}
	state, err := s.store.GetTriggerState(ctx, name, key)
	armed := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if armed && state.NextRunAt.After(now) {
		return nil
	}
	enabled, err := s.store.IsEnabled(ctx, name)
	if err != nil {
		return err
	}
	// Whatever happens, try again after the interval.
	defer func() { _ = s.store.SetTriggerState(ctx, name, key, store.TriggerState{NextRunAt: now.Add(interval)}) }()

	conn, err := s.hub.GoogleConnection(ctx, a.Workspace(), connName)
	if err != nil {
		return err
	}
	ids, err := s.hub.GmailList(ctx, conn, query, 100)
	if err != nil {
		return err
	}
	fresh, err := s.store.MarkEmailSeen(ctx, name, key, ids, now)
	if err != nil || !armed {
		if err == nil {
			s.log.Info("email trigger armed", "automation", name, "alreadyInMailbox", len(ids))
		}
		return err
	}
	if !enabled {
		return nil
	}
	slices.Reverse(fresh) // oldest first
	if len(fresh) > maxEmailsPerPoll {
		s.log.Warn("many new emails; the rest wait for the next poll", "automation", name, "new", len(fresh))
		fresh = fresh[:maxEmailsPerPoll]
	}
	for _, id := range fresh {
		email, err := s.hub.GmailMessage(ctx, conn, id)
		if err != nil {
			return err
		}
		runID, _, err := s.store.Enqueue(ctx, store.NewRun{
			Automation: name, Workspace: a.Workspace(), ManifestHash: a.Hash, TriggerType: "email",
			TriggerKey: key, TriggerData: connectors.EmailTriggerData(email),
		}, time.Now())
		if err != nil {
			return err
		}
		s.log.Info("run enqueued", "automation", name, "trigger", "email", "run", runID, "subject", email.Subject)
	}
	if len(fresh) > 0 {
		s.wakeWorkers()
	}
	return nil
}

func (s *Server) wakeWorkers() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// expireApprovals applies each pending approval's timeout rule.
func (s *Server) expireApprovals(ctx context.Context) {
	list, err := s.store.ExpiredApprovals(ctx, time.Now())
	if err != nil {
		s.log.Error("expired approvals", "err", err)
		return
	}
	for _, a := range list {
		status := store.ApprovalExpired
		if a.OnTimeout == "approve" {
			status = store.ApprovalApproved
		}
		if _, err := s.store.Decide(ctx, a.ID, status, "timeout", "", time.Now()); err != nil && !errors.Is(err, store.ErrConflict) {
			s.log.Error("expire approval", "approval", a.ID, "err", err)
			continue
		}
		s.log.Info("approval timed out", "automation", a.Automation, "run", a.RunID, "result", status)
	}
	if len(list) > 0 {
		s.wakeWorkers()
	}
}
