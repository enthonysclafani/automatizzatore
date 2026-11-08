package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// maxHookBody caps the body a webhook may send.
const maxHookBody = 1 << 20

// hiddenHeaders are never stored with a webhook run.
var hiddenHeaders = map[string]bool{"authorization": true, "cookie": true, "x-pendolo-signature": true, "x-hub-signature-256": true, "x-hub-signature": true}

// handleHook queues a run of the automation named in the path. The caller
// proves it knows the trigger's secret with "Authorization: Bearer <secret>"
// or an HMAC-SHA256 of the body in X-Pendolo-Signature or
// X-Hub-Signature-256 ("sha256=<hex>", the GitHub format).
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a, ok := s.automations()[name]
	var hook *manifest.WebhookTrigger
	if ok {
		for _, t := range a.Manifest.Triggers {
			if t.Webhook != nil {
				hook = t.Webhook
				break
			}
		}
	}
	if hook == nil {
		http.Error(w, "no automation with a webhook trigger named "+name, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body larger than 1 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	if hook.Secret != "" {
		secret, err := s.resolveSecret(hook.Secret)
		if err != nil {
			s.log.Error("webhook secret", "automation", name, "err", err)
			http.Error(w, "webhook secret not available", http.StatusServiceUnavailable)
			return
		}
		if !hookAuthorized(r, body, secret) {
			http.Error(w, "missing or wrong credentials", http.StatusUnauthorized)
			return
		}
	}

	enabled, err := s.store.IsEnabled(r.Context(), name)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if !enabled {
		http.Error(w, name+" is paused", http.StatusConflict)
		return
	}

	data := map[string]any{"body": string(body), "headers": hookHeaders(r), "query": hookQuery(r)}
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		data["json"] = parsed
	}
	id, _, err := s.store.Enqueue(r.Context(), store.NewRun{
		Automation: name, Workspace: a.Workspace(), ManifestHash: a.Hash, TriggerType: "webhook", TriggerData: data,
	}, time.Now())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.log.Info("run enqueued", "automation", name, "trigger", "webhook", "run", id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"runId": id})
}

func hookAuthorized(r *http.Request, body []byte, secret string) bool {
	if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	}
	sig := r.Header.Get("X-Pendolo-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Hub-Signature-256")
	}
	hexSig, ok := strings.CutPrefix(sig, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func hookHeaders(r *http.Request) map[string]string {
	out := map[string]string{}
	for k := range r.Header {
		lk := strings.ToLower(k)
		if !hiddenHeaders[lk] {
			out[lk] = r.Header.Get(k)
		}
	}
	return out
}

func hookQuery(r *http.Request) map[string]string {
	out := map[string]string{}
	for k := range r.URL.Query() {
		if k != "token" {
			out[k] = r.URL.Query().Get(k)
		}
	}
	return out
}
