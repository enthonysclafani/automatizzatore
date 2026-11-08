package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T, sched manifest.ScheduleTrigger) (*Scheduler, *store.Store, *clock) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := &clock{t: time.Date(2026, 11, 9, 6, 59, 0, 0, time.UTC)} // 07:59 in Rome
	a := engine.Automation{Name: "checks", Hash: "h", Manifest: &manifest.Manifest{
		Triggers: []manifest.Trigger{{Schedule: &sched}},
	}}
	s := &Scheduler{
		Store: st, Owner: "test", Now: c.now,
		Automations: func() map[string]engine.Automation { return map[string]engine.Automation{"checks": a} },
	}
	return s, st, c
}

func queued(t *testing.T, st *store.Store) []store.Run {
	t.Helper()
	runs, err := st.ListRuns(context.Background(), store.RunFilter{Status: store.RunQueued})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func tick(t *testing.T, s *Scheduler) {
	t.Helper()
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFiresOnTimeInTimezone(t *testing.T) {
	s, st, c := setup(t, manifest.ScheduleTrigger{Cron: "0 8,16 * * *", Timezone: "Europe/Rome"})
	tick(t, s) // arms the schedule, nothing due
	if n := len(queued(t, st)); n != 0 {
		t.Fatalf("enqueued %d runs when arming", n)
	}
	c.t = c.t.Add(30 * time.Second) // 07:59:30 Rome
	tick(t, s)
	if n := len(queued(t, st)); n != 0 {
		t.Fatalf("enqueued %d runs before 08:00", n)
	}
	c.t = time.Date(2026, 11, 9, 7, 0, 1, 0, time.UTC) // 08:00:01 Rome
	tick(t, s)
	tick(t, s) // a second tick must not enqueue the same occurrence again
	runs := queued(t, st)
	if len(runs) != 1 {
		t.Fatalf("enqueued %d runs at 08:00, want 1", len(runs))
	}
	want := time.Date(2026, 11, 9, 7, 0, 0, 0, time.UTC)
	if !runs[0].ScheduledFor.Equal(want) || runs[0].TriggerType != "schedule" {
		t.Fatalf("run = %+v", runs[0])
	}
}

func TestMisfirePolicies(t *testing.T) {
	cases := []struct {
		policy string
		want   int
	}{
		{"", 1},        // run_once: one run for the whole outage
		{"run_all", 3}, // every missed occurrence
		{"skip", 0},    // the latest is too late
	}
	for _, tc := range cases {
		t.Run(tc.policy, func(t *testing.T) {
			s, st, c := setup(t, manifest.ScheduleTrigger{Cron: "0 * * * *", Misfire: tc.policy})
			tick(t, s) // armed at 06:59, next 07:00
			c.t = time.Date(2026, 11, 9, 9, 30, 0, 0, time.UTC)
			tick(t, s) // 07:00, 08:00 and 09:00 were missed
			if n := len(queued(t, st)); n != tc.want {
				t.Fatalf("enqueued %d runs, want %d", n, tc.want)
			}
			st2, _ := st.GetTriggerState(context.Background(), "checks", TriggerKey(0, &manifest.ScheduleTrigger{Cron: "0 * * * *"}))
			if st2 == nil || !st2.NextRunAt.Equal(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)) {
				t.Fatalf("next run not advanced: %+v", st2)
			}
		})
	}
}

func TestSkipPolicyKeepsOnTimeRun(t *testing.T) {
	s, st, c := setup(t, manifest.ScheduleTrigger{Cron: "0 * * * *", Misfire: "skip"})
	tick(t, s)
	c.t = time.Date(2026, 11, 9, 7, 0, 20, 0, time.UTC)
	tick(t, s)
	if n := len(queued(t, st)); n != 1 {
		t.Fatalf("enqueued %d runs within the grace period, want 1", n)
	}
}

func TestPausedAutomationDoesNotRun(t *testing.T) {
	s, st, c := setup(t, manifest.ScheduleTrigger{Cron: "* * * * *"})
	ctx := context.Background()
	_ = st.UpsertAutomation(ctx, store.Automation{Name: "checks", Path: "x", Hash: "h", Valid: true}, c.t)
	_ = st.SetEnabled(ctx, "checks", false)
	tick(t, s)
	c.t = c.t.Add(5 * time.Minute)
	tick(t, s)
	if n := len(queued(t, st)); n != 0 {
		t.Fatalf("paused automation enqueued %d runs", n)
	}
	// Resuming does not replay the paused period.
	_ = st.SetEnabled(ctx, "checks", true)
	c.t = c.t.Add(10 * time.Second)
	tick(t, s)
	if n := len(queued(t, st)); n != 0 {
		t.Fatalf("resume replayed %d runs", n)
	}
}

func TestOnlyOneLeader(t *testing.T) {
	s1, st, c := setup(t, manifest.ScheduleTrigger{Cron: "* * * * *"})
	s2 := &Scheduler{Store: st, Owner: "other", Now: c.now, Automations: s1.Automations}
	tick(t, s1)
	tick(t, s2)
	if _, leader := s2.Status(); leader {
		t.Fatal("two schedulers hold the lease")
	}
	c.t = c.t.Add(2 * time.Minute)
	tick(t, s1)
	tick(t, s2)
	if n := len(queued(t, st)); n != 1 {
		t.Fatalf("enqueued %d runs, want 1", n)
	}
}
