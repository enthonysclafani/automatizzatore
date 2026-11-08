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
