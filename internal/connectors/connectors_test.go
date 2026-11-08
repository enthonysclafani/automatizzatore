package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

func hub(t *testing.T) *Hub {
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	v, _ := vault.New(make([]byte, 32))
	_ = st.EnsureWorkspace(context.Background(), "w", time.Now())
	return &Hub{Store: st, Vault: v}
}

func TestWebFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Inside &amp; secret</title><body><p>internal</p><script>x()</script></body></html>"))
	}))
	defer srv.Close()
	h := hub(t)
	b, err := h.Resolve(context.Background(), "w", "web.fetch", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(context.Background(), map[string]any{"url": srv.URL}); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("loopback fetch allowed: %v", err)
	}
	if _, err := b.Call(context.Background(), map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("file URL accepted")
	}
	h.AllowPrivateFetch = true
	out, err := b.Call(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	page := out.(map[string]any)
	if page["title"] != "Inside & secret" || page["text"] != "internal" {
		t.Fatalf("page = %+v", page)
	}
}

func TestResolveErrors(t *testing.T) {
	h := hub(t)
	ctx := context.Background()
	cases := map[string]string{
		"gmail.search":      "no google connection",
		"nodot":             "must look like",
		"openseo.something": `no connection "openseo"`,
	}
	for action, want := range cases {
		if _, err := h.Resolve(ctx, "w", action, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", action, err)
		}
	}
	sealed, _ := h.Seal("w", "chat", map[string]string{"token": "xoxb"})
	_ = h.Store.PutConnection(ctx, store.Connection{Workspace: "w", Name: "chat", Kind: KindSlack, Sealed: sealed, Status: store.ConnReady, UpdatedAt: time.Now()})
	b, err := h.Resolve(ctx, "w", "slack.send_message", "")
	if err != nil || b.Risk != RiskSend || b.Connection != "chat" {
		t.Fatalf("single slack connection not found: %+v %v", b, err)
	}
	if _, err := h.Resolve(ctx, "w", "slack.delete_everything", ""); err == nil || !strings.Contains(err.Error(), "send_message") {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := b.Call(ctx, map[string]any{"channel": "#x"}); err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("missing input not caught: %v", err)
	}
	// A sealed value moved to another connection does not open.
	_ = h.Store.PutConnection(ctx, store.Connection{Workspace: "w", Name: "copy", Kind: KindSlack, Sealed: sealed, Status: store.ConnReady, UpdatedAt: time.Now()})
	if _, err := h.Resolve(ctx, "w", "slack.send_message", "copy"); err == nil {
		t.Fatal("credentials opened under another name")
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("<h1>Title</h1><p>One&nbsp;line<br>two</p><style>p{}</style><ul><li>a</li><li>b</li></ul>")
	if got != "Title\nOne line\ntwo\na\nb" {
		t.Fatalf("got %q", got)
	}
}
