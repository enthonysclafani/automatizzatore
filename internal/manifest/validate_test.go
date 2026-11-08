package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// header is a valid manifest up to the steps list.
const header = `apiVersion: pendolo/v1
kind: Automation
metadata:
  name: test
triggers:
  - schedule: { cron: "0 8 * * *", timezone: Europe/Rome }
budget: { maxCostEur: 1 }
steps:
`

const okStep = `  - id: ping
    type: http
    request: { url: "https://example.com" }
`

func TestTemplatesAreValid(t *testing.T) {
	files, err := filepath.Glob("../../templates/*/" + FileName)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	for _, f := range files {
		t.Run(filepath.Base(filepath.Dir(f)), func(t *testing.T) {
			res, err := ValidateFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Problems) > 0 {
				t.Fatalf("expected no problems, got:\n%s", list(res.Problems))
			}
			if res.Manifest == nil {
				t.Fatal("manifest not decoded")
			}
		})
	}
}

func TestValidMinimal(t *testing.T) {
	res := Validate([]byte(header+okStep), Options{})
	if len(res.Problems) > 0 {
		t.Fatalf("unexpected problems:\n%s", list(res.Problems))
	}
	m := res.Manifest
	if m.Metadata.Name != "test" || len(m.Steps) != 1 || m.Steps[0].Request.URL != "https://example.com" {
		t.Fatalf("decoded manifest is wrong: %+v", m)
	}
	if got := m.EffectiveAutonomy(); got != AutonomyAsk {
		t.Fatalf("default autonomy = %q, want %q", got, AutonomyAsk)
	}
}

