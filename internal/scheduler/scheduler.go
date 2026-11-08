// Package scheduler turns schedule triggers into queued runs.
//
// The next fire time of every schedule lives in the database, so a restart
// loses nothing: occurrences missed while Pendolo was down are handled by
// the trigger's misfire policy. A lease in the database makes sure only one
// scheduler enqueues at a time, and a unique index makes each occurrence
// enqueue at most once.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

const (
	lockName = "scheduler"
	lockTTL  = 15 * time.Second
	// Grace is how late an occurrence can be and still count as on time
	// for the "skip" policy.
	Grace = time.Minute
	// maxCatchUp caps how many missed occurrences "run_all" enqueues.
	maxCatchUp = 100
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Scheduler enqueues runs for schedule triggers.
type Scheduler struct {
	Store *store.Store
	// Owner identifies this process in the scheduler lease.
	Owner string
	// Automations returns the current set of valid automations.
	Automations func() map[string]engine.Automation
	// Wake is signalled (without blocking) when runs were enqueued.
	Wake chan<- struct{}
	Log  *slog.Logger
	Now  func() time.Time

	mu       sync.Mutex
	lastTick time.Time
	leader   bool
}

// Status reports when the scheduler last completed a tick and whether it
// holds the lease.
func (s *Scheduler) Status() (lastTick time.Time, leader bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTick, s.leader
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run ticks every interval until ctx ends, then releases the lease.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("scheduler tick", "err", err)
		}
		select {
		case <-ctx.Done():
			rel, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Store.ReleaseLock(rel, lockName, s.Owner)
			cancel()
			return
		case <-t.C:
		}
	}
}

// Tick enqueues every due occurrence. It returns how many runs it added.
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.now()
	leader, err := s.Store.AcquireLock(ctx, lockName, s.Owner, lockTTL, now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if leader != s.leader {
		s.log().Info("scheduler lease", "leader", leader, "owner", s.Owner)
	}
	s.leader = leader
	s.mu.Unlock()

	added := 0
	var errs []error
	if leader {
		for name, a := range s.Automations() {
			n, err := s.tickAutomation(ctx, name, a, now)
			added += n
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
	}
	s.mu.Lock()
	s.lastTick = now
	s.mu.Unlock()
	if added > 0 && s.Wake != nil {
		select {
		case s.Wake <- struct{}{}:
		default:
		}
	}
	return errors.Join(errs...)
}

// TriggerKey identifies a schedule trigger; it changes when the schedule does.
func TriggerKey(index int, t *manifest.ScheduleTrigger) string {
	sum := sha256.Sum256([]byte(t.Cron + "\x00" + t.Timezone))
	return strconv.Itoa(index) + "-" + hex.EncodeToString(sum[:])[:12]
}

func (s *Scheduler) tickAutomation(ctx context.Context, name string, a engine.Automation, now time.Time) (int, error) {
	enabled, err := s.Store.IsEnabled(ctx, name)
	if err != nil {
		return 0, err
	}
	var keys []string
	added := 0
	for i, tr := range a.Manifest.Triggers {
		if tr.Schedule == nil {
			continue
		}
		key := TriggerKey(i, tr.Schedule)
		keys = append(keys, key)
		sched, err := Parse(tr.Schedule)
		if err != nil {
			return added, err
		}

		st, err := s.Store.GetTriggerState(ctx, name, key)
		if errors.Is(err, store.ErrNotFound) {
			// A new or edited schedule starts from now: no catch-up.
			next := sched.Next(now)
			if err := s.Store.SetTriggerState(ctx, name, key, store.TriggerState{NextRunAt: next}); err != nil {
				return added, err
			}
			s.log().Info("schedule armed", "automation", name, "cron", tr.Schedule.Cron, "next", next)
			continue
		}
		if err != nil {
			return added, err
		}
		if st.NextRunAt.After(now) {
			continue
		}

		due := occurrences(sched, st.NextRunAt, now)
		if enabled {
			for _, occ := range pick(due, tr.Schedule.Misfire, now) {
				at := occ
				_, inserted, err := s.Store.Enqueue(ctx, store.NewRun{
					Automation: name, Workspace: a.Workspace(), ManifestHash: a.Hash, TriggerType: "schedule", TriggerKey: key, ScheduledFor: &at,
					TriggerData: map[string]any{
						"cron": tr.Schedule.Cron, "timezone": tr.Schedule.Timezone,
						"lateSeconds": int(now.Sub(occ).Seconds()),
					},
				}, now)
				if err != nil {
					return added, err
				}
				if inserted {
					added++
					s.log().Info("run enqueued", "automation", name, "scheduledFor", occ, "late", now.Sub(occ).Round(time.Second))
				}
			}
			if skipped := len(due) - len(pick(due, tr.Schedule.Misfire, now)); skipped > 0 {
				s.log().Warn("missed runs skipped by misfire policy", "automation", name, "skipped", skipped,
					"policy", misfirePolicy(tr.Schedule.Misfire))
			}
		}
		last := due[len(due)-1]
		next := sched.Next(now)
		if err := s.Store.SetTriggerState(ctx, name, key, store.TriggerState{NextRunAt: next, LastScheduledAt: &last}); err != nil {
			return added, err
		}
	}
	return added, s.Store.PruneTriggerStates(ctx, name, keys)
}

// Parse compiles a schedule in its time zone.
func Parse(t *manifest.ScheduleTrigger) (cron.Schedule, error) {
	loc := time.UTC
	if t.Timezone != "" {
		l, err := time.LoadLocation(t.Timezone)
		if err != nil {
			return nil, err
		}
		loc = l
	}
	sched, err := parser.Parse(t.Cron)
	if err != nil {
		return nil, err
	}
	return inLocation{sched, loc}, nil
}

// inLocation evaluates a schedule in a time zone, whatever the caller's.
type inLocation struct {
	s   cron.Schedule
	loc *time.Location
}

func (l inLocation) Next(t time.Time) time.Time { return l.s.Next(t.In(l.loc)) }

// occurrences lists due times from first up to now, at most maxCatchUp+1.
func occurrences(s cron.Schedule, first, now time.Time) []time.Time {
	out := []time.Time{first}
	for t := s.Next(first); !t.After(now) && len(out) <= maxCatchUp; t = s.Next(t) {
		out = append(out, t)
	}
	return out
}

func misfirePolicy(p string) string {
	if p == "" {
		return "run_once"
	}
	return p
}

// pick applies the misfire policy to the due occurrences.
func pick(due []time.Time, policy string, now time.Time) []time.Time {
	latest := due[len(due)-1]
	switch misfirePolicy(policy) {
	case "run_all":
		if len(due) > maxCatchUp {
			return due[len(due)-maxCatchUp:]
		}
		return due
	case "skip":
		if now.Sub(latest) <= Grace {
			return []time.Time{latest}
		}
		return nil
	default: // run_once
		return []time.Time{latest}
	}
}
