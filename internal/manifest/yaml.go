package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// position is a 1-based line and column in the source file.
type position struct{ line, col int }

// document is a YAML manifest converted to JSON-compatible values, with the
// source position of every value, keyed by JSON pointer.
type document struct {
	root  *yaml.Node
	value any
	pos   map[string]position
}

// maxAliasDepth stops alias expansion from looping or exploding.
const maxAliasDepth = 64

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

// parseYAML parses exactly one YAML document.
func parseYAML(data []byte) (*document, []Problem) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Severity: SeverityError, Message: "the file is empty"}}
		}
		return nil, []Problem{yamlProblem(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, []Problem{{
			Severity: SeverityError, Line: extra.Line, Column: extra.Column,
			Message: "the file holds more than one YAML document; put one automation per file",
		}}
	} else if !errors.Is(err, io.EOF) {
		return nil, []Problem{yamlProblem(err)}
	}

	c := converter{pos: map[string]position{}}
	v := c.convert(&root, "", 0)
	if len(c.problems) > 0 {
		return nil, c.problems
	}
	return &document{root: &root, value: v, pos: c.pos}, nil
}

func yamlProblem(err error) Problem {
	p := Problem{Severity: SeverityError, Message: "invalid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
	if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
		p.Line, _ = strconv.Atoi(m[1])
	}
	return p
}

type converter struct {
	pos      map[string]position
	problems []Problem
}

func (c *converter) fail(n *yaml.Node, ptr, format string, args ...any) {
	c.problems = append(c.problems, Problem{
		Severity: SeverityError, Path: ptr, Line: n.Line, Column: n.Column,
		Message: fmt.Sprintf(format, args...),
	})
}

// convert turns a YAML node into the values encoding/json would produce,
// so the JSON Schema sees exactly what a JSON manifest would contain.
func (c *converter) convert(n *yaml.Node, ptr string, depth int) any {
	if depth > maxAliasDepth {
		c.fail(n, ptr, "the document nests too deeply or has an alias loop")
		return nil
	}
	if _, seen := c.pos[ptr]; !seen {
		c.pos[ptr] = position{n.Line, n.Column}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return c.convert(n.Content[0], ptr, depth+1)
	case yaml.AliasNode:
		return c.convert(n.Alias, ptr, depth+1)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, item := range n.Content {
			out = append(out, c.convert(item, ptr+"/"+strconv.Itoa(i), depth+1))
		}
		return out
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" {
				c.fail(k, ptr, "YAML merge keys (<<) are not supported; write the keys out")
				continue
			}
			if k.Kind != yaml.ScalarNode || (k.Tag != "!!str" && k.Tag != "!!int" && k.Tag != "!!bool") {
				c.fail(k, ptr, "mapping keys must be plain strings")
				continue
			}
			child := ptr + "/" + escapePointer(k.Value)
			if _, dup := out[k.Value]; dup {
				c.fail(k, child, "key %q appears twice", k.Value)
				continue
			}
			c.pos[child] = position{v.Line, v.Column}
			out[k.Value] = c.convert(v, child, depth+1)
		}
		return out
	case yaml.ScalarNode:
		return c.scalar(n, ptr)
	}
	c.fail(n, ptr, "unsupported YAML node")
	return nil
}

func (c *converter) scalar(n *yaml.Node, ptr string) any {
	switch n.Tag {
	case "!!null":
		return nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			c.fail(n, ptr, "invalid boolean %q", n.Value)
		}
		return b
	case "!!int":
		var i int64
		if err := n.Decode(&i); err == nil {
			return i
		}
		var f float64
		if err := n.Decode(&f); err != nil {
			c.fail(n, ptr, "invalid integer %q", n.Value)
		}
		return f
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			c.fail(n, ptr, "invalid or non-finite number %q", n.Value)
		}
		return f
	default:
		// !!str, and !!timestamp or !!binary, which stay the text as written.
		return n.Value
	}
}

// escapePointer escapes a key for use in a JSON pointer (RFC 6901).
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// locate returns the position of ptr, or of its nearest ancestor.
func (d *document) locate(ptr string) position {
	for {
		if p, ok := d.pos[ptr]; ok {
			return p
		}
		i := strings.LastIndex(ptr, "/")
		if i < 0 {
			return position{}
		}
		ptr = ptr[:i]
	}
}