func TestInvalid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring of one error message
		line int    // expected line of that error, 0 to skip
	}{
		{"empty file", "", "the file is empty", 0},
		{"yaml syntax", "apiVersion: pendolo/v1\nkind: [Automation\n", "invalid YAML", 0},
		{"two documents", header + okStep + "---\napiVersion: pendolo/v1\n", "more than one YAML document", 0},
		{"merge key", header + "  - &a { id: a, type: http, request: { url: x } }\n  - <<: *a\n    id: b\n", "merge keys", 0},
		{"duplicate key", header + "  - id: a\n    id: b\n    type: http\n    request: { url: x }\n", "appears twice", 10},
		{"wrong apiVersion", strings.Replace(header, "pendolo/v1", "pendolo/v2", 1) + okStep, "pendolo/v1", 1},
		{"missing triggers", strings.Replace(header, "triggers:\n  - schedule: { cron: \"0 8 * * *\", timezone: Europe/Rome }\n", "", 1) + okStep, "triggers", 0},
		{"two kinds in one trigger", strings.Replace(header, "  - schedule:", "  - manual: {}\n    schedule:", 1) + okStep, "maxProperties", 0},
		{"bad name", strings.Replace(header, "name: test", "name: Test Automation", 1) + okStep, "does not match pattern", 4},
		{"unknown step type", header + "  - id: a\n    type: magic\n", "value must be one of", 0},
		{"connector without action", header + "  - id: a\n    type: connector\n", "missing property 'action'", 9},
		{"unknown step field", header + "  - id: a\n    type: http\n    request: { url: x }\n    colour: red\n", "colour", 9},
		{"bad step id", header + "  - id: Fetch-Mail\n    type: http\n    request: { url: x }\n", "does not match pattern", 9},
		{"bad duration", header + "  - id: a\n    type: http\n    timeout: 5 minutes\n    request: { url: x }\n", "does not match pattern", 11},
		{"shell with run and script", header + "  - id: a\n    type: shell\n    run: echo hi\n    script: s.sh\n", "oneOf", 0},
		{"agent without tools", header + "  - id: a\n    type: agent\n    promptText: go\n", "missing property 'tools'", 0},
		{"absolute script path", header + "  - id: a\n    type: shell\n    script: /etc/passwd\n", "does not match pattern", 11},
		{"script path with dotdot", header + "  - id: a\n    type: shell\n    script: scripts/../../x.sh\n", "leaves the automation folder", 11},
		{"invalid cron", strings.Replace(header, "0 8 * * *", "0 25 * * *", 1) + okStep, "invalid cron", 6},
		{"unknown timezone", strings.Replace(header, "Europe/Rome", "Europe/Atlantis", 1) + okStep, "unknown timezone", 6},
		{"local timezone", strings.Replace(header, "Europe/Rome", "Local", 1) + okStep, "depends on the host", 6},
		{"duplicate step id", header + okStep + okStep, "already used", 12},
		{"duplicate id in foreach", header + okStep + "  - id: loop\n    type: foreach\n    items: \"${steps.ping.output}\"\n    steps:\n" +
			"      - { id: ping, type: http, request: { url: x } }\n", "already used", 0},
		{"reference to later step", header + "  - id: a\n    type: http\n    request: { url: \"${steps.b.output}\" }\n" + strings.Replace(okStep, "ping", "b", 1),
			"runs later", 11},
		{"reference to unknown step", header + "  - id: a\n    type: http\n    request: { url: \"${steps.nope.output}\" }\n", "unknown step \"nope\"", 11},
		{"reference to itself", header + "  - id: a\n    type: http\n    request: { url: \"${steps.a.output}\" }\n", "its own output", 11},
		{"reference into foreach", header + okStep + "  - id: loop\n    type: foreach\n    items: \"${steps.ping.output}\"\n    steps:\n" +
			"      - { id: inner, type: http, request: { url: \"${item.url}\" } }\n" +
			"  - id: after\n    type: http\n    request: { url: \"${steps.inner.output}\" }\n", "inside foreach \"loop\"", 0},
		{"item outside foreach", header + "  - id: a\n    type: http\n    request: { url: \"${item.url}\" }\n", "only inside a foreach", 11},
		{"unknown root", header + "  - id: a\n    type: http\n    request: { url: \"${secret.token}\" }\n", "unknown name \"secret\"", 11},
		{"syntax error", header + "  - id: a\n    type: http\n    request: { url: \"${steps.a.output +}\" }\n", "invalid expression", 11},
		{"ref inside function", header + "  - id: a\n    type: http\n    request: { url: \"${len(steps.later.output)}\" }\n" + strings.Replace(okStep, "ping", "later", 1), "runs later", 11},
		{"expression in shell run", header + okStep + "  - id: a\n    type: shell\n    run: echo ${steps.ping.output.status}\n", "not evaluated in run", 14},
		{"bare steps", header + "  - id: a\n    type: http\n    request: { url: \"${steps}\" }\n", "must name a step", 11},
		{"unclosed expression", header + "  - id: a\n    type: http\n    request: { url: \"${steps.x.output\" }\n", "unclosed", 11},
		{"empty expression", header + "  - id: a\n    type: http\n    request: { url: \"${ }\" }\n", "empty expression", 11},
		{"if is not one expression", header + okStep + "  - id: a\n    type: http\n    if: steps.ping.failed\n    request: { url: x }\n", "single expression", 14},
		{"invalid output schema", header + "  - id: a\n    type: llm\n    promptText: hi\n    outputSchema: { type: banana }\n", "not a valid JSON Schema", 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate([]byte(tc.yaml), Options{})
			if !res.HasErrors() {
				t.Fatalf("expected an error containing %q, got none (problems: %v)", tc.want, res.Problems)
			}
			if res.Manifest != nil {
				t.Fatal("an invalid manifest must not be returned")
			}
			p, ok := find(res.Problems, SeverityError, tc.want)
			if !ok {
				t.Fatalf("no error contains %q; got:\n%s", tc.want, list(res.Problems))
			}
			if tc.line != 0 && p.Line != tc.line {
				t.Fatalf("error %q at line %d, want line %d", p.Message, p.Line, tc.line)
			}
		})
	}
}

