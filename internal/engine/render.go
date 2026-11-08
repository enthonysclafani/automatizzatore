package engine

import (
	"errors"
	"fmt"

	"github.com/enthonysclafani/automatizzatore/internal/expr"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
)

// render evaluates a value from the manifest. A string that is exactly a
// secret:// reference becomes the secret; every other string has its
// ${ } expressions evaluated. A secret is never looked up from the result
// of an expression: data from an email, a web page or a model that happens
// to read "secret://name" stays that text and cannot pull a secret out.
func (ex *execution) render(v any, env expr.Env) (any, error) {
	switch t := v.(type) {
	case string:
		if name, ok := secrets.Name(t); ok {
			if ex.dry {
				return nil, fmt.Errorf("%w: %s", errDrySecret, name)
			}
			return ex.resolver.String(t)
		}
		return expr.String(t, env)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			r, err := ex.render(x, env)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			r, err := ex.render(x, env)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// errDrySecret stops a dry-run step that would use a secret: dry runs are
// for builders, who may not see secret values, so they never resolve one.
var errDrySecret = errors.New("dry runs do not use secrets")

// renderText renders a string and returns it as text.
func (ex *execution) renderText(s string, env expr.Env) (string, error) {
	v, err := ex.render(s, env)
	if err != nil {
		return "", err
	}
	return expr.Text(v), nil
}
