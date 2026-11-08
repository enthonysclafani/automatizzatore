package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

// SealContext binds a stored secret to its workspace and name.
func SealContext(workspace, name string) string { return "secret:" + workspace + ":" + name }

// Workspace reads secrets saved in the dashboard for one workspace, then
// falls back to the instance-wide provider (files and environment). Every
// value read from the dashboard store is recorded in the audit log.
type Workspace struct {
	Store    *store.Store
	Vault    *vault.Vault
	ID       string
	Fallback Provider
	// FallbackAllowed says which names may be read from Fallback; nil allows none.
	FallbackAllowed func(name string) bool
	// Actor and RunID describe who reads, for the audit log.
	Actor string
	RunID string
}

// Get implements Provider.
func (w Workspace) Get(name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", errors.New("invalid secret name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sealed, err := w.Store.GetSecret(ctx, w.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		if w.Fallback == nil || w.FallbackAllowed == nil || !w.FallbackAllowed(name) {
			return "", fmt.Errorf("%w: save it on the Secrets page of workspace %s", ErrNotFound, w.ID)
		}
		v, err := w.Fallback.Get(name)
		if err == nil {
			_ = w.Store.Audit(ctx, store.AuditEntry{
				At: time.Now(), Actor: w.Actor, Action: "secret.read", Workspace: w.ID, Target: name,
				Detail: map[string]any{"run": w.RunID, "source": "instance"},
			})
		}
		return v, err
	}
	if err != nil {
		return "", err
	}
	plain, err := w.Vault.Open(sealed, SealContext(w.ID, name))
	if err != nil {
		return "", err
	}
	_ = w.Store.Audit(ctx, store.AuditEntry{
		At: time.Now(), Actor: w.Actor, Action: "secret.read", Workspace: w.ID, Target: name,
		Detail: map[string]any{"run": w.RunID},
	})
	return string(plain), nil
}

// ValidName reports whether name can be used as a secret name.
func ValidName(name string) bool { return nameRe.MatchString(name) }