func TestExpressionsWithFunctions(t *testing.T) {
	y := header + okStep + "  - id: a\n    type: shell\n    run: echo \"$N\"\n    env:\n      N: \"${len(steps.ping.output.json.items) > 0 && !steps.ping.failed}\"\n      U: \"${upper(automation.name)}-${date}\"\n"
	if res := Validate([]byte(y), Options{}); len(res.Problems) > 0 {
		t.Fatalf("unexpected problems:\n%s", list(res.Problems))
	}
}

func TestShellVariablesInRun(t *testing.T) {
	y := header + "  - id: a\n    type: shell\n    run: 'echo \"${HOME} ${N:-1} ${#ARR[@]} ${date_custom}\"'\n"
	if res := Validate([]byte(y), Options{}); len(res.Problems) > 0 {
		t.Fatalf("shell variables flagged:\n%s", list(res.Problems))
	}
}

func TestWarnings(t *testing.T) {
	noBudget := strings.Replace(header, "budget: { maxCostEur: 1 }\n", "", 1)
	noTZ := strings.Replace(header, ", timezone: Europe/Rome", "", 1)
	llmStep := "  - id: a\n    type: llm\n    promptText: hi\n"

	cases := []struct {
		name, yaml, want string
	}{
		{"model without budget", noBudget + llmStep, "spending cap"},
		{"model in foreach without budget", noBudget + okStep + "  - id: loop\n    type: foreach\n    items: \"${steps.ping.output}\"\n    steps:\n      - { id: b, type: llm, promptText: \"${item}\" }\n", "spending cap"},
		{"schedule without timezone", noTZ + okStep, "runs in UTC"},
		{"webhook without secret", strings.Replace(header, "  - schedule:", "  - webhook: {}\n  - schedule:", 1) + okStep, "without a secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate([]byte(tc.yaml), Options{})
			if res.HasErrors() {
				t.Fatalf("unexpected errors:\n%s", list(res.Problems))
			}
			if _, ok := find(res.Problems, SeverityWarning, tc.want); !ok {
				t.Fatalf("no warning contains %q; got:\n%s", tc.want, list(res.Problems))
			}
			if res.Manifest == nil {
				t.Fatal("a manifest with only warnings must be returned")
			}
		})
	}
}

func TestReferencedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "ok.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := header + "  - id: a\n    type: llm\n    prompt: prompts/ok.md\n  - id: b\n    type: llm\n    prompt: prompts/missing.md\n  - id: c\n    type: llm\n    prompt: prompts\n"
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ValidateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := find(res.Problems, SeverityError, `"prompts/missing.md" not found`); !ok {
		t.Fatalf("missing file not reported:\n%s", list(res.Problems))
	}
	if _, ok := find(res.Problems, SeverityError, "is a folder"); !ok {
		t.Fatalf("folder not reported:\n%s", list(res.Problems))
	}
	if _, ok := find(res.Problems, SeverityError, "ok.md"); ok {
		t.Fatalf("existing file reported as a problem:\n%s", list(res.Problems))
	}

	// Without a base folder, file checks are skipped.
	if res := Validate([]byte(m), Options{}); res.HasErrors() {
		t.Fatalf("file checks ran without a base folder:\n%s", list(res.Problems))
	}
}

func TestSchemaCompiles(t *testing.T) {
	if _, err := Schema(); err != nil {
		t.Fatal(err)
	}
}

func TestAliasesExpand(t *testing.T) {
	y := header + "  - id: a\n    type: http\n    request: &req { url: \"https://example.com\" }\n  - id: b\n    type: http\n    request: *req\n"
	res := Validate([]byte(y), Options{})
	if len(res.Problems) > 0 {
		t.Fatalf("unexpected problems:\n%s", list(res.Problems))
	}
	if res.Manifest.Steps[1].Request.URL != "https://example.com" {
		t.Fatal("alias not expanded")
	}
}

func find(ps []Problem, sev Severity, substr string) (Problem, bool) {
	for _, p := range ps {
		if p.Severity == sev && strings.Contains(p.Message, substr) {
			return p, true
		}
	}
	return Problem{}, false
}

func list(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  ")
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	return b.String()
}
