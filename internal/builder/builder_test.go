package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/testfakes"
	"github.com/enthonysclafani/automatizzatore/templates"
)

const minimal = `apiVersion: pendolo/v1
kind: Automation
metadata: { name: hello, workspace: acme }
triggers: [ { manual: {} } ]
steps: [ { id: say, type: shell, run: "echo hi" } ]
`

func ws() Workspace {
	return Workspace{ID: "acme", Taken: map[string]bool{"taken": true}, Models: []string{"smart"}, Secrets: []string{"db"}}
}

func messages(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}

func TestCheckPaths(t *testing.T) {
	for _, tc := range []struct {
		files Files
		want  string
	}{
		{Files{"prompts/a.md": "x"}, "has no automation.yaml"},
		{Files{"automation.yaml": minimal, "../evil": "x"}, "relative"},
		{Files{"automation.yaml": minimal, "/etc/passwd": "x"}, "relative"},
		{Files{"automation.yaml": minimal, ".env": "x"}, "relative"},
		{Files{"automation.yaml": minimal, "a/./b": "x"}, "relative"},
		{Files{"automation.yaml": minimal, "a/b/c/d/e/f": "x"}, "relative"},
		{Files{"automation.yaml": minimal, "big.md": strings.Repeat("x", MaxFileBytes+1)}, "KiB"},
		{Files{"automation.yaml": minimal, "bin": "\xff\xfe"}, "UTF-8"},
		{Files{"automation.yaml": minimal, "a": "x", "a/b": "y"}, "both a file and a folder"},
	} {
		got := messages(CheckPaths(tc.files))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%v: got %q, want %q", tc.files.Paths(), got, tc.want)
		}
	}
	if ps := CheckPaths(Files{"automation.yaml": minimal, "prompts/x.md": "y", "scripts/run.sh": "z"}); len(ps) != 0 {
		t.Fatalf("valid paths rejected: %s", messages(ps))
	}
}

