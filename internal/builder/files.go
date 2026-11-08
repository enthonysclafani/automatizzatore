// Package builder turns a request in plain language into an automation
// folder with a language model, and checks the result before anyone can
// activate it. It never activates anything itself.
package builder

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/enthonysclafani/automatizzatore/internal/manifest"
)

// Files is the content of an automation folder by slash-separated path,
// e.g. "automation.yaml" or "prompts/classify.md".
type Files map[string]string

// Paths returns the file paths, sorted.
func (f Files) Paths() []string {
	out := make([]string, 0, len(f))
	for p := range f {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Limits on what a draft may hold.
const (
	MaxFiles     = 30
	MaxFileBytes = 128 << 10
	MaxTotal     = 512 << 10
)

// filePathRe allows plain relative paths, at most four levels deep, with no
// hidden files and no "..".
var filePathRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*){0,3}$`)

// Problem is a finding about a draft.
type Problem struct {
	Severity manifest.Severity `json:"severity"`
	// File is the path of the file the problem is in; empty for the whole draft.
	File string `json:"file,omitempty"`
	// Path is a JSON pointer into the manifest.
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	var b strings.Builder
	if p.File != "" {
		b.WriteString(p.File)
		if p.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", p.Line, p.Column)
		}
		b.WriteString(": ")
	}
	b.WriteString(string(p.Severity))
	b.WriteString(": ")
	b.WriteString(p.Message)
	if p.Path != "" {
		fmt.Fprintf(&b, " (at %s)", p.Path)
	}
	return b.String()
}

// HasErrors reports whether any problem is an error.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == manifest.SeverityError {
			return true
		}
	}
	return false
}

func errorf(file, format string, args ...any) Problem {
	return Problem{Severity: manifest.SeverityError, File: file, Message: fmt.Sprintf(format, args...)}
}

func warnf(file, ptr, format string, args ...any) Problem {
	return Problem{Severity: manifest.SeverityWarning, File: file, Path: ptr, Message: fmt.Sprintf(format, args...)}
}

// CheckPaths checks the file names and sizes of a draft.
func CheckPaths(files Files) []Problem {
	var out []Problem
	if _, ok := files[manifest.FileName]; !ok {
		out = append(out, errorf("", "the draft has no %s", manifest.FileName))
	}
	if len(files) > MaxFiles {
		out = append(out, errorf("", "the draft has %d files; at most %d are allowed", len(files), MaxFiles))
	}
	total := 0
	dirs := map[string]bool{}
	for _, p := range files.Paths() {
		c := files[p]
		total += len(c)
		switch {
		case !filePathRe.MatchString(p) || path.Clean(p) != p:
			out = append(out, errorf(p, "file paths must be relative, like prompts/summary.md, without hidden files or '..'"))
			continue
		case len(c) > MaxFileBytes:
			out = append(out, errorf(p, "the file is %d KiB; at most %d KiB are allowed", len(c)>>10, MaxFileBytes>>10))
		case !utf8.ValidString(c):
			out = append(out, errorf(p, "the file is not UTF-8 text"))
		}
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for p := range files {
		if dirs[p] {
			out = append(out, errorf(p, "%s is both a file and a folder", p))
		}
	}
	if total > MaxTotal {
		out = append(out, errorf("", "the draft holds %d KiB; at most %d KiB are allowed", total>>10, MaxTotal>>10))
	}
	return out
}

// Write puts files into dir, which must exist. Call CheckPaths first.
func Write(dir string, files Files) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, p := range files.Paths() {
		if !filePathRe.MatchString(p) {
			return fmt.Errorf("invalid file path %q", p)
		}
		if d := path.Dir(p); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(p, "scripts/") || strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		f, err := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, werr := f.WriteString(files[p])
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
		if err := root.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}

// ErrNotEditable means an automation folder holds files the builder cannot
// edit, such as binaries or very large files.
var ErrNotEditable = errors.New("not editable with the builder")

// ReadFolder reads an automation folder into Files. Hidden files and
// folders are left out.
func ReadFolder(dir string) (Files, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := Files{}
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file: %w", p, ErrNotEditable)
		}
		b, err := fs.ReadFile(root.FS(), p)
		if err != nil {
			return err
		}
		if len(b) > MaxFileBytes || !utf8.Valid(b) {
			return fmt.Errorf("%s is binary or larger than %d KiB: %w", p, MaxFileBytes>>10, ErrNotEditable)
		}
		out[filepath.ToSlash(p)] = string(b)
		if len(out) > MaxFiles {
			return fmt.Errorf("more than %d files: %w", MaxFiles, ErrNotEditable)
		}
		return nil
	})
	return out, err
}
