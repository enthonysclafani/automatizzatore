package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/enthonysclafani/automatizzatore/spec"
)

// Severity tells whether a problem blocks the automation.
type Severity string

// Severities.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Problem is one finding about a manifest.
type Problem struct {
	Severity Severity `json:"severity"`
	// Path is a JSON pointer into the manifest; empty means the whole file.
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	var b strings.Builder
	if p.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", p.Line, p.Column)
	}
	b.WriteString(string(p.Severity))
	b.WriteString(": ")
	b.WriteString(p.Message)
	if p.Path != "" {
		fmt.Fprintf(&b, " (at %s)", p.Path)
	}
	return b.String()
}

// Result is the outcome of a validation.
type Result struct {
	// Manifest is set when the manifest has no errors.
	Manifest *Manifest
	Problems []Problem
}

// HasErrors reports whether any problem is an error.
func (r Result) HasErrors() bool {
	for _, p := range r.Problems {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Options tune a validation.
type Options struct {
	// BaseDir is the automation folder. When set, files the manifest refers
	// to (prompts, scripts) must exist inside it.
	BaseDir string
}

// ValidateFile validates the manifest at path, checking referenced files
// relative to its folder.
func ValidateFile(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	return Validate(data, Options{BaseDir: filepath.Dir(path)}), nil
}

// Validate checks a manifest against the schema and the rules the schema
// cannot express.
func Validate(data []byte, opts Options) Result {
	doc, problems := parseYAML(data)
	if doc == nil {
		return Result{Problems: problems}
	}

	if problems = schemaProblems(doc); len(problems) > 0 {
		return Result{Problems: problems}
	}

	v := &semantic{doc: doc, opts: opts}
	v.run()
	sortProblems(v.problems)

	res := Result{Problems: v.problems}
	if !res.HasErrors() {
		var m Manifest
		if err := doc.root.Decode(&m); err != nil {
			res.Problems = append(res.Problems, Problem{Severity: SeverityError, Message: "cannot decode manifest: " + err.Error()})
			return res
		}
		res.Manifest = &m
	}
	return res
}

var (
	schemaOnce sync.Once
	schemaV1   *jsonschema.Schema
	schemaErr  error
	printer    = message.NewPrinter(language.English)
)

// Schema returns the compiled pendolo/v1 schema.
func Schema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(spec.SchemaV1))
		if err != nil {
			schemaErr = fmt.Errorf("parse embedded schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(spec.SchemaV1URL, doc); err != nil {
			schemaErr = fmt.Errorf("load embedded schema: %w", err)
			return
		}
		schemaV1, schemaErr = c.Compile(spec.SchemaV1URL)
	})
	return schemaV1, schemaErr
}

func schemaProblems(doc *document) []Problem {
	sch, err := Schema()
	if err != nil {
		return []Problem{{Severity: SeverityError, Message: err.Error()}}
	}
	err = sch.Validate(doc.value)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []Problem{{Severity: SeverityError, Message: err.Error()}}
	}

	var leaves []*jsonschema.ValidationError
	collectLeaves(verr, &leaves)

	seen := map[string]bool{}
	var out []Problem
	for _, l := range leaves {
		ptr := pointer(l.InstanceLocation)
		msg := l.ErrorKind.LocalizedString(printer)
		key := ptr + "\x00" + msg
		if seen[key] {
			continue
		}
		seen[key] = true
		pos := doc.locate(ptr)
		out = append(out, Problem{Severity: SeverityError, Path: ptr, Line: pos.line, Column: pos.col, Message: msg})
	}
	sortProblems(out)
	return out
}

func collectLeaves(e *jsonschema.ValidationError, out *[]*jsonschema.ValidationError) {
	if len(e.Causes) == 0 {
		*out = append(*out, e)
		return
	}
	for _, c := range e.Causes {
		collectLeaves(c, out)
	}
}

func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(escapePointer(t))
	}
	return b.String()
}

func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Line != ps[j].Line {
			return ps[i].Line < ps[j].Line
		}
		return ps[i].Column < ps[j].Column
	})
}
