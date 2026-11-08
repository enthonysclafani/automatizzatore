package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "pendolo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var t0 = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)

func TestMigrateTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		_ = s.Close()
	}
}

func TestEnqueueScheduledOnce(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := t0
	n := NewRun{Automation: "a", TriggerType: "schedule", TriggerKey: "k", ScheduledFor: &at}
	_, ins1, err := s.Enqueue(ctx, n, t0)
	if err != nil || !ins1 {
		t.Fatalf("first enqueue: inserted=%v err=%v", ins1, err)
	}
	_, ins2, err := s.Enqueue(ctx, n, t0.Add(time.Second))
	if err != nil || ins2 {
		t.Fatalf("second enqueue of the same occurrence: inserted=%v err=%v", ins2, err)
	}
	// Manual runs have no occurrence and are never deduplicated.
	for i := 0; i < 2; i++ {
		if _, ins, err := s.Enqueue(ctx, NewRun{Automation: "a", TriggerType: "manual"}, t0); err != nil || !ins {
			t.Fatalf("manual enqueue: inserted=%v err=%v", ins, err)
		}
	}
	if n, _ := s.CountQueued(ctx); n != 3 {
		t.Fatalf("queued = %d, want 3", n)
	}
}

func TestClaimOneRunPerAutomation(t *testing.T) {
	s, ctx := open(t), context.Background()
	idA1, _, _ := s.Enqueue(ctx, NewRun{Automation: "a", TriggerType: "manual"}, t0)
	_, _, _ = s.Enqueue(ctx, NewRun{Automation: "a", TriggerType: "manual"}, t0.Add(time.Second))
	idB, _, _ := s.Enqueue(ctx, NewRun{Automation: "b", TriggerType: "manual"}, t0.Add(2*time.Second))

	r1, err := s.Claim(ctx, "w1", time.Minute, t0)
	if err != nil || r1 == nil || r1.ID != idA1 || r1.Status != RunRunning {
		t.Fatalf("first claim = %+v, %v", r1, err)
	}
	// The second run of "a" waits while the first runs; "b" goes next.
	r2, err := s.Claim(ctx, "w2", time.Minute, t0)
	if err != nil || r2 == nil || r2.ID != idB {
		t.Fatalf("second claim = %+v, %v; want run of b", r2, err)
	}
	if r3, _ := s.Claim(ctx, "w3", time.Minute, t0); r3 != nil {
		t.Fatalf("third claim = %+v, want nothing", r3)
	}
	if err := s.Finish(ctx, r1.ID, RunSucceeded, "", 0, 0.01, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if r4, _ := s.Claim(ctx, "w1", time.Minute, t0); r4 == nil || r4.Automation != "a" {
		t.Fatalf("after finishing, the next run of a should be claimable, got %+v", r4)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	s, ctx := open(t), context.Background()
	_, _, _ = s.Enqueue(ctx, NewRun{Automation: "a", TriggerType: "manual"}, t0)
	r, _ := s.Claim(ctx, "w", time.Minute, t0)

	if n, _ := s.RecoverInterrupted(ctx, t0.Add(30*time.Second)); n != 0 {
		t.Fatalf("recovered %d runs with a live lease", n)
	}
	if err := s.RenewLease(ctx, r.ID, "w", t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.RecoverInterrupted(ctx, t0.Add(90*time.Second)); n != 0 {
		t.Fatalf("recovered %d runs after renewal", n)
	}
	if n, _ := s.RecoverInterrupted(ctx, t0.Add(3*time.Minute)); n != 1 {
		t.Fatalf("recovered %d runs, want 1", n)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != RunFailed || got.Error == "" || got.FinishedAt == nil {
		t.Fatalf("recovered run = %+v", got)
	}
}

func TestStepsAndCallsCascade(t *testing.T) {
	s, ctx := open(t), context.Background()
	r, err := s.StartNow(ctx, NewRun{Automation: "a", TriggerType: "manual"}, "cli", t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartStep(ctx, r.ID, 1, "check", "check", "http", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishStep(ctx, r.ID, 1, StepResult{Status: StepSucceeded, Attempts: 1, Output: map[string]any{"status": 200}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLLMCall(ctx, LLMCall{RunID: r.ID, StepPath: "check", Provider: "p", Model: "m", Request: []byte(`{"a":1}`), PriceKnown: true, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	steps, _ := s.ListSteps(ctx, r.ID)
	if len(steps) != 1 || string(steps[0].Output) != `{"status":200}` {
		t.Fatalf("steps = %+v", steps)
	}
	if err := s.Finish(ctx, r.ID, RunSucceeded, "", 0, 0, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.DeleteRunsBefore(ctx, t0.Add(time.Hour)); n != 1 {
		t.Fatalf("deleted %d runs", n)
	}
	steps, _ = s.ListSteps(ctx, r.ID)
	calls, _ := s.ListLLMCalls(ctx, r.ID)
	if len(steps) != 0 || len(calls) != 0 {
		t.Fatalf("steps and calls not deleted with their run: %d, %d", len(steps), len(calls))
	}
}

func TestLocks(t *testing.T) {
	s, ctx := open(t), context.Background()
	ok, _ := s.AcquireLock(ctx, "scheduler", "a", time.Minute, t0)
	if !ok {
		t.Fatal("a could not take a free lock")
	}
	if ok, _ := s.AcquireLock(ctx, "scheduler", "b", time.Minute, t0.Add(10*time.Second)); ok {
		t.Fatal("b took a lock held by a")
	}
	if ok, _ := s.AcquireLock(ctx, "scheduler", "a", time.Minute, t0.Add(10*time.Second)); !ok {
		t.Fatal("a could not renew its own lock")
	}
	if ok, _ := s.AcquireLock(ctx, "scheduler", "b", time.Minute, t0.Add(2*time.Minute)); !ok {
		t.Fatal("b could not take an expired lock")
	}
}

func TestTriggerStateAndAutomations(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := s.GetTriggerState(ctx, "a", "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	_ = s.SetTriggerState(ctx, "a", "k1", TriggerState{NextRunAt: t0})
	_ = s.SetTriggerState(ctx, "a", "k2", TriggerState{NextRunAt: t0})
	if err := s.PruneTriggerStates(ctx, "a", []string{"k2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTriggerState(ctx, "a", "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale trigger state not pruned")
	}
	st, err := s.GetTriggerState(ctx, "a", "k2")
	if err != nil || !st.NextRunAt.Equal(t0) {
		t.Fatalf("state = %+v, %v", st, err)
	}

	_ = s.UpsertAutomation(ctx, Automation{Name: "a", Path: "/x/a", Hash: "h1", Valid: true}, t0)
	_ = s.SetEnabled(ctx, "a", false)
	_ = s.UpsertAutomation(ctx, Automation{Name: "a", Path: "/x/a", Hash: "h2", Valid: true}, t0)
	if on, _ := s.IsEnabled(ctx, "a"); on {
		t.Fatal("upsert re-enabled a paused automation")
	}
	if err := s.DeleteAutomationsExcept(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListAutomations(ctx); len(list) != 0 {
		t.Fatalf("automations left: %+v", list)
	}
	if _, err := s.GetTriggerState(ctx, "a", "k2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("trigger state of a deleted automation kept")
	}
}

func TestNewIDSortable(t *testing.T) {
	a := NewID(t0)
	b := NewID(t0.Add(time.Millisecond))
	if len(a) != 26 || a >= b {
		t.Fatalf("ids not sortable: %s %s", a, b)
	}
}

func TestListRunsPagination(t *testing.T) {
	s, ctx := open(t), context.Background()
	for i := 0; i < 5; i++ {
		_, _, _ = s.Enqueue(ctx, NewRun{Automation: "a", Workspace: "w", TriggerType: "manual"}, t0) // same timestamp
	}
	_, _, _ = s.Enqueue(ctx, NewRun{Automation: "b", Workspace: "other", TriggerType: "manual"}, t0)
	page1, _ := s.ListRuns(ctx, RunFilter{Workspace: "w", Limit: 3})
	page2, _ := s.ListRuns(ctx, RunFilter{Workspace: "w", Limit: 3, Before: page1[2].ID})
	if len(page1) != 3 || len(page2) != 2 {
		t.Fatalf("pages of %d and %d", len(page1), len(page2))
	}
	seen := map[string]bool{}
	for _, r := range append(page1, page2...) {
		if r.Workspace != "w" || seen[r.ID] {
			t.Fatalf("run %+v repeated or from another workspace", r)
		}
		seen[r.ID] = true
	}
}
