package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Workspace is what the builder knows about the workspace a draft is for.
type Workspace struct {
	ID string
	// MaxAutonomy caps every automation of the workspace.
	MaxAutonomy string
	// Connections of the workspace, with their actions.
	Connections []Connection
	// Secrets are the names of the workspace's secrets.
	Secrets []string
	// Models are the configured model aliases.
	Models []string
	// Taken holds the names of automations that exist, in any workspace:
	// names are unique across the instance.
	Taken map[string]bool
	// BaseName is the automation the draft edits; empty for a new one.
	BaseName string
}

// Connection is a workspace connection as the builder sees it.
type Connection struct {
	Name    string
	Kind    string
	Status  string
	Actions []connectors.ActionInfo
}

// Checked is a validated draft.
type Checked struct {
	Problems []Problem
	// Manifest is set when the manifest itself has no errors.
	Manifest *manifest.Manifest
	// Hash identifies the files, the same way the automations folder does.
	Hash string
}

// Check validates a draft: file names, the manifest and the files it
// refers to, and what the workspace offers. tmp is a folder for scratch
// files.
func Check(files Files, ws Workspace, tmp string) Checked {
	var res Checked
	res.Problems = CheckPaths(files)
	if HasErrors(res.Problems) {
		return res
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		res.Problems = append(res.Problems, errorf("", "cannot check the draft: %v", err))
		return res
	}
	dir, err := os.MkdirTemp(tmp, "check-")
	if err != nil {
		res.Problems = append(res.Problems, errorf("", "cannot check the draft: %v", err))
		return res
	}
	defer os.RemoveAll(dir)
	if err := Write(dir, files); err != nil {
		res.Problems = append(res.Problems, errorf("", "cannot check the draft: %v", err))
		return res
	}
	res.Hash, _ = registry.HashFolder(dir)
	mres, err := manifest.ValidateFile(filepath.Join(dir, manifest.FileName))
	if err != nil {
		res.Problems = append(res.Problems, errorf(manifest.FileName, "%v", err))
		return res
	}
	for _, p := range mres.Problems {
		res.Problems = append(res.Problems, Problem{Severity: p.Severity, File: manifest.FileName, Path: p.Path, Line: p.Line, Column: p.Column, Message: p.Message})
	}
	res.Problems = append(res.Problems, credentialProblems(files)...)
	if mres.Manifest == nil {
		return res
	}
	res.Manifest = mres.Manifest
	res.Problems = append(res.Problems, workspaceProblems(mres.Manifest, files, ws)...)
	return res
}

// credentialRe matches values that look like real credentials. Manifests
// must refer to secrets with secret://<name> instead.
var credentialRe = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|sk-ant-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|ya29\.[0-9A-Za-z_-]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

func credentialProblems(files Files) []Problem {
	var out []Problem
	for _, p := range files.Paths() {
		if loc := credentialRe.FindStringIndex(files[p]); loc != nil {
			line := strings.Count(files[p][:loc[0]], "\n") + 1
			out = append(out, Problem{Severity: manifest.SeverityError, File: p, Line: line, Column: 1,
				Message: "this looks like a credential; store it on the Secrets page and refer to it as secret://<name>"})
		}
	}
	return out
}

var secretRefRe = regexp.MustCompile(`secret://([A-Za-z0-9][A-Za-z0-9_.-]*)`)