func TestCheckWorkspace(t *testing.T) {
	tmp := t.TempDir()
	ok := Check(Files{"automation.yaml": minimal}, ws(), tmp)
	if HasErrors(ok.Problems) || ok.Manifest == nil || ok.Hash == "" {
		t.Fatalf("minimal: %s", messages(ok.Problems))
	}
	for name, tc := range map[string]struct {
		yaml string
		ws   func(*Workspace)
		want string
		err  bool
	}{
		"other workspace": {strings.Replace(minimal, "workspace: acme", "workspace: other", 1), nil, `set metadata.workspace to "acme"`, true},
		"name taken":      {strings.Replace(minimal, "name: hello", "name: taken", 1), nil, "already exists", true},
		"rename":          {minimal, func(w *Workspace) { w.BaseName = "old" }, `keep metadata.name "old"`, true},
		"unknown action": {strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: connector, action: gmail.explode }`, 1),
			nil, `unknown action "gmail.explode"`, true},
		"missing connection": {strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: connector, action: slack.send_message, with: { channel: "#a", text: "b" } }`, 1),
			nil, "needs a slack connection", false},
		"mcp tool unknown": {strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: connector, action: seo.nope }`, 1),
			func(w *Workspace) {
				w.Connections = []Connection{{Name: "seo", Kind: connectors.KindMCP, Actions: []connectors.ActionInfo{{Name: "seo.get"}}}}
			}, `has no tool "seo.nope"`, true},
		"wrong kind": {strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: connector, action: slack.send_message, connection: g, with: { channel: "#a", text: "b" } }`, 1),
			func(w *Workspace) { w.Connections = []Connection{{Name: "g", Kind: connectors.KindGoogle}} }, "is a google connection", true},
		"secret unknown": {strings.Replace(minimal, `run: "echo hi"`, `run: "echo hi", env: { K: "secret://nope" }`, 1), nil, `secret "nope" is not among`, false},
		"credential":     {strings.Replace(minimal, `echo hi`, `echo xoxb-1234567890-abcdefghij`, 1), nil, "looks like a credential", true},
		"autonomy cap":   {strings.Replace(minimal, "steps:", "autonomy: auto\nsteps:", 1), func(w *Workspace) { w.MaxAutonomy = "draft" }, "caps autonomy", false},
		"model":          {strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: llm, model: huge, promptText: "hi" }`, 1), nil, `model "huge" is not configured`, false},
	} {
		w := ws()
		if tc.ws != nil {
			tc.ws(&w)
		}
		res := Check(Files{"automation.yaml": tc.yaml}, w, tmp)
		got := messages(res.Problems)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
			continue
		}
		if HasErrors(res.Problems) != tc.err {
			t.Errorf("%s: errors = %v, want %v: %s", name, HasErrors(res.Problems), tc.err, got)
		}
	}
	// A prompt file that does not exist is reported.
	res := Check(Files{"automation.yaml": strings.Replace(minimal, `{ id: say, type: shell, run: "echo hi" }`, `{ id: say, type: llm, prompt: prompts/missing.md }`, 1)}, ws(), tmp)
	if !HasErrors(res.Problems) {
		t.Fatalf("missing prompt accepted")
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Fatalf("scratch files left behind: %v", entries)
	}
}

func TestTemplatesPassCheck(t *testing.T) {
	for _, name := range templates.Names() {
		res := Check(templates.Files(name), Workspace{ID: "default", Taken: map[string]bool{}}, t.TempDir())
		if HasErrors(res.Problems) {
			t.Errorf("%s: %s", name, messages(res.Problems))
		}
		// The hash matches what the automations folder computes.
		a, inv := registry.LoadFolder(filepath.Join("..", "..", "templates", name))
		if inv != nil || a.Hash != res.Hash {
			t.Errorf("%s: hash %s, folder %s", name, res.Hash, a.Hash)
		}
	}
}

func TestInstall(t *testing.T) {
	root := t.TempDir()
	files := Files{"automation.yaml": minimal, "scripts/run.sh": "#!/bin/sh\necho hi\n", "prompts/p.md": "hello"}
	dir, err := Install(root, "hello", files, false)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "scripts", "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("script mode: %v %v", info, err)
	}
	if _, err := Install(root, "hello", files, false); !errors.Is(err, ErrFolderExists) {
		t.Fatalf("overwrite without replace: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".keep"), []byte("x"), 0o644)
	if _, err := Install(root, "hello", Files{"automation.yaml": minimal}, true); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFolder(dir)
	if err != nil || len(got) != 1 || got["automation.yaml"] != minimal {
		t.Fatalf("after replace: %v %v", got.Paths(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".keep")); err != nil {
		t.Fatalf("hidden file lost: %v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("leftovers in the automations folder: %v", entries)
	}
	if _, err := Install(root, "../x", files, false); err == nil {
		t.Fatal("folder outside the root accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0xff}, 0o644)
	if _, err := ReadFolder(dir); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("binary read: %v", err)
	}
}

func TestRunRepairsAndKeepsFiles(t *testing.T) {
	answer := func(reply string, files map[string]string) string {
		type f struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		fs := []f{}
		for p, c := range files {
			fs = append(fs, f{p, c})
		}
		b, _ := json.Marshal(map[string]any{"reply": reply, "files": fs})
		return string(b)
	}
	broken := strings.Replace(minimal, "type: shell", "type: shel", 1)
	script := &testfakes.Script{Answers: []string{
		answer("first", map[string]string{"automation.yaml": broken}),
		answer("still", map[string]string{"automation.yaml": broken}),
		answer("fixed", map[string]string{"automation.yaml": minimal}),
		answer("Only a question.", nil),
	}}
	gw := llm.NewWith(map[string]llm.Provider{"fake": script}, map[string]config.Model{"smart": {Provider: "fake", Model: "m"}})
	b := &Builder{LLM: gw, Model: "smart", TempDir: t.TempDir()}
	res, err := b.Run(context.Background(), Turn{Workspace: ws(), Message: "say hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "fixed" || HasErrors(res.Checked.Problems) || len(res.Calls) != 3 {
		t.Fatalf("res = %+v %s", res, messages(res.Checked.Problems))
	}
	sys := script.SeenComplete[0].System
	for _, want := range []string{"# The automation format", "pendolo/v1", "Template site-checks", "secret://"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	user := script.SeenComplete[0].User
	for _, want := range []string{"metadata.workspace: acme", "say hi", "## Secrets\ndb"} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt lacks %q:\n%s", want, user)
		}
	}
	// A reply without files keeps the draft as it is.
	res, err = b.Run(context.Background(), Turn{Workspace: ws(), Files: Files{"automation.yaml": minimal}, Message: "what does it do?"})
	if err != nil || res.Files["automation.yaml"] != minimal || res.Reply != "Only a question." {
		t.Fatalf("question turn = %+v, %v", res, err)
	}
	if _, err := (&Builder{}).Run(context.Background(), Turn{}); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no model: %v", err)
	}
	_ = manifest.FileName
}
