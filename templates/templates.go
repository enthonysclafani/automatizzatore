// Package templates embeds the ready-made automations, so the dashboard
// can start a draft from one and the AI builder can learn from them.
package templates

import (
	"embed"
	"io/fs"
	"path"
	"sort"
)

//go:embed */automation.yaml */prompts/* */scripts/*
var files embed.FS

// FS holds one folder per template.
var FS fs.FS = files

// Names lists the templates.
func Names() []string {
	entries, _ := fs.ReadDir(files, ".")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Files returns the files of one template by path inside its folder, or
// nil if there is no such template.
func Files(name string) map[string]string {
	sub, err := fs.Sub(files, name)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err == nil {
			out[path.Clean(p)] = string(b)
		}
		return err
	})
	if len(out) == 0 {
		return nil
	}
	return out
}
