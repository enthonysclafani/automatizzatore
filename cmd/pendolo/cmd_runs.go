package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/store"
)

func cmdList(args []string, stdout, stderr io.Writer) int {
	c := newCommand("list", stderr, true)
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	ctx := context.Background()
	autos, err := st.ListAutomations(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if c.json() {
		printJSON(stdout, autos)
		return exitOK
	}
	if len(autos) == 0 {
		fmt.Fprintln(stdout, "no automations recorded yet: start `pendolo server` to load the automations folder")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tLAST RUN\tLAST STATUS\tFOLDER")
	for _, a := range autos {
		state := "active"
		switch {
		case !a.Valid:
			state = "invalid"
		case !a.Enabled:
			state = "paused"
		}
		last, status := "-", "-"
		if runs, _ := st.ListRuns(ctx, store.RunFilter{Automation: a.Name, Limit: 1}); len(runs) > 0 {
			last, status = local(runs[0].StartedAt), runs[0].Status
			if runs[0].StartedAt == nil {
				last = local(&runs[0].QueuedAt)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, state, last, status, a.Path)
	}
	_ = tw.Flush()
	return exitOK
}

func cmdRuns(args []string, stdout, stderr io.Writer) int {
	c := newCommand("runs", stderr, true)
	automation := c.fs.String("automation", "", "only runs of this automation")
	status := c.fs.String("status", "", "only runs with this status (queued, running, succeeded, failed, cancelled)")
	limit := c.fs.Int("limit", 20, "how many runs")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	runs, err := st.ListRuns(context.Background(), store.RunFilter{Automation: *automation, Status: *status, Limit: *limit})
	if err != nil {
		return fail(stderr, err)
	}
	if c.json() {
		if runs == nil {
			runs = []store.Run{}
		}
		printJSON(stdout, runs)
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tAUTOMATION\tSTATUS\tTRIGGER\tSTARTED\tTOOK\tCOST\tNOTE")
	for _, r := range runs {
		note := r.Error
		if note == "" && r.Anomalies > 0 {
			note = fmt.Sprintf("%d step(s) failed", r.Anomalies)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t€%.4f\t%s\n", r.ID, r.Automation, r.Status, r.TriggerType,
			local(r.StartedAt), duration(r.StartedAt, r.FinishedAt), r.CostEur, short(note, 60))
	}
	_ = tw.Flush()
	return exitOK
}

func cmdShow(args []string, stdout, stderr io.Writer) int {
	c := newCommand("show", stderr, true)
	full := c.fs.Bool("full", false, "print complete logs and model answers")
	pos, err := c.parse(args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: pendolo show [--config f] [--json] [--full] <run-id>")
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	return printRun(context.Background(), st, pos[0], c.json(), *full, stdout, stderr)
}

// runDetail is the JSON shape of `show`.
type runDetail struct {
	Run   *store.Run      `json:"run"`
	Steps []store.Step    `json:"steps"`
	Calls []store.LLMCall `json:"llmCalls"`
}

func printRun(ctx context.Context, st *store.Store, id string, asJSON, full bool, stdout, stderr io.Writer) int {
	run, err := st.GetRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return fail(stderr, fmt.Errorf("no run %q", id))
	}
	if err != nil {
		return fail(stderr, err)
	}
	steps, err := st.ListSteps(ctx, id)
	if err != nil {
		return fail(stderr, err)
	}
	calls, err := st.ListLLMCalls(ctx, id)
	if err != nil {
		return fail(stderr, err)
	}
	if asJSON {
		if steps == nil {
			steps = []store.Step{}
		}
		if calls == nil {
			calls = []store.LLMCall{}
		}
		printJSON(stdout, runDetail{Run: run, Steps: steps, Calls: calls})
		return exitOK
	}

	fmt.Fprintf(stdout, "Run %s of %s: %s\n", run.ID, run.Automation, strings.ToUpper(run.Status))
	fmt.Fprintf(stdout, "  trigger %s, started %s, took %s, cost €%.4f\n", run.TriggerType, local(run.StartedAt),
		duration(run.StartedAt, run.FinishedAt), run.CostEur)
	if run.Error != "" {
		fmt.Fprintf(stdout, "  error: %s\n", run.Error)
	}
	fmt.Fprintln(stdout)

	maxLines := 15
	if full {
		maxLines = 0
	}
	for _, s := range steps {
		mark := map[string]string{store.StepSucceeded: "ok  ", store.StepFailed: "FAIL", store.StepSkipped: "skip", store.StepRunning: "... "}[s.Status]
		extra := ""
		if s.Attempts > 1 {
			extra = fmt.Sprintf(", %d attempts", s.Attempts)
		}
		if s.CostEur > 0 {
			extra += fmt.Sprintf(", €%.4f", s.CostEur)
		}
		started := s.StartedAt
		fmt.Fprintf(stdout, "  %s %s (%s, %s%s)\n", mark, s.Path, s.Type, duration(&started, s.FinishedAt), extra)
		if s.Error != "" {
			fmt.Fprintf(stdout, "       error: %s\n", s.Error)
		}
		if logs := strings.TrimRight(s.Logs, "\n"); logs != "" {
			printIndented(stdout, logs, maxLines)
		}
	}
	if len(calls) > 0 {
		fmt.Fprintln(stdout, "\n  Model calls:")
		for _, c := range calls {
			price := fmt.Sprintf("€%.4f", c.CostEur)
			if !c.PriceKnown {
				price = "price not configured"
			}
			fmt.Fprintf(stdout, "    %s: %s/%s, %d in + %d out tokens, %s, %s\n", c.StepPath, c.Provider, c.Model,
				c.InputTokens, c.OutputTokens, price, (time.Duration(c.LatencyMs) * time.Millisecond).String())
			if c.Error != "" {
				fmt.Fprintf(stdout, "      error: %s\n", c.Error)
			}
			if full {
				printIndented(stdout, c.Response, 0)
			}
		}
	}
	return exitOK
}

func printIndented(w io.Writer, text string, maxLines int) {
	lines := strings.Split(text, "\n")
	cut := 0
	if maxLines > 0 && len(lines) > maxLines {
		cut = len(lines) - maxLines
		lines = lines[cut:]
	}
	if cut > 0 {
		fmt.Fprintf(w, "       | ... %d earlier lines (use --full)\n", cut)
	}
	for _, l := range lines {
		fmt.Fprintf(w, "       | %s\n", l)
	}
}

func cmdPause(pause bool, args []string, stdout, stderr io.Writer) int {
	name := "resume"
	if pause {
		name = "pause"
	}
	c := newCommand(name, stderr, false)
	pos, err := c.parse(args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintf(stderr, "usage: pendolo %s [--config f] <name>\n", name)
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	if err := st.SetEnabled(context.Background(), pos[0], !pause); errors.Is(err, store.ErrNotFound) {
		return fail(stderr, fmt.Errorf("no automation %q recorded; is the server running with this configuration?", pos[0]))
	} else if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s: %sd\n", pos[0], name)
	return exitOK
}

func cmdHealthcheck(args []string, stdout, stderr io.Writer) int {
	c := newCommand("healthcheck", stderr, false)
	url := c.fs.String("url", "", "health URL (default from the configured listen address)")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	target := *url
	if target == "" {
		cfg, err := c.config()
		if err != nil {
			return fail(stderr, err)
		}
		addr := cfg.Server.Listen
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		target = "http://" + addr + "/healthz"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(target) //nolint:noctx // a one-shot probe with a client timeout
	if err != nil {
		return fail(stderr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	if resp.StatusCode != http.StatusOK {
		return exitInvalid
	}
	return exitOK
}
