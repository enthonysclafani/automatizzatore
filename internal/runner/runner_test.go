package runner

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func automation(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	dir = filepath.Join(root, "checks")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "hello.sh"), []byte("#!/bin/sh\necho \"hello $1 $WHO\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func TestLocal(t *testing.T) {
	t.Setenv("PENDOLO_SECRET_X", "leak")
	_, dir := automation(t)
	ctx := context.Background()

	res, err := Local{}.Exec(ctx, Job{Dir: dir, Script: "scripts/hello.sh", Args: []string{"a"}, Env: map[string]string{"WHO": "b"}})
	if err != nil || res.ExitCode != 0 || res.Stdout != "hello a b\n" {
		t.Fatalf("script: %+v %v", res, err)
	}
	res, err = Local{}.Exec(ctx, Job{Dir: dir, Run: "env; echo oops >&2; exit 7"})
	if err != nil || res.ExitCode != 7 || res.Stderr != "oops\n" || strings.Contains(res.Stdout, "leak") {
		t.Fatalf("inline: %+v %v", res, err)
	}
	start := time.Now()
	res, err = Local{}.Exec(ctx, Job{Dir: dir, Run: "sleep 30 & wait", Timeout: 200 * time.Millisecond})
	if err != nil || !res.Killed || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %+v %v after %s", res, err, time.Since(start))
	}
	for _, bad := range []Job{
		{Dir: dir, Script: "../x.sh"},
		{Dir: dir, Script: "/bin/sh"},
		{Dir: dir},
		{Dir: dir, Run: "true", Script: "scripts/hello.sh"},
		{Dir: filepath.Join(dir, "missing"), Run: "true"},
	} {
		if _, err := (Local{}).Exec(ctx, bad); err == nil {
			t.Errorf("job %+v accepted", bad)
		}
	}
}

func TestRemote(t *testing.T) {
	root, dir := automation(t)
	srv := httptest.NewServer(Handler{Root: root, Token: "t0ken"})
	defer srv.Close()
	ctx := context.Background()

	// The server side may keep automations elsewhere: only the relative folder travels.
	serverRoot := filepath.Join(t.TempDir(), "automations")
	serverDir := filepath.Join(serverRoot, "checks")
	r := Remote{URL: srv.URL, Token: "t0ken", Root: serverRoot}

	if err := r.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := r.Exec(ctx, Job{Dir: serverDir, Script: "scripts/hello.sh", Args: []string{"x"}, Env: map[string]string{"WHO": "y"}})
	if err != nil || res.Stdout != "hello x y\n" {
		t.Fatalf("remote exec: %+v %v", res, err)
	}
	if _, err := r.Exec(ctx, Job{Dir: t.TempDir(), Run: "true"}); err == nil || !strings.Contains(err.Error(), "outside automationsDir") {
		t.Fatalf("folder outside root: %v", err)
	}

	wrong := Remote{URL: srv.URL, Token: "nope", Root: serverRoot}
	if err := wrong.Ping(ctx); err == nil {
		t.Fatal("wrong token accepted by ping")
	}
	if _, err := wrong.Exec(ctx, Job{Dir: serverDir, Run: "true"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong token: %v", err)
	}
	if err := (Remote{URL: srv.URL, Token: "", Root: serverRoot}).Ping(ctx); err == nil {
		t.Fatal("empty token accepted")
	}

	// A caller that gives up kills the script on the runner.
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err = r.Exec(cctx, Job{Dir: serverDir, Run: "sleep 30"})
	if err != nil || !res.Killed || time.Since(start) > 10*time.Second {
		t.Fatalf("cancel: %+v %v", res, err)
	}
	_ = dir
}
