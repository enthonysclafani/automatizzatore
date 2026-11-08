package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
)

// maxBody caps how much of a response body is read and stored.
const maxBody = 1 << 20

func (ex *execution) execHTTP(ctx context.Context, st *manifest.Step, env expr.Env) stepOutcome {
	req := st.Request
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	rawURL, err := ex.renderText(req.URL, env)
	if err != nil {
		return stepOutcome{err: err}
	}

	headers := map[string]string{}
	for k, v := range req.Headers {
		if headers[k], err = ex.renderText(v, env); err != nil {
			return stepOutcome{err: err}
		}
	}

	var body io.Reader
	contentType := ""
	if req.Body != nil {
		bv, err := ex.render(req.Body, env)
		if err != nil {
			return stepOutcome{err: err}
		}
		if s, ok := bv.(string); ok {
			body = strings.NewReader(s)
		} else {
			b, err := json.Marshal(bv)
			if err != nil {
				return stepOutcome{err: fmt.Errorf("encode body: %w", err)}
			}
			body, contentType = bytes.NewReader(b), "application/json"
		}
	}

	input := map[string]any{"method": method, "url": rawURL}
	if len(headers) > 0 {
		names := make([]string, 0, len(headers))
		for k := range headers {
			names = append(names, k)
		}
		input["headers"] = names // names only: values are often credentials
	}

	if ex.dry && method != http.MethodGet && method != http.MethodHead {
		return stepOutcome{input: input, output: map[string]any{"status": 0, "ok": true, "body": "", "dryRun": true},
			logs: dryNote + ": " + method + " requests can change things"}
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return stepOutcome{input: input, err: err}
	}
	httpReq.Header.Set("User-Agent", "pendolo")
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := ex.e.httpClient().Do(httpReq)
	if err != nil {
		return stepOutcome{input: input, err: fmt.Errorf("request failed: %w", err)}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	latency := time.Since(start)
	truncated := len(raw) > maxBody
	if truncated {
		raw = raw[:maxBody]
	}

	respHeaders := map[string]string{}
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	out := map[string]any{
		"status":    resp.StatusCode,
		"ok":        resp.StatusCode < 400,
		"latencyMs": latency.Milliseconds(),
		"headers":   respHeaders,
		"body":      string(raw),
		"truncated": truncated,
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") && !truncated {
		var parsed any
		if json.Unmarshal(raw, &parsed) == nil {
			out["json"] = parsed
		}
	}
	o := stepOutcome{input: input, output: out}
	if readErr != nil {
		o.err = fmt.Errorf("read response: %w", readErr)
		return o
	}
	o.err = checkExpect(st.Expect, resp.StatusCode, latency, string(raw))
	return o
}

func checkExpect(exp *manifest.HTTPExpect, status int, latency time.Duration, body string) error {
	if exp == nil || len(exp.Status) == 0 {
		if status >= 400 {
			return fmt.Errorf("HTTP %d", status)
		}
	} else {
		ok := false
		for _, s := range exp.Status {
			ok = ok || s == status
		}
		if !ok {
			return fmt.Errorf("HTTP %d, expected %v", status, exp.Status)
		}
	}
	if exp == nil {
		return nil
	}
	if exp.MaxLatency != "" {
		if limit, err := time.ParseDuration(exp.MaxLatency); err == nil && latency > limit {
			return fmt.Errorf("response took %s, more than %s", latency.Round(time.Millisecond), limit)
		}
	}
	if exp.BodyContains != "" && !strings.Contains(body, exp.BodyContains) {
		return fmt.Errorf("response body does not contain %q", exp.BodyContains)
	}
	return nil
}
