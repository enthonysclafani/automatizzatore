package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const manifestFor = `apiVersion: pendolo/v1
kind: Automation
metadata: { name: NAME }
triggers: [ { manual: {} } ]
steps: [ { id: a, type: shell, run: "true" } ]
`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "one", "automation.yaml"), strings.Replace(manifestFor, "NAME", "one", 1))
	write(t, filepath.Join(dir, "copy", "automation.yaml"), strings.Replace(manifestFor, "NAME", "one", 1))
	write(t, filepath.Join(dir, "bad", "automation.yaml"), "kind: nope\n")
	write(t, filepath.Join(dir, "notes", "readme.md"), "not an automation")
	write(t, filepath.Join(dir, ".hidden", "automation.yaml"), strings.Replace(manifestFor, "NAME", "hidden", 1))

	snap, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Valid) != 1 || snap.Valid["one"].Manifest == nil {
		t.Fatalf("valid = %+v", snap.Valid)
	}
	if len(snap.Invalid) != 2 {
		t.Fatalf("invalid = %+v", snap.Invalid)
	}
	// Folders load in name order, so "copy" claims the name and "one" is the duplicate.
	dup := snap.Invalid[1]
	if dup.Name != "one" || !strings.Contains(dup.Problems[0].Message, "already used") {
		t.Fatalf("duplicate not reported: %+v", snap.Invalid)
	}
}

func TestHashChangesWithAnyFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "automation.yaml"), strings.Replace(manifestFor, "NAME", "x", 1))
	write(t, filepath.Join(dir, "prompts", "p.md"), "v1")
	a1, inv := LoadFolder(dir)
	if inv != nil {
		t.Fatalf("invalid: %+v", inv)
	}
	write(t, filepath.Join(dir, "prompts", "p.md"), "v2")
	a2, _ := LoadFolder(dir)
	if a1.Hash == a2.Hash || len(a1.Hash) != 16 {
		t.Fatalf("hash did not change with a prompt edit: %s %s", a1.Hash, a2.Hash)
	}
}
