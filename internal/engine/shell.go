package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
)

// maxOutput caps the stdout and stderr kept from a shell step.
const maxOutput = 1 << 20

// limitedBuffer keeps the first maxOutput bytes and counts the rest.
type limitedBuffer struct {
	buf     bytes.Buffer
	dropped int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	room := maxOutput - l.buf.Len()
	if room <= 0 {
		l.dropped += len(p)
		return len(p), nil
	}
	if len(p) > room {
		l.buf.Write(p[:room])
		l.dropped += len(p) - room
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string {
	if l.dropped > 0 {
		return l.buf.String() + fmt.Sprintf("\n[%d bytes not kept]", l.dropped)
	}
	return l.buf.String()
}

func (ex *execution) execShell(ctx context.Context, st *manifest.Step, env expr.Env, path string) stepOutcome {
	argsV, err := expr.Value(st.Args, env)
	if err != nil {
		return stepOutcome{err: err}
	}
	args, _ := argsV.([]string)

	envV, err := expr.Value(st.Env, env)
	if err != nil {
		return stepOutcome{err: err}
	}
	stepEnv := map[string]string{}
	if m, ok := envV.(map[string]string); ok {
		for k, v := range m {
			if stepEnv[k], err = ex.resolver.String(v); err != nil {
				return stepOutcome{err: fmt.Errorf("env %s: %w", k, err)}
			}
		}
	}

	envNames := make([]string, 0, len(stepEnv))
	for k := range stepEnv {
		envNames = append(envNames, k)
	}
	sort.Strings(envNames)
	input := map[string]any{"args": args, "env": envNames}

	var cmd *exec.Cmd
	if st.Script != "" {
		script := filepath.Join(ex.a.Dir, filepath.FromSlash(st.Script))
		input["script"] = st.Script
		info, err := os.Stat(script)
		if err != nil {
			return stepOutcome{input: input, err: fmt.Errorf("script %s: %w", st.Script, err)}
		}
		if info.Mode()&0o111 != 0 {
			cmd = exec.CommandContext(ctx, script, args...) //nolint:gosec // running the automation's own script is what a shell step does
		} else {
			cmd = exec.CommandContext(ctx, shellPath(), append([]string{script}, args...)...) //nolint:gosec // same: the automation's script
		}
	} else {
		input["run"] = st.Run
		cmd = exec.CommandContext(ctx, shellPath(), append([]string{"-c", st.Run, "pendolo"}, args...)...) //nolint:gosec // inline shell code from the manifest; expressions are never pasted into it
	}
	cmd.Dir = ex.a.Dir
	cmd.Env = ex.shellEnv(stepEnv, path)
	prepareCmd(cmd)

	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	out := map[string]any{"exitCode": exitCode, "stdout": stdout.String(), "stderr": stderr.String()}
	var parsed any
	if trimmed := bytes.TrimSpace(stdout.buf.Bytes()); len(trimmed) > 0 && stdout.dropped == 0 && json.Unmarshal(trimmed, &parsed) == nil {
		out["json"] = parsed
	}

	logs := stdout.String()
	if stderr.buf.Len() > 0 {
		if logs != "" && !strings.HasSuffix(logs, "\n") {
			logs += "\n"
		}
		logs += stderr.String()
	}
	o := stepOutcome{input: input, output: out, logs: logs}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && ctx.Err() == nil {
			o.err = fmt.Errorf("exit status %d%s", exitCode, lastLine(stderr.buf.String()))
		} else {
			o.err = runErr
		}
	}
	return o
}

// shellEnv is a small, explicit environment: the step never sees Pendolo's
// own variables (API keys, secret values in PENDOLO_SECRET_*).
func (ex *execution) shellEnv(stepEnv map[string]string, path string) []string {
	base := map[string]string{
		"PATH":               os.Getenv("PATH"),
		"HOME":               os.TempDir(),
		"TMPDIR":             os.TempDir(),
		"LANG":               "C.UTF-8",
		"PENDOLO_RUN_ID":     ex.run.ID,
		"PENDOLO_AUTOMATION": ex.a.Name,
		"PENDOLO_STEP":       path,
	}
	if base["PATH"] == "" {
		base["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	for k, v := range stepEnv {
		base[k] = v
	}
	out := make([]string, 0, len(base))
	for k, v := range base {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func shellPath() string {
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "sh"
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
