// Command pendolo runs and manages automations.
//
// Phase 0 ships the validator: `pendolo validate` checks automation
// manifests against the pendolo/v1 schema and its rules.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/version"
	"github.com/enthonysclafani/automatizzatore/spec"
)

// Exit codes.
const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

const usage = `pendolo: automations that keep running.

Usage:
  pendolo validate [--format text|json] [--strict] <path>...
      Check automation manifests. A path is a manifest file or a folder,
      searched for files named automation.yaml.
  pendolo schema
      Print the JSON Schema of the manifest format (pendolo/v1).
  pendolo version
      Print version information.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "schema":
		_, _ = stdout.Write(spec.SchemaV1)
		return exitOK
	case "version", "--version":
		fmt.Fprintf(stdout, "pendolo %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

type fileReport struct {
	File     string             `json:"file"`
	Valid    bool               `json:"valid"`
	Problems []manifest.Problem `json:"problems"`
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("validate", flag.ContinueOnError)
	fl.SetOutput(stderr)
	format := fl.String("format", "text", "output format: text or json")
	strict := fl.Bool("strict", false, "treat warnings as errors")
	if err := fl.Parse(args); err != nil {
		return exitUsage
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "unknown format %q: use text or json\n", *format)
		return exitUsage
	}
	if fl.NArg() == 0 {
		fmt.Fprint(stderr, "validate needs at least one file or folder\n\n"+usage)
		return exitUsage
	}

	files, err := manifestFiles(fl.Args())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}

	reports := make([]fileReport, 0, len(files))
	failed := false
	for _, f := range files {
		res, err := manifest.ValidateFile(f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		ok := !res.HasErrors() && (!*strict || len(res.Problems) == 0)
		failed = failed || !ok
		probs := res.Problems
		if probs == nil {
			probs = []manifest.Problem{}
		}
		reports = append(reports, fileReport{File: f, Valid: ok, Problems: probs})
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(reports)
	} else {
		printText(stdout, reports)
	}
	if failed {
		return exitInvalid
	}
	return exitOK
}

func printText(w io.Writer, reports []fileReport) {
	valid := 0
	for _, r := range reports {
		status := "ok"
		if !r.Valid {
			status = "invalid"
		} else {
			valid++
		}
		fmt.Fprintf(w, "%s: %s\n", r.File, status)
		for _, p := range r.Problems {
			fmt.Fprintf(w, "  %s:%s\n", r.File, prefixed(p))
		}
	}
	fmt.Fprintf(w, "%d of %d automations valid\n", valid, len(reports))
}

func prefixed(p manifest.Problem) string {
	s := p.String()
	if p.Line == 0 {
		return " " + s
	}
	return s
}

// manifestFiles expands folders into the automation.yaml files they hold.
func manifestFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		var found []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == manifest.FileName {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, errors.New("no " + manifest.FileName + " found in " + p)
		}
		sort.Strings(found)
		out = append(out, found...)
	}
	return out, nil
}
