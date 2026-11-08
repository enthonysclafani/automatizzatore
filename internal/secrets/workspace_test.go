package secrets

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

func TestWorkspaceProvider(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _ := vault.New(make([]byte, 32))
	ctx := context.Background()
	_ = st.EnsureWorkspace(ctx, "ws-a", time.Now())
	_ = st.EnsureWorkspace(ctx, "ws-b", time.Now())
	sealed, _ := v.Seal([]byte("from-dashboard"), SealContext("ws-a", "api-key"))
	_ = st.PutSecret(ctx, "ws-a", "api-key", sealed, "admin", time.Now())

	fallback := Static{"api-key": "from-files", "only-files": "file-value"}
	all := func(string) bool { return true }
	a := Workspace{Store: st, Vault: v, ID: "ws-a", Fallback: fallback, FallbackAllowed: all, Actor: "automation:alpha", RunID: "r1"}
	b := Workspace{Store: st, Vault: v, ID: "ws-b", Fallback: fallback, FallbackAllowed: all, Actor: "automation:beta", RunID: "r2"}

	if got, err := a.Get("api-key"); err != nil || got != "from-dashboard" {
		t.Fatalf("ws-a api-key = %q, %v (dashboard value wins over files)", got, err)
	}
	if got, _ := b.Get("api-key"); got != "from-files" {
		t.Fatalf("ws-b sees ws-a's secret: %q", got)
	}
	if got, _ := a.Get("only-files"); got != "file-value" {
		t.Fatalf("fallback not used: %q", got)
	}
	// Instance secrets a workspace may not use are not found.
	c := Workspace{Store: st, Vault: v, ID: "ws-c", Fallback: fallback, FallbackAllowed: func(n string) bool { return n == "api-key" }}
	if _, err := c.Get("only-files"); err == nil {
		t.Fatal("a workspace read an instance secret it may not use")
	}
	// A sealed value copied into another workspace does not open.
	_ = st.PutSecret(ctx, "ws-b", "api-key", sealed, "attacker", time.Now())
	if _, err := b.Get("api-key"); err == nil {
		t.Fatal("value sealed for ws-a opened in ws-b")
	}
	entries, _ := st.ListAudit(ctx, "ws-a", 10)
	if len(entries) != 2 || entries[1].Action != "secret.read" || entries[1].Actor != "automation:alpha" || entries[1].Detail["run"] != "r1" {
		t.Fatalf("audit = %+v", entries)
	}
}