func workspaceProblems(m *manifest.Manifest, files Files, ws Workspace) []Problem {
	const mf = manifest.FileName
	var out []Problem
	add := func(sev manifest.Severity, ptr, format string, args ...any) {
		out = append(out, Problem{Severity: sev, File: mf, Path: ptr, Message: fmt.Sprintf(format, args...)})
	}

	// The workspace and the name.
	wsName := m.Metadata.Workspace
	if wsName == "" {
		wsName = store.DefaultWorkspace
	}
	if wsName != ws.ID {
		add(manifest.SeverityError, "/metadata/workspace", "set metadata.workspace to %q, the workspace this draft belongs to", ws.ID)
	}
	name := m.Metadata.Name
	switch {
	case ws.BaseName != "" && name != ws.BaseName:
		add(manifest.SeverityError, "/metadata/name", "keep metadata.name %q: a draft edits that automation and cannot rename it", ws.BaseName)
	case ws.BaseName == "" && ws.Taken[name]:
		add(manifest.SeverityError, "/metadata/name", "an automation named %q already exists; choose another name, or edit that automation instead", name)
	}

	// Connections and actions.
	byName := map[string]Connection{}
	byKind := map[string][]Connection{}
	for _, c := range ws.Connections {
		byName[c.Name] = c
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}
	builtin := builtinActions()
	reported := map[string]bool{}
	missing := func(ptr, what string) {
		if !reported[what] {
			reported[what] = true
			add(manifest.SeverityWarning, ptr, "%s; add it on the Connections page before activating, or runs will fail", what)
		}
	}
	checkAction := func(ptr, action, conn string) {
		prefix, _, _ := strings.Cut(action, ".")
		if connectors.NeedsNoConnection(action) {
			return
		}
		if kind, ok := connectors.KindForPrefix(prefix); ok {
			if _, known := builtin[action]; !known {
				add(manifest.SeverityError, ptr, "unknown action %q; %s offers: %s", action, prefix, strings.Join(withPrefix(builtin, prefix), ", "))
				return
			}
			if conn != "" {
				c, ok := byName[conn]
				switch {
				case !ok:
					missing(ptr, fmt.Sprintf("there is no connection named %q in this workspace", conn))
				case c.Kind != kind:
					add(manifest.SeverityError, ptr, "connection %q is a %s connection; %s needs a %s one", conn, c.Kind, action, kind)
				}
				return
			}
			if len(byKind[kind]) == 0 {
				missing(ptr, fmt.Sprintf("%s needs a %s connection and this workspace has none", action, kind))
			}
			return
		}
		name := conn
		if name == "" {
			name = prefix
		}
		c, ok := byName[name]
		if !ok {
			missing(ptr, fmt.Sprintf("%s needs an MCP connection named %q and this workspace has none", action, name))
			return
		}
		if c.Kind != connectors.KindMCP {
			add(manifest.SeverityError, ptr, "connection %q is a %s connection, not an MCP server", name, c.Kind)
			return
		}
		if len(c.Actions) == 0 {
			return // tools unknown: the server may be down; the run will tell
		}
		want := name + "." + strings.TrimPrefix(action, prefix+".")
		for _, a := range c.Actions {
			if a.Name == want {
				return
			}
		}
		names := make([]string, 0, len(c.Actions))
		for _, a := range c.Actions {
			names = append(names, a.Name)
		}
		sort.Strings(names)
		add(manifest.SeverityError, ptr, "%s has no tool %q; it offers: %s", name, action, strings.Join(names, ", "))
	}
	models := map[string]bool{}
	for _, mdl := range ws.Models {
		models[mdl] = true
	}
	checkModel := func(ptr, model string) {
		if model == "" {
			model = "smart"
		}
		if !strings.Contains(model, "/") && !models[model] && len(ws.Models) > 0 {
			add(manifest.SeverityWarning, ptr, "model %q is not configured; use one of: %s", model, strings.Join(ws.Models, ", "))
		}
	}
	var walk func(steps []manifest.Step, ptr string)
	walk = func(steps []manifest.Step, ptr string) {
		for i := range steps {
			st := &steps[i]
			sp := fmt.Sprintf("%s/%d", ptr, i)
			switch st.Type {
			case manifest.StepConnector:
				checkAction(sp+"/action", st.Action, st.Connection)
			case manifest.StepAgent:
				for j, t := range st.Tools {
					checkAction(fmt.Sprintf("%s/tools/%d", sp, j), t, "")
				}
				checkModel(sp+"/model", st.Model)
			case manifest.StepLLM:
				checkModel(sp+"/model", st.Model)
			case manifest.StepForeach:
				walk(st.Steps, sp+"/steps")
			}
		}
	}
	walk(m.Steps, "/steps")
	for i, t := range m.Triggers {
		if t.Email == nil {
			continue
		}
		ptr := fmt.Sprintf("/triggers/%d/email/connection", i)
		c, ok := byName[t.Email.Connection]
		switch {
		case !ok:
			missing(ptr, fmt.Sprintf("the email trigger needs a Google connection named %q", t.Email.Connection))
		case c.Kind != connectors.KindGoogle:
			add(manifest.SeverityError, ptr, "connection %q is a %s connection; email triggers need a Google one", c.Name, c.Kind)
		}
	}

	// Secrets.
	have := map[string]bool{}
	for _, s := range ws.Secrets {
		have[s] = true
	}
	seen := map[string]bool{}
	for _, p := range files.Paths() {
		for _, mt := range secretRefRe.FindAllStringSubmatch(files[p], -1) {
			if name := mt[1]; !have[name] && !seen[name] {
				seen[name] = true
				out = append(out, warnf(p, "", "secret %q is not among this workspace's secrets; add it on the Secrets page", name))
			}
		}
	}

	// Autonomy above the workspace cap.
	if ws.MaxAutonomy != "" && autonomyRank[m.EffectiveAutonomy()] > autonomyRank[ws.MaxAutonomy] {
		add(manifest.SeverityWarning, "/autonomy", "the workspace caps autonomy at %q, so this automation runs at %q, not %q",
			ws.MaxAutonomy, ws.MaxAutonomy, m.EffectiveAutonomy())
	}
	return out
}

var autonomyRank = map[string]int{
	manifest.AutonomyObserve: 0, manifest.AutonomyDraft: 1, manifest.AutonomyAsk: 2, manifest.AutonomyAuto: 3,
}

// builtinActions lists the actions of the built-in connectors.
func builtinActions() map[string]connectors.ActionInfo {
	out := map[string]connectors.ActionInfo{}
	for _, k := range connectors.Catalog("") {
		if k.Kind == connectors.KindMCP {
			continue
		}
		for _, a := range k.Actions {
			out[a.Name] = a
		}
	}
	return out
}

func withPrefix(m map[string]connectors.ActionInfo, prefix string) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, prefix+".") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
