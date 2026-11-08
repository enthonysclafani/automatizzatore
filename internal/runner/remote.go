package runner

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// maxJobTimeout caps how long the runner lets one job run.
const maxJobTimeout = 6 * time.Hour

// Remote sends jobs to a `pendolo runner` over HTTP.
type Remote struct {
	// URL of the runner, e.g. http://runner:8090.
	URL   string
	Token string
	// Root is this process's automations folder; job folders are sent
	// relative to it, and the runner resolves them against its own.
	Root   string
	Client *http.Client
}

// Exec implements Executor.
func (r Remote) Exec(ctx context.Context, job Job) (Result, error) {
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return Result{}, err
	}
	dir, err := filepath.Abs(job.Dir)
	if err != nil {
		return Result{}, err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return Result{}, fmt.Errorf("automation folder %s is outside automationsDir %s, which the runner cannot see", job.Dir, r.Root)
	}
	job.Dir = filepath.ToSlash(rel)

	body, err := json.Marshal(job)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.URL, "/")+"/v1/exec", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("runner: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{Killed: true, ExitCode: -1}, nil
		}
		return Result{}, fmt.Errorf("runner unreachable at %s: %w", r.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*MaxOutput))
	if err != nil {
		return Result{}, fmt.Errorf("runner: read answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("runner: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return Result{}, fmt.Errorf("runner: invalid answer: %w", err)
	}
	return res, nil
}

// Ping checks that the runner answers and accepts the token.
func (r Remote) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.URL, "/")+"/v1/ping", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("runner unreachable")
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runner answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// Handler serves jobs for Remote. It is what `pendolo runner` runs.
type Handler struct {
	// Root is the runner's automations folder.
	Root  string
	Token string
	// Exec runs the jobs; nil means Local.
	Exec Executor
}

// ServeHTTP implements http.Handler.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if !h.authorized(r) {
		http.Error(w, "missing or wrong token", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/v1/ping" && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, "ok\n")
	case r.URL.Path == "/v1/exec" && r.Method == http.MethodPost:
		h.exec(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && h.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) == 1
}

func (h Handler) exec(w http.ResponseWriter, r *http.Request) {
	var job Job
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		http.Error(w, "invalid job: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := filepath.FromSlash(job.Dir)
	if !filepath.IsLocal(dir) {
		http.Error(w, "job folder must be inside the automations folder", http.StatusBadRequest)
		return
	}
	job.Dir = filepath.Join(h.Root, dir)
	if job.Timeout <= 0 || job.Timeout > maxJobTimeout {
		job.Timeout = maxJobTimeout
	}
	exec := h.Exec
	if exec == nil {
		exec = Local{}
	}
	// The request context ends if the caller gives up, which kills the script.
	res, err := exec.Exec(r.Context(), job)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
