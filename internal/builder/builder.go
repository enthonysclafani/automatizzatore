package builder

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/enthonysclafani/automatizzatore/docs"
	"github.com/enthonysclafani/automatizzatore/internal/llm"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/spec"
	"github.com/enthonysclafani/automatizzatore/templates"
)

//go:embed prompt.md
var rules string

// Builder writes automations with a language model.
type Builder struct {
	LLM *llm.Gateway
	// Model is the alias or provider/model that writes automations.
	Model string
	// TempDir holds scratch folders for checking drafts.
	TempDir string
	// Repairs is how many times the model may fix problems Pendolo found in
	// one turn; 0 means 2.
	Repairs int
}

// Turn is one request to the builder.
type Turn struct {
	Workspace Workspace
	// History is the conversation so far, oldest first.
	History []store.DraftMessage
	// Files is the current draft; empty for a new automation.
	Files Files
	// Message is what the person asks now.
	Message string
}

// Result is the builder's answer to a Turn.
type Result struct {
	Reply   string
	Files   Files
	Checked Checked
	// Calls holds every model request, including failed ones.
	Calls   []llm.Call
	CostEur float64
}

// ErrNoModel means no model is configured for the builder.
var ErrNoModel = errors.New("no model is configured for the builder")

// MaxHistory is how many earlier messages the model sees.
const MaxHistory = 20

var answerSchema = map[string]any{
	"type":     "object",
	"required": []any{"reply", "files"},
	"properties": map[string]any{
		"reply": map[string]any{"type": "string", "description": "What you did, for the person, in their language."},
		"files": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":     "object",
				"required": []any{"path", "content"},
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
	},
	"additionalProperties": false,
}

