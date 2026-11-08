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

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Expressions are written as ${ ... }. Phase 0 checks what they refer to;
// the evaluator arrives with the engine.
var (
	exprRe      = regexp.MustCompile(`\$\{([^{}]*)\}`)
	wholeExprRe = regexp.MustCompile(`^\s*\$\{[^{}]*\}\s*$`)
	stepRefRe   = regexp.MustCompile(`(?:^|[^\w.])steps\.([a-zA-Z_][a-zA-Z0-9_]*)`)
	bareStepsRe = regexp.MustCompile(`(?:^|[^\w.])steps(?:$|[^\w.])`)
	loopVarRe   = regexp.MustCompile(`(?:^|[^\w.])(item|index)\b`)
	rootRe      = regexp.MustCompile(`^\s*!?\s*\(?\s*([a-zA-Z_][a-zA-Z0-9_]*)`)
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
		if r := rootRe.FindStringSubmatch(body); r == nil || !exprRoots[r[1]] {
			v.add(SeverityError, ptr, "expression ${%s} must start with one of: %s", body, rootList())
			continue
		}
		if bareStepsRe.MatchString(body) && !stepRefRe.MatchString(body) {
			v.add(SeverityError, ptr, "expression ${%s} must name a step, as in steps.<id>.output", body)
		}
		for _, ref := range stepRefRe.FindAllStringSubmatch(body, -1) {
			v.checkStepRef(ref[1], ptr, self, visible, foreach)
		}
		if lv := loopVarRe.FindStringSubmatch(body); lv != nil && foreach == "" {
			v.add(SeverityError, ptr, "${%s} uses %q, which exists only inside a foreach step", body, lv[1])
		}
	}
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
			if stepTop && (k == "steps" || k == "outputSchema") {
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
