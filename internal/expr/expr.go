// Package expr evaluates the ${ ... } expressions in manifests.
//
// A string that is exactly one expression keeps the expression's value
// (a list stays a list). A string with text around expressions becomes a
// string, with each value written as text (JSON for lists and objects).
// The expression language is expr-lang (https://expr-lang.org): field access
// with dots, comparisons, && || !, and a few builtins.
package expr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Env holds the names an expression can use: steps, trigger, item, index,
// date, now, automation.
type Env map[string]any

var (
	exprRe  = regexp.MustCompile(`\$\{([^{}]*)\}`)
	wholeRe = regexp.MustCompile(`^\s*\$\{([^{}]*)\}\s*$`)
	cache   sync.Map // expression body -> *vm.Program
)

// Eval evaluates one expression body (without ${ }).
func Eval(body string, env Env) (any, error) {
	body = strings.TrimSpace(body)
	var prog *vm.Program
	if p, ok := cache.Load(body); ok {
		prog = p.(*vm.Program)
	} else {
		compiled, err := exprlang.Compile(body)
		if err != nil {
			return nil, fmt.Errorf("expression ${%s}: %w", body, err)
		}
		cache.Store(body, compiled)
		prog = compiled
	}
	out, err := exprlang.Run(prog, map[string]any(env))
	if err != nil {
		return nil, fmt.Errorf("expression ${%s}: %s", body, firstLine(err.Error()))
	}
	return out, nil
}

// String renders a string: a lone expression keeps its value's type,
// anything else becomes text.
func String(s string, env Env) (any, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	if m := wholeRe.FindStringSubmatch(s); m != nil {
		return Eval(m[1], env)
	}
	var firstErr error
	out := exprRe.ReplaceAllStringFunc(s, func(match string) string {
		body := match[2 : len(match)-1]
		v, err := Eval(body, env)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return ""
		}
		return Text(v)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// Value renders every string in v, recursively.
func Value(v any, env Env) (any, error) {
	switch t := v.(type) {
	case string:
		return String(t, env)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			r, err := Value(x, env)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			r, err := Value(x, env)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, x := range t {
			r, err := String(x, env)
			if err != nil {
				return nil, err
			}
			out[k] = Text(r)
		}
		return out, nil
	case []string:
		out := make([]string, len(t))
		for i, x := range t {
			r, err := String(x, env)
			if err != nil {
				return nil, err
			}
			out[i] = Text(r)
		}
		return out, nil
	default:
		return v, nil
	}
}

// Condition evaluates an `if` or similar field written as ${ ... }.
func Condition(s string, env Env) (bool, error) {
	v, err := String(s, env)
	if err != nil {
		return false, err
	}
	return Truthy(v), nil
}

// Truthy is false for nil, false, 0, "", "false", and empty lists or maps.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false" && t != "0"
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// Text writes a value as text: strings as they are, nil as "", the rest as JSON.
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
