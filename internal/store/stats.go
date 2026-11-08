package store

import (
	"context"
	"encoding/json"
	"time"
)

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// RunCounts counts runs by outcome.
type RunCounts struct {
	Succeeded     int `json:"succeeded"`
	Failed        int `json:"failed"`
	Cancelled     int `json:"cancelled"`
	WithAnomalies int `json:"withAnomalies"`
	Running       int `json:"-"`
	Queued        int `json:"-"`
}

// CountRuns counts the runs of a workspace queued since a time.
func (s *Store) CountRuns(ctx context.Context, workspace string, since time.Time) (RunCounts, error) {
	var c RunCounts
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*), SUM(anomalies > 0) FROM runs
		WHERE workspace = ? AND queued_at >= ? GROUP BY status`, workspace, ts(since))
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n, anomalies int
		if err := rows.Scan(&status, &n, &anomalies); err != nil {
			return c, err
		}
		switch status {
		case RunSucceeded:
			c.Succeeded, c.WithAnomalies = n, c.WithAnomalies+anomalies
		case RunFailed:
			c.Failed = n
		case RunCancelled:
			c.Cancelled = n
		}
	}
	if err := rows.Err(); err != nil {
		return c, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(status = ?), 0), COALESCE(SUM(status = ?), 0) FROM runs WHERE workspace = ?`,
		RunRunning, RunQueued, workspace).Scan(&c.Running, &c.Queued)
	return c, err
}

// DayStat is one day of activity.
type DayStat struct {
	Date      string  `json:"date"`
	Succeeded int     `json:"succeeded"`
	Failed    int     `json:"failed"`
	CostEur   float64 `json:"costEur"`
}

// DailyRuns returns the last n days (UTC), oldest first, with zero days.
func (s *Store) DailyRuns(ctx context.Context, workspace string, days int, now time.Time) ([]DayStat, error) {
	start := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	rows, err := s.db.QueryContext(ctx, `SELECT substr(queued_at, 1, 10) AS day, SUM(status = ?), SUM(status = ?), SUM(cost_eur)
		FROM runs WHERE workspace = ? AND queued_at >= ? GROUP BY day`, RunSucceeded, RunFailed, workspace, ts(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DayStat{}
	for rows.Next() {
		var d DayStat
		if err := rows.Scan(&d.Date, &d.Succeeded, &d.Failed, &d.CostEur); err != nil {
			return nil, err
		}
		byDay[d.Date] = d
	}
	out := make([]DayStat, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		d := byDay[date]
		d.Date = date
		out = append(out, d)
	}
	return out, rows.Err()
}

// RunCostSince sums run costs of a workspace since a time.
func (s *Store) RunCostSince(ctx context.Context, workspace string, since time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_eur), 0) FROM runs WHERE workspace = ? AND queued_at >= ?`,
		workspace, ts(since)).Scan(&total)
	return total, err
}

// AutomationCost is the spending of one automation.
type AutomationCost struct {
	Automation string  `json:"automation"`
	CostEur    float64 `json:"costEur"`
	Runs       int     `json:"runs"`
}

// CostByAutomation sums run costs per automation since a time.
func (s *Store) CostByAutomation(ctx context.Context, workspace string, since time.Time) ([]AutomationCost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT automation, SUM(cost_eur), COUNT(*) FROM runs
		WHERE workspace = ? AND queued_at >= ? GROUP BY automation ORDER BY SUM(cost_eur) DESC, automation`, workspace, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationCost{}
	for rows.Next() {
		var c AutomationCost
		if err := rows.Scan(&c.Automation, &c.CostEur, &c.Runs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ModelCost is the spending on one model.
type ModelCost struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	CostEur      float64 `json:"costEur"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Calls        int     `json:"calls"`
	PriceKnown   bool    `json:"priceKnown"`
}

// CostByModel sums model calls of a workspace since a time.
func (s *Store) CostByModel(ctx context.Context, workspace string, since time.Time) ([]ModelCost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.provider, c.model, SUM(c.cost_eur), SUM(c.input_tokens), SUM(c.output_tokens),
		COUNT(*), MIN(c.price_known) FROM llm_calls c JOIN runs r ON r.id = c.run_id
		WHERE r.workspace = ? AND c.created_at >= ? GROUP BY c.provider, c.model ORDER BY SUM(c.cost_eur) DESC`, workspace, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelCost{}
	for rows.Next() {
		var m ModelCost
		var known int
		if err := rows.Scan(&m.Provider, &m.Model, &m.CostEur, &m.InputTokens, &m.OutputTokens, &m.Calls, &known); err != nil {
			return nil, err
		}
		m.PriceKnown = known == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// Upcoming is a scheduled run that has not happened yet.
type Upcoming struct {
	Automation string    `json:"automation"`
	At         time.Time `json:"at"`
}

// NextRuns returns the next fire time of every enabled automation of a
// workspace, soonest first.
func (s *Store) NextRuns(ctx context.Context, workspace string, limit int) ([]Upcoming, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.automation, MIN(t.next_run_at) FROM trigger_state t
		JOIN automations a ON a.name = t.automation
		WHERE a.workspace = ? AND a.enabled = 1 AND a.valid = 1 GROUP BY t.automation ORDER BY MIN(t.next_run_at) LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upcoming{}
	for rows.Next() {
		var u Upcoming
		var at string
		if err := rows.Scan(&u.Automation, &at); err != nil {
			return nil, err
		}
		u.At = mustTS(at)
		out = append(out, u)
	}
	return out, rows.Err()
}
