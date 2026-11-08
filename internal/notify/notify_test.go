package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

func TestShouldNotify(t *testing.T) {
	ok := &store.Run{Status: store.RunSucceeded}
	degraded := &store.Run{Status: store.RunSucceeded, Anomalies: 1}
	failed := &store.Run{Status: store.RunFailed}
	cases := []struct {
		on   string
		run  *store.Run
		want bool
	}{
		{"", ok, false}, {"", failed, true}, {"failure", degraded, false},
		{"anomaly", degraded, true}, {"anomaly", ok, false},
		{"always", ok, true}, {"never", failed, false},
	}
	for _, tc := range cases {
		if got := ShouldNotify(tc.on, tc.run); got != tc.want {
			t.Errorf("ShouldNotify(%q, %+v) = %v", tc.on, tc.run, got)
		}
	}
}

func TestWebhookWithSecretURL(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	defer srv.Close()

	n := &Notifier{Secrets: secrets.Static{"alerts": srv.URL}}
	a := engine.Automation{Name: "site-checks", Manifest: &manifest.Manifest{
		Notifications: &manifest.Notifications{On: "failure", Channels: []string{"webhook:secret://alerts", "slack:#ops"}},
	}}
	n.Notify(context.Background(), a, &store.Run{ID: "r1", Status: store.RunFailed, Error: "step homepage: HTTP 503"})
	if got.RunID != "r1" || got.Text != "site-checks: run failed - step homepage: HTTP 503" || got.Content != got.Text {
		t.Fatalf("payload = %+v", got)
	}
}
