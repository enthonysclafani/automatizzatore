package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/runner"
)

func (e *Engine) shell() runner.Executor {
	if e.Shell != nil {
		return e.Shell
	}
	return runner.Local{}
}

func (ex *execution) execShell(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	argsV, err := expr.Value(st.Args, env)
	if err != nil {
		return stepOutcome{err: err}
	}
	args, _ := argsV.([]string)

	jobEnv := map[string]string{
		"PENDOLO_RUN_ID":     ex.run.ID,
		"PENDOLO_AUTOMATION": ex.a.Name,
		"PENDOLO_STEP":       path,
	}
	var envNames []string
	for k, v := range st.Env {
		if jobEnv[k], err = ex.renderText(v, env); err != nil {
			return stepOutcome{err: fmt.Errorf("env %s: %w", k, err)}
		}
		envNames = append(envNames, k)
	}
	sort.Strings(envNames)

	job := runner.Job{Dir: ex.a.Dir, Script: st.Script, Run: st.Run, Args: args, Env: jobEnv}
	if deadline, ok := ctx.Deadline(); ok {
		job.Timeout = time.Until(deadline)
	}
	input := map[string]any{"args": args, "env": envNames}
	if st.Script != "" {
		input["script"] = st.Script
	} else {
		input["run"] = st.Run
	}

	res, err := ex.e.shell().Exec(ctx, job)
	if err != nil {
		return stepOutcome{input: input, err: err}
	}

	out := map[string]any{"exitCode": res.ExitCode, "stdout": res.Stdout, "stderr": res.Stderr}
	var parsed any
	if trimmed := strings.TrimSpace(res.Stdout); trimmed != "" && !res.Truncated && json.Unmarshal([]byte(trimmed), &parsed) == nil {
		out["json"] = parsed
	}
	logs := res.Stdout
	if res.Stderr != "" {
		if logs != "" && !strings.HasSuffix(logs, "\n") {
			logs += "\n"
		}
		logs += res.Stderr
	}
	o := stepOutcome{input: input, output: out, logs: logs}
	switch {
	case res.Killed:
		o.err = fmt.Errorf("stopped before finishing: %w", context.Cause(ctx))
		if ctx.Err() == nil {
			o.err = fmt.Errorf("stopped before finishing")
		}
	case res.ExitCode != 0:
		o.err = fmt.Errorf("exit status %d%s", res.ExitCode, lastLine(res.Stderr))
	}
	return o
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return ": " + s
}