type answer struct {
	Reply string `json:"reply"`
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

// System returns the system prompt: rules, the format, connectors, the
// schema and the templates as examples.
func System() string {
	var b strings.Builder
	b.WriteString(rules)
	b.WriteString("\n# The automation format (docs/manifest.md)\n\n")
	b.WriteString(docs.Manifest)
	b.WriteString("\n# Connections and actions (docs/connectors.md)\n\n")
	b.WriteString(docs.Connectors)
	b.WriteString("\n# JSON Schema of automation.yaml\n\n```json\n")
	b.Write(spec.SchemaV1)
	b.WriteString("\n```\n\n# Examples: the bundled templates\n")
	for _, name := range templates.Names() {
		fmt.Fprintf(&b, "\n## Template %s\n", name)
		writeFiles(&b, templates.Files(name))
	}
	return b.String()
}

func writeFiles(b *strings.Builder, files Files) {
	for _, p := range files.Paths() {
		fence := "```"
		for strings.Contains(files[p], fence) {
			fence += "`"
		}
		fmt.Fprintf(b, "\n%s path=%s\n%s", fence, p, files[p])
		if !strings.HasSuffix(files[p], "\n") {
			b.WriteString("\n")
		}
		b.WriteString(fence + "\n")
	}
}

// workspaceSection describes the workspace to the model.
func workspaceSection(ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# The workspace\n\nmetadata.workspace: %s\n", ws.ID)
	if ws.MaxAutonomy != "" {
		fmt.Fprintf(&b, "Maximum autonomy in this workspace: %s\n", ws.MaxAutonomy)
	}
	if len(ws.Models) > 0 {
		fmt.Fprintf(&b, "Configured models: %s\n", strings.Join(ws.Models, ", "))
	} else {
		b.WriteString("Configured models: none yet (use the alias smart)\n")
	}
	if ws.BaseName != "" {
		fmt.Fprintf(&b, "This draft edits the automation %q: keep its name.\n", ws.BaseName)
	}
	b.WriteString("\n## Connections\n")
	if len(ws.Connections) == 0 {
		b.WriteString("None yet. web.fetch works without one. Say in reply which connections the person must add.\n")
	}
	for _, c := range ws.Connections {
		fmt.Fprintf(&b, "\n- %s (%s, %s)\n", c.Name, c.Kind, c.Status)
		for _, a := range c.Actions {
			fmt.Fprintf(&b, "  - %s [%s]: %s", a.Name, a.Risk, oneLine(a.Description, 200))
			if len(a.Input) > 0 {
				if raw, err := json.Marshal(a.Input); err == nil && len(raw) < 1500 {
					fmt.Fprintf(&b, " Input: %s", raw)
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Secrets\n")
	if len(ws.Secrets) == 0 {
		b.WriteString("None yet.\n")
	} else {
		fmt.Fprintf(&b, "%s\n", strings.Join(ws.Secrets, ", "))
	}
	if len(ws.Taken) > 0 {
		names := make([]string, 0, len(ws.Taken))
		for n := range ws.Taken {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) > 200 {
			names = names[:200]
		}
		fmt.Fprintf(&b, "\n## Names already taken\n%s\n", strings.Join(names, ", "))
	}
	return b.String()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// userPrompt assembles the context and the request.
func userPrompt(t Turn, files Files, problems []Problem, repair bool) string {
	var b strings.Builder
	b.WriteString(workspaceSection(t.Workspace))
	history := t.History
	if len(history) > MaxHistory {
		history = history[len(history)-MaxHistory:]
	}
	if len(history) > 0 {
		b.WriteString("\n# Conversation so far\n")
		for _, m := range history {
			who := "Person"
			switch m.Role {
			case store.RoleAssistant:
				who = "You"
			case store.RoleEvent:
				who = "Pendolo"
			}
			fmt.Fprintf(&b, "\n%s: %s\n", who, m.Text)
		}
	}
	if len(files) > 0 {
		b.WriteString("\n# The draft's current files\n")
		writeFiles(&b, files)
	} else {
		b.WriteString("\n# The draft has no files yet.\n")
	}
	if len(problems) > 0 {
		if repair {
			b.WriteString("\n# Pendolo found problems in your files\n\nFix every error, then return the complete files again. Keep the person's request in mind.\n\n")
		} else {
			b.WriteString("\n# Known problems of the current files\n\n")
		}
		for _, p := range problems {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	fmt.Fprintf(&b, "\n# The person's request\n\n%s\n", t.Message)
	return b.String()
}

// Run answers one turn: the model writes the files, Pendolo checks them,
// and the model fixes the errors it finds, up to Repairs times.
func (bl *Builder) Run(ctx context.Context, t Turn) (Result, error) {
	if bl.LLM == nil || bl.Model == "" {
		return Result{}, ErrNoModel
	}
	repairs := bl.Repairs
	if repairs <= 0 {
		repairs = 2
	}
	system := System()
	files := t.Files
	var problems []Problem
	if len(files) > 0 {
		problems = Check(files, t.Workspace, bl.TempDir).Problems
	}
	var res Result
	for attempt := 0; ; attempt++ {
		r, err := bl.LLM.Complete(ctx, llm.Request{Model: bl.Model, System: system, User: userPrompt(t, files, problems, attempt > 0), Schema: answerSchema})
		res.Calls = append(res.Calls, r.Calls...)
		res.CostEur += r.CostEur
		if err != nil {
			return res, err
		}
		var a answer
		raw, _ := json.Marshal(r.Value)
		if err := json.Unmarshal(raw, &a); err != nil {
			return res, fmt.Errorf("the model's answer is not usable: %w", err)
		}
		res.Reply = strings.TrimSpace(a.Reply)
		if len(a.Files) > 0 {
			files = Files{}
			for _, f := range a.Files {
				files[strings.TrimPrefix(strings.TrimSpace(f.Path), "./")] = f.Content
			}
		}
		res.Files = files
		if len(files) == 0 {
			return res, nil
		}
		res.Checked = Check(files, t.Workspace, bl.TempDir)
		if !HasErrors(res.Checked.Problems) || attempt >= repairs {
			return res, nil
		}
		problems = res.Checked.Problems
		if res.Reply != "" {
			t.History = append(append([]store.DraftMessage{}, t.History...), store.DraftMessage{Role: store.RoleAssistant, Text: res.Reply})
		}
	}
}
