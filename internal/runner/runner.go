// Package runner executes the scripts of shell steps.
//
// The Local executor runs them as child processes. The Remote executor
// sends them to `pendolo runner`, a separate process (usually its own
// container) that has the automations folder and the tools, but no access
// to Pendolo's database, configuration or secrets: a script that misbehaves
// can only reach what the runner can.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// MaxOutput caps the stdout and stderr kept from a script.
const MaxOutput = 1 << 20

// Job is one script execution.
type Job struct {
	// Dir is the automation folder: absolute for Local, relative to the
	// runner's automations folder for Remote.
	Dir string `json:"dir"`
	// Exactly one of Script (a path inside Dir) and Run (inline code).
	Script string   `json:"script,omitempty"`
	Run    string   `json:"run,omitempty"`
	Args   []string `json:"args,omitempty"`
	// Env is added to a minimal base environment (PATH, HOME, TMPDIR, LANG).
	Env map[string]string `json:"env,omitempty"`
	// Timeout bounds the execution; zero means only the caller's context.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// Result is what a script produced. A non-zero ExitCode is a result, not an
// error: errors mean the script could not run at all.
type Result struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// Truncated is true when output beyond MaxOutput was dropped.
	Truncated bool `json:"truncated,omitempty"`
	// Killed is true when the script was stopped by a timeout or cancellation.
	Killed bool `json:"killed,omitempty"`
}

// Executor runs jobs.
type Executor interface {
	Exec(ctx context.Context, job Job) (Result, error)
}

// Local runs jobs as child processes of this process.
type Local struct{}

// Exec implements Executor.
func (Local) Exec(ctx context.Context, job Job) (Result, error) {
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	info, err := os.Stat(job.Dir)
	if err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("automation folder %q not found", job.Dir)
	}

	var cmd *exec.Cmd
	switch {
	case job.Script != "" && job.Run != "":
		return Result{}, errors.New("a job has either a script or inline code, not both")
	case job.Script != "":
		if !filepath.IsLocal(job.Script) {
			return Result{}, fmt.Errorf("script %q must stay inside the automation folder", job.Script)
		}
		script := filepath.Join(job.Dir, job.Script)
		info, err := os.Stat(script)
		if err != nil {
			return Result{}, fmt.Errorf("script %s: %w", job.Script, err)
		}
		if info.Mode()&0o111 != 0 {
			cmd = exec.CommandContext(ctx, script, job.Args...) //nolint:gosec // running the automation's own script is what a shell step does
		} else {
			cmd = exec.CommandContext(ctx, shellPath(), append([]string{script}, job.Args...)...) //nolint:gosec // same: the automation's script
		}
	case job.Run != "":
		cmd = exec.CommandContext(ctx, shellPath(), append([]string{"-c", job.Run, "pendolo"}, job.Args...)...) //nolint:gosec // inline code from the manifest; expressions are never pasted into it
	default:
		return Result{}, errors.New("a job needs a script or inline code")
	}
	cmd.Dir = job.Dir
	cmd.Env = environment(job.Env)
	prepareCmd(cmd)

	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.dropped+stderr.dropped > 0}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		switch {
		case ctx.Err() != nil:
			res.Killed = true
			if res.ExitCode == 0 {
				res.ExitCode = -1
			}
		case errors.As(runErr, &exitErr):
		default:
			return res, runErr
		}
	}
	return res, nil
}

// environment is a small, explicit environment: scripts never see the
// variables of the process that runs them (API keys, PENDOLO_SECRET_*).
func environment(extra map[string]string) []string {
	base := map[string]string{
		"PATH":   os.Getenv("PATH"),
		"HOME":   os.TempDir(),
		"TMPDIR": os.TempDir(),
		"LANG":   "C.UTF-8",
	}
	if base["PATH"] == "" {
		base["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	for k, v := range extra {
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

// limitedBuffer keeps the first MaxOutput bytes and counts the rest.
type limitedBuffer struct {
	buf     bytes.Buffer
	dropped int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	room := MaxOutput - l.buf.Len()
	switch {
	case room <= 0:
		l.dropped += len(p)
	case len(p) > room:
		l.buf.Write(p[:room])
		l.dropped += len(p) - room
	default:
		l.buf.Write(p)
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	if l.dropped > 0 {
		return l.buf.String() + fmt.Sprintf("\n[%d bytes not kept]", l.dropped)
	}
	return l.buf.String()
}
