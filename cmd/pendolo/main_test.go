package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestValidateTemplates(t *testing.T) {
	code, out, errOut := runCLI("validate", "--strict", "../../templates")
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "4 of 4 automations valid") {
		t.Fatalf("unexpected summary:\n%s", out)
	}
}

func TestValidateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "automation.yaml")
	if err := os.WriteFile(bad, []byte("apiVersion: pendolo/v1\nkind: Automation\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runCLI("validate", "--format", "json", bad)
	if code != exitInvalid {
		t.Fatalf("exit %d, want %d", code, exitInvalid)
	}
	var reports []fileReport
	if err := json.Unmarshal([]byte(out), &reports); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(reports) != 1 || reports[0].Valid || len(reports[0].Problems) == 0 {
		t.Fatalf("unexpected report: %+v", reports)
	}
}

func TestStrictFailsOnWarnings(t *testing.T) {
	dir := t.TempDir()
	m := "apiVersion: pendolo/v1\nkind: Automation\nmetadata: { name: w }\ntriggers:\n  - schedule: { cron: \"@daily\" }\nsteps:\n  - { id: a, type: http, request: { url: x } }\n"
	path := filepath.Join(dir, "automation.yaml")
	if err := os.WriteFile(path, []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runCLI("validate", path); code != exitOK {
		t.Fatalf("warnings alone must pass without --strict; exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI("validate", "--strict", path); code != exitInvalid {
		t.Fatalf("--strict must fail on warnings; exit %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"nope"},
		{"validate"},
		{"validate", "--format", "xml", "x"},
		{"validate", "does-not-exist.yaml"},
		{"validate", t.TempDir()},
	}
	for _, args := range cases {
		if code, _, _ := runCLI(args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestSchemaCommand(t *testing.T) {
	code, out, _ := runCLI("schema")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if v["title"] == nil {
		t.Fatal("schema has no title")
	}
}

func TestVersionCommand(t *testing.T) {
	code, out, _ := runCLI("version")
	if code != exitOK || !strings.HasPrefix(out, "pendolo ") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

func TestRunRunsShowAndPause(t *testing.T) {
	data := t.TempDir()
	autos := t.TempDir()
	t.Setenv("PENDOLO_DATA_DIR", data)
	t.Setenv("PENDOLO_AUTOMATIONS_DIR", autos)
	dir := filepath.Join(autos, "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := "apiVersion: pendolo/v1\nkind: Automation\nmetadata: { name: hello }\ntriggers: [ { manual: {} } ]\n" +
		"steps:\n  - id: greet\n    type: shell\n    run: echo \"hello $WHO\"\n    env: { WHO: \"${automation.name}\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "automation.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI("run", "hello")
	if code != exitOK || !strings.Contains(out, "SUCCEEDED") || !strings.Contains(out, "hello hello") {
		t.Fatalf("run by name: exit %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCLI("run", dir); code != exitOK || !strings.Contains(out, "ok   greet") {
		t.Fatalf("run by folder: exit %d\n%s", code, out)
	}

	code, out, _ = runCLI("runs", "--json")
	var runs []map[string]any
	if err := json.Unmarshal([]byte(out), &runs); err != nil || code != exitOK || len(runs) != 2 {
		t.Fatalf("runs: exit %d err %v\n%s", code, err, out)
	}
	id, _ := runs[0]["id"].(string)
	if code, out, _ := runCLI("show", id); code != exitOK || !strings.Contains(out, "greet") {
		t.Fatalf("show: exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI("show", "nope"); code != exitInvalid {
		t.Fatalf("show of a missing run: exit %d", code)
	}
	if code, out, _ := runCLI("trigger", "hello"); code != exitOK || !strings.Contains(out, "queued run") {
		t.Fatalf("trigger: exit %d\n%s", code, out)
	}
	// pause needs the automation recorded by a server; without it the error says so.
	if code, _, errOut := runCLI("pause", "hello"); code != exitInvalid || !strings.Contains(errOut, "server") {
		t.Fatalf("pause before any server: exit %d %s", code, errOut)
	}
	if code, _, errOut := runCLI("run", "missing"); code != exitInvalid || !strings.Contains(errOut, "no automation named") {
		t.Fatalf("run of a missing automation: exit %d %s", code, errOut)
	}
}
