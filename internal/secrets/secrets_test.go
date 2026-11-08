package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "from-file"), []byte("file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PENDOLO_SECRET_SERVER_SSH_KEY": "env-value", "PENDOLO_SECRET_FROM_FILE": "ignored"}
	p := FilesAndEnv{Dir: dir, EnvPrefix: "PENDOLO_SECRET_", LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }}

	if v, err := p.Get("from-file"); err != nil || v != "file-value" {
		t.Fatalf("file secret = %q, %v (files win, trailing newline trimmed)", v, err)
	}
	if v, err := p.Get("server-ssh-key"); err != nil || v != "env-value" {
		t.Fatalf("env secret = %q, %v", v, err)
	}
	if _, err := p.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing secret: %v", err)
	}
	if _, err := p.Get("../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted as a secret name")
	}
}

func TestResolverAndRedact(t *testing.T) {
	r := NewResolver(Static{"token": "abc123", "long": "abc123-and-more", "pin": "12"})
	for _, ref := range []string{"secret://token", "secret://long", "secret://pin"} {
		if _, err := r.String(ref); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := r.String("plain text"); v != "plain text" {
		t.Fatalf("plain string changed: %q", v)
	}
	if _, err := r.String("secret://bad name"); err == nil {
		t.Fatal("invalid reference accepted")
	}
	got := r.Redact("a abc123-and-more b abc123 c 12")
	want := "a [secret:long] b [secret:token] c 12" // values under 4 characters are not masked
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
	v, err := r.Value(map[string]any{"h": []any{"secret://token", 3}})
	if err != nil || v.(map[string]any)["h"].([]any)[0] != "abc123" {
		t.Fatalf("Value = %#v, %v", v, err)
	}
}
