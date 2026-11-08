// Package registry loads the automations folder: one sub-folder per
// automation, each with an automation.yaml.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

// Invalid is an automation folder that failed validation.
type Invalid struct {
	Name string
	// Workspace is read leniently from the broken manifest, "default" if unreadable.
	Workspace string
	Path      string
	Hash      string
	Problems  []manifest.Problem
}

// Snapshot is the content of the automations folder at one moment.
type Snapshot struct {
	Valid   map[string]engine.Automation
	Invalid []Invalid
}

// Names returns the names of every automation, valid or not.
func (s Snapshot) Names() []string {
	names := make([]string, 0, len(s.Valid)+len(s.Invalid))
	for n := range s.Valid {
		names = append(names, n)
	}
	for _, inv := range s.Invalid {
		names = append(names, inv.Name)
	}
	sort.Strings(names)
	return names
}

// Load reads every <dir>/<folder>/automation.yaml. A valid automation is
// named by metadata.name; an invalid one by its folder.
func Load(dir string) (Snapshot, error) {
	snap := Snapshot{Valid: map[string]engine.Automation{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return snap, err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		folder := filepath.Join(dir, e.Name())
		path := filepath.Join(folder, manifest.FileName)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		a, inv := LoadFolder(folder)
		if inv != nil {
			snap.Invalid = append(snap.Invalid, *inv)
			continue
		}
		if prev, dup := snap.Valid[a.Name]; dup {
			snap.Invalid = append(snap.Invalid, Invalid{
				Name: e.Name(), Path: folder, Hash: a.Hash,
				Problems: []manifest.Problem{{Severity: manifest.SeverityError, Path: "/metadata/name",
					Message: fmt.Sprintf("metadata.name %q is already used by %s", a.Name, prev.Dir)}},
			})
			continue
		}
		snap.Valid[a.Name] = a
	}
	sort.Slice(snap.Invalid, func(i, j int) bool { return snap.Invalid[i].Name < snap.Invalid[j].Name })
	return snap, nil
}

// LoadFolder loads one automation folder.
func LoadFolder(folder string) (engine.Automation, *Invalid) {
	path := filepath.Join(folder, manifest.FileName)
	hash, herr := HashFolder(folder)
	res, err := manifest.ValidateFile(path)
	if err == nil && herr != nil {
		err = herr
	}
	if err != nil {
		return engine.Automation{}, &Invalid{Name: filepath.Base(folder), Path: folder,
			Problems: []manifest.Problem{{Severity: manifest.SeverityError, Message: err.Error()}}}
	}
	if res.HasErrors() {
		return engine.Automation{}, &Invalid{Name: filepath.Base(folder), Workspace: guessWorkspace(path), Path: folder, Hash: hash, Problems: res.Problems}
	}
	abs, err := filepath.Abs(folder)
	if err != nil {
		abs = folder
	}
	return engine.Automation{Name: res.Manifest.Metadata.Name, Dir: abs, Hash: hash, Manifest: res.Manifest}, nil
}

// guessWorkspace reads metadata.workspace from a manifest that may be
// invalid, so its problems show up in the right workspace.
func guessWorkspace(path string) string {
	var m struct {
		Metadata struct {
			Workspace string `yaml:"workspace"`
		} `yaml:"metadata"`
	}
	b, err := os.ReadFile(path)
	if err == nil && yaml.Unmarshal(b, &m) == nil && m.Metadata.Workspace != "" {
		return m.Metadata.Workspace
	}
	return store.DefaultWorkspace
}

// HashFolder fingerprints every file of an automation, so an edited prompt
// or script counts as a new version.
func HashFolder(folder string) (string, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return "", err
	}
	defer root.Close()
	h := sha256.New()
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != "." && d.Name()[0] == '.' {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil // hidden files are not part of the automation
		}
		if d.IsDir() {
			return nil
		}
		_, _ = io.WriteString(h, path+"\x00")
		f, err := root.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(h, f)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}
