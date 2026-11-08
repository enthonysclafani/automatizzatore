package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones work even on images without a tz database

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Expressions are written as ${ ... }. Phase 0 checks what they refer to;
// the evaluator arrives with the engine.
var (
	exprRe      = regexp.MustCompile(`\$\{([^{}]*)\}`)
	wholeExprRe = regexp.MustCompile(`^\s*\$\{[^{}]*\}\s*$`)
)

// exprRoots are the names an expression may start from.
var exprRoots = map[string]bool{
	"steps": true, "trigger": true, "item": true, "index": true,
	"date": true, "now": true, "automation": true,
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// stepInfo records where a step is declared.
type stepInfo struct {
	ptr     string
	foreach string // id of the enclosing foreach, if any
}

type semantic struct {
	doc      *document
	opts     Options
	problems []Problem
	steps    map[string]stepInfo
}

func (v *semantic) add(sev Severity, ptr, format string, args ...any) {
	pos := v.doc.locate(ptr)
	v.problems = append(v.problems, Problem{
		Severity: sev, Path: ptr, Line: pos.line, Column: pos.col,
		Message: fmt.Sprintf(format, args...),
	})
}

func (v *semantic) run() {
	root, _ := v.doc.value.(map[string]any)
	v.checkTriggers(root)

	steps, _ := root["steps"].([]any)
	v.steps = map[string]stepInfo{}
	v.collectIDs(steps, "/steps", "")
	v.checkSteps(steps, "/steps", map[string]bool{}, "")

	if usesModel(steps) {
		budget, _ := root["budget"].(map[string]any)
		if _, ok := budget["maxCostEur"]; !ok {
			v.add(SeverityWarning, "", "steps call a language model but budget.maxCostEur is not set; a run has no spending cap")
		}
	}
}

func (v *semantic) checkTriggers(root map[string]any) {
	triggers, _ := root["triggers"].([]any)
	for i, t := range triggers {
		tm, _ := t.(map[string]any)
		if hook, ok := tm["webhook"].(map[string]any); ok {
			if _, hasSecret := hook["secret"]; !hasSecret {
				v.add(SeverityWarning, "/triggers/"+strconv.Itoa(i)+"/webhook", "webhook without a secret: anyone who can reach Pendolo can start this automation; set secret: secret://<name>")
			}
			continue
		}
		s, ok := tm["schedule"].(map[string]any)
		if !ok {
			continue
		}
		ptr := "/triggers/" + strconv.Itoa(i) + "/schedule"
		expr, _ := s["cron"].(string)
		if _, err := cronParser.Parse(expr); err != nil {
			v.add(SeverityError, ptr+"/cron", "invalid cron expression %q: %v", expr, err)
		}
		tz, hasTZ := s["timezone"].(string)
		switch {
		case !hasTZ:
			v.add(SeverityWarning, ptr, "no timezone set; the schedule runs in UTC")
		case tz == "Local":
			v.add(SeverityError, ptr+"/timezone", "timezone \"Local\" depends on the host; use an IANA name such as Europe/Rome")
		default:
			if _, err := time.LoadLocation(tz); err != nil {
				v.add(SeverityError, ptr+"/timezone", "unknown timezone %q; use an IANA name such as Europe/Rome", tz)
			}
		}
	}
}

func (v *semantic) collectIDs(steps []any, ptr, foreach string) {
	for i, s := range steps {
		sm, _ := s.(map[string]any)
		sp := ptr + "/" + strconv.Itoa(i)
		id, _ := sm["id"].(string)
		if prev, dup := v.steps[id]; dup {
			v.add(SeverityError, sp+"/id", "step id %q is already used at %s; ids must be unique in the whole automation", id, prev.ptr)
		} else {
			v.steps[id] = stepInfo{ptr: sp, foreach: foreach}
		}
		if sm["type"] == StepForeach {
			nested, _ := sm["steps"].([]any)
			v.collectIDs(nested, sp+"/steps", id)
		}
	}
}

// checkSteps walks one list of steps. visible holds the ids a step may
// refer to: earlier steps in this list and in every enclosing list.
func (v *semantic) checkSteps(steps []any, ptr string, visible map[string]bool, foreach string) {
	local := make(map[string]bool, len(visible)+len(steps))
	for k := range visible {
		local[k] = true
	}
	for i, s := range steps {
		sm, _ := s.(map[string]any)
		sp := ptr + "/" + strconv.Itoa(i)
		id, _ := sm["id"].(string)

		for _, key := range []string{"if", "items"} {
			if e, ok := sm[key].(string); ok && !wholeExprRe.MatchString(e) {
				v.add(SeverityError, sp+"/"+key, "%s must be a single expression such as ${steps.check.output.ok}", key)
			}
		}

		if sm["type"] == StepApproval && foreach != "" {
			v.add(SeverityError, sp+"/type", "approval steps cannot be inside foreach: a run pauses only between top-level steps")
		}
		if run, ok := sm["run"].(string); ok && sm["type"] == StepShell {
			v.checkShellRun(run, sp+"/run")
		}

		for _, str := range stringsIn(sm, sp, true) {
			v.checkExpressions(str.value, str.ptr, id, local, foreach)
		}

		v.checkFile(sm, sp, "prompt")
		v.checkFile(sm, sp, "script")
		v.checkOutputSchema(sm, sp)

		if sm["type"] == StepForeach {
			nested, _ := sm["steps"].([]any)
			v.checkSteps(nested, sp+"/steps", local, id)
		}
		local[id] = true
	}
}

func (v *semantic) checkExpressions(s, ptr, self string, visible map[string]bool, foreach string) {
	matches := exprRe.FindAllStringSubmatch(s, -1)
	if strings.Count(s, "${") > len(matches) {
		v.add(SeverityError, ptr, "unclosed or nested ${ ... } expression")
	}
	for _, m := range matches {
		body := strings.TrimSpace(m[1])
		if body == "" {
			v.add(SeverityError, ptr, "empty expression ${}")
			continue
		}
		names, refs, bareSteps, err := analyzeExpr(body)
		if err != nil {
			v.add(SeverityError, ptr, "invalid expression ${%s}: %s", body, firstLine(err.Error()))
			continue
		}
		for _, n := range names {
			if !exprRoots[n] {
				v.add(SeverityError, ptr, "expression ${%s} uses unknown name %q; names start from one of: %s", body, n, rootList())
			}
			if (n == "item" || n == "index") && foreach == "" {
				v.add(SeverityError, ptr, "${%s} uses %q, which exists only inside a foreach step", body, n)
			}
		}
		if bareSteps {
			v.add(SeverityError, ptr, "expression ${%s} must name a step, as in steps.<id>.output", body)
		}
		for _, ref := range refs {
			v.checkStepRef(ref, ptr, self, visible, foreach)
		}
	}
}

// shellVarRe matches the name at the start of a ${...} in shell code.
var shellVarRe = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)(.?)`)

// checkShellRun rejects Pendolo expressions in shell code: values pasted
// into a script can break it or inject commands. Shell variables such as
// ${HOME} or ${N:-1} are left alone.
func (v *semantic) checkShellRun(run, ptr string) {
	for _, m := range exprRe.FindAllStringSubmatch(run, -1) {
		sm := shellVarRe.FindStringSubmatch(m[1])
		if sm == nil || !exprRoots[sm[1]] || sm[2] == ":" || sm[2] == "-" || sm[2] == "#" || sm[2] == "%" {
			continue
		}
		v.add(SeverityError, ptr, "${%s} is not evaluated in run, which is shell code; pass the value through env (e.g. env: { VALUE: \"${%s}\" }) and use $VALUE in the script",
			strings.TrimSpace(m[1]), strings.TrimSpace(m[1]))
	}
}

// analyzeExpr parses an expression and returns the variable names it
// uses, the step ids it reads through steps.<id>, and whether it uses
// "steps" without naming a step.
func analyzeExpr(body string) (names, stepRefs []string, bareSteps bool, err error) {
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, nil, false, err
	}
	c := &exprCollector{stepBases: map[*ast.IdentifierNode]bool{}, seen: map[string]bool{}}
	ast.Walk(&tree.Node, c)
	for _, id := range c.idents {
		if !c.seen[id.Value] {
			c.seen[id.Value] = true
			names = append(names, id.Value)
		}
		if id.Value == "steps" && !c.stepBases[id] {
			bareSteps = true
		}
	}
	return names, c.refs, bareSteps, nil
}

type exprCollector struct {
	idents    []*ast.IdentifierNode
	stepBases map[*ast.IdentifierNode]bool
	refs      []string
	seen      map[string]bool
}

func (c *exprCollector) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		c.idents = append(c.idents, n)
	case *ast.MemberNode:
		if base, ok := n.Node.(*ast.IdentifierNode); ok && base.Value == "steps" {
			if prop, ok := n.Property.(*ast.StringNode); ok {
				c.stepBases[base] = true
				c.refs = append(c.refs, prop.Value)
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (v *semantic) checkStepRef(id, ptr, self string, visible map[string]bool, foreach string) {
	switch info, known := v.steps[id]; {
	case visible[id]:
		return
	case id == self:
		v.add(SeverityError, ptr, "step %q refers to its own output", id)
	case !known:
		v.add(SeverityError, ptr, "unknown step %q", id)
	case info.foreach != "" && info.foreach != foreach:
		v.add(SeverityError, ptr, "step %q is inside foreach %q; refer to %q instead", id, info.foreach, info.foreach)
	default:
		v.add(SeverityError, ptr, "step %q runs later; a step can only use the output of steps before it", id)
	}
}

func (v *semantic) checkFile(sm map[string]any, sp, key string) {
	rel, ok := sm[key].(string)
	if !ok {
		return
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			v.add(SeverityError, sp+"/"+key, "path %q leaves the automation folder; '..' is not allowed", rel)
			return
		}
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		v.add(SeverityError, sp+"/"+key, "path %q must be relative to the automation folder", rel)
		return
	}
	if v.opts.BaseDir == "" {
		return
	}
	path := filepath.Join(v.opts.BaseDir, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	switch {
	case err != nil:
		v.add(SeverityError, sp+"/"+key, "file %q not found in the automation folder", rel)
	case info.IsDir():
		v.add(SeverityError, sp+"/"+key, "%q is a folder, not a file", rel)
	}
}

func (v *semantic) checkOutputSchema(sm map[string]any, sp string) {
	schema, ok := sm["outputSchema"].(map[string]any)
	if !ok {
		return
	}
	const url = "mem://output-schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, schema); err != nil {
		v.add(SeverityError, sp+"/outputSchema", "outputSchema is not a valid JSON Schema: %v", err)
		return
	}
	if _, err := c.Compile(url); err != nil {
		v.add(SeverityError, sp+"/outputSchema", "outputSchema is not a valid JSON Schema: %v", err)
	}
}

type located struct{ ptr, value string }

// stringsIn returns every string value under v. At the top of a step it
// skips the nested steps of a foreach, which are checked on their own.
func stringsIn(v any, ptr string, stepTop bool) []located {
	var out []located
	switch t := v.(type) {
	case string:
		out = append(out, located{ptr, t})
	case []any:
		for i, x := range t {
			out = append(out, stringsIn(x, ptr+"/"+strconv.Itoa(i), false)...)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			// run is shell code: ${VAR} there is the shell's, see checkShellRun.
			if stepTop && (k == "steps" || k == "outputSchema" || k == "run") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, stringsIn(t[k], ptr+"/"+escapePointer(k), false)...)
		}
	}
	return out
}

func usesModel(steps []any) bool {
	for _, s := range steps {
		sm, _ := s.(map[string]any)
		switch sm["type"] {
		case StepLLM, StepAgent:
			return true
		case StepForeach:
			if nested, _ := sm["steps"].([]any); usesModel(nested) {
				return true
			}
		}
	}
	return false
}

func rootList() string {
	names := make([]string, 0, len(exprRoots))
	for k := range exprRoots {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
