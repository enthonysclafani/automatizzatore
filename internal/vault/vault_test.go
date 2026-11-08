package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpen(t *testing.T) {
	v, err := New(make([]byte, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := v.Seal([]byte("hunter2"), "secret:a:x")
	s2, _ := v.Seal([]byte("hunter2"), "secret:a:x")
	if s1 == s2 {
		t.Fatal("two seals of the same value are identical: nonce reused")
	}
	got, err := v.Open(s1, "secret:a:x")
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := v.Open(s1, "secret:b:x"); err == nil {
		t.Fatal("value opened under another context")
	}
	other, _ := New([]byte("0123456789abcdef0123456789abcdef"))
	if _, err := other.Open(s1, "secret:a:x"); err == nil {
		t.Fatal("value opened with another key")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	k1, src, err := LoadKey("", "", dir)
	if err != nil || src != FromGenerated || len(k1) != KeySize {
		t.Fatalf("generate: %v %v", src, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "master.key"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", info.Mode().Perm())
	}
	k2, src, _ := LoadKey("", "", dir)
	if string(k1) != string(k2) || src != FromFile {
		t.Fatal("generated key not reused")
	}
	enc, _ := NewKey()
	k3, src, err := LoadKey(enc, "", dir)
	if err != nil || src != FromEnv || string(k3) == string(k1) {
		t.Fatalf("env key: %v %v", src, err)
	}
	if _, _, err := LoadKey("bm90IGEga2V5", "", dir); err == nil {
		t.Fatal("short env key accepted")
	}
}
