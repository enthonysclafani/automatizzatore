// Package secrets resolves secret:// references.
//
// A manifest never holds a secret value. It refers to one as
// secret://<name>, and the value is read at run time from a backend. Phase 1
// ships one backend that reads files (the Docker secrets layout) and
// environment variables; encrypted storage and 1Password come later behind
// the same interface.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Scheme prefixes every secret reference.
const Scheme = "secret://"

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// ErrNotFound is returned when no backend holds the secret.
var ErrNotFound = errors.New("secret not found")

// Provider returns the value of a secret by name.
type Provider interface {
	Get(name string) (string, error)
}

// Name returns the secret name if s is exactly a secret reference.
func Name(s string) (string, bool) {
	if !strings.HasPrefix(s, Scheme) {
		return "", false
	}
	name := strings.TrimPrefix(s, Scheme)
	return name, nameRe.MatchString(name)
}

// FilesAndEnv reads a secret from Dir/<name>, then from the environment
// variable EnvPrefix + NAME (uppercase, with - and . turned into _).
type FilesAndEnv struct {
	Dir       string
	EnvPrefix string
	// LookupEnv defaults to os.LookupEnv; tests replace it.
	LookupEnv func(string) (string, bool)
}

// Get implements Provider.
func (p FilesAndEnv) Get(name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid secret name %q", name)
	}
	if p.Dir != "" {
		data, err := os.ReadFile(filepath.Join(p.Dir, name))
		if err == nil {
			return strings.TrimRight(string(data), "\r\n"), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read secret %q: %w", name, err)
		}
	}
	lookup := p.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	env := p.EnvName(name)
	if v, ok := lookup(env); ok {
		return v, nil
	}
	where := "environment variable " + env
	if p.Dir != "" {
		where = "file " + filepath.Join(p.Dir, name) + " or " + where
	}
	return "", fmt.Errorf("%w: %q (looked for %s)", ErrNotFound, name, where)
}

// EnvName is the environment variable that holds a secret.
func (p FilesAndEnv) EnvName(name string) string {
	r := strings.NewReplacer("-", "_", ".", "_")
	return p.EnvPrefix + strings.ToUpper(r.Replace(name))
}

// Static is a Provider backed by a map, for tests and the CLI.
type Static map[string]string

// Get implements Provider.
func (s Static) Get(name string) (string, error) {
	if v, ok := s[name]; ok {
		return v, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Resolver resolves references for one run and remembers every value it
// revealed, so the run's records can be redacted.
type Resolver struct {
	provider Provider
	mu       sync.Mutex
	revealed map[string]string // value -> name
}

// NewResolver returns a Resolver over p.
func NewResolver(p Provider) *Resolver {
	return &Resolver{provider: p, revealed: map[string]string{}}
}

// String resolves s if it is exactly a secret reference; other strings are
// returned unchanged.
func (r *Resolver) String(s string) (string, error) {
	name, ok := Name(s)
	if !ok {
		if strings.HasPrefix(s, Scheme) {
			return "", fmt.Errorf("invalid secret reference %q", s)
		}
		return s, nil
	}
	v, err := r.provider.Get(name)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if len(v) >= 4 {
		r.revealed[v] = name
	}
	r.mu.Unlock()
	return v, nil
}

// Value resolves every string in v (maps and slices included).
func (r *Resolver) Value(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return r.String(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			rv, err := r.Value(x)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			rv, err := r.Value(x)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

// Redact replaces every revealed secret value in s with [secret:<name>].
func (r *Resolver) Redact(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.revealed) == 0 || s == "" {
		return s
	}
	// Longest values first, so a secret that contains another is masked whole.
	values := make([]string, 0, len(r.revealed))
	for v := range r.revealed {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		s = strings.ReplaceAll(s, v, "[secret:"+r.revealed[v]+"]")
	}
	return s
}
