package fold

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"go.starlark.net/starlark"
)

func runRuleDefinition(active effectiveDefinition, rel, file string, r *rule.Rule, depPatterns *depPatternCache, reportViolation func(policyViolation)) error {
	params, err := active.normalizedParams()
	if err != nil {
		return err
	}
	_, err = starlark.Call(
		&starlark.Thread{Name: "gazelle_fold rule " + active.Activation.Name},
		active.Definition.Apply,
		starlark.Tuple{
			&ruleContextValue{
				name:            active.Activation.Name,
				params:          params,
				file:            file,
				ruleKind:        r.Kind(),
				ruleName:        r.Name(),
				allowViolation:  active.Definition.Kind == kindRulePolicy,
				reportViolation: reportViolation,
			},
			&ruleValue{
				file:        file,
				pkg:         rel,
				rule:        r,
				depPatterns: depPatterns,
			},
		},
		nil,
	)
	return err
}

func runFold(active effectiveDefinition, ctx *foldContextValue) error {
	returned, err := starlark.Call(
		&starlark.Thread{Name: "gazelle_fold fold " + active.Activation.Name},
		active.Definition.Apply,
		starlark.Tuple{ctx},
		nil,
	)
	if err != nil {
		return err
	}
	return ctx.applyFoldResult(returned)
}

type ruleContextValue struct {
	name            string
	params          *starlark.Dict
	file            string
	ruleKind        string
	ruleName        string
	allowViolation  bool
	reportViolation func(policyViolation)
}

func (*ruleContextValue) String() string       { return "rule_context" }
func (*ruleContextValue) Type() string         { return "rule_context" }
func (*ruleContextValue) Freeze()              {}
func (*ruleContextValue) Truth() starlark.Bool { return starlark.True }
func (*ruleContextValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("rule context is unhashable")
}

func (ctx *ruleContextValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "params":
		return ctx.params, nil
	case "report_violation":
		if !ctx.allowViolation {
			return nil, nil
		}
		return ruleContextReportViolation.BindReceiver(ctx), nil
	default:
		return nil, nil
	}
}

func (ctx *ruleContextValue) AttrNames() []string {
	if !ctx.allowViolation {
		return []string{"params"}
	}
	return []string{"params", "report_violation"}
}

type ruleValue struct {
	file        string
	pkg         string
	rule        *rule.Rule
	depPatterns *depPatternCache
}

func (*ruleValue) String() string        { return "rule" }
func (*ruleValue) Type() string          { return "rule" }
func (*ruleValue) Freeze()               {}
func (*ruleValue) Truth() starlark.Bool  { return starlark.True }
func (*ruleValue) Hash() (uint32, error) { return 0, fmt.Errorf("rule is unhashable") }

func (r *ruleValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "name":
		return starlark.String(r.rule.Name()), nil
	case "matches_kind":
		return ruleMatchesKind.BindReceiver(r), nil
	case "ensure_list_attr_contains":
		return ruleEnsureListAttrContains.BindReceiver(r), nil
	case "deps":
		return &depsValue{
			pkg:         r.pkg,
			rule:        r.rule,
			depPatterns: r.depPatterns,
		}, nil
	default:
		return nil, nil
	}
}

func (*ruleValue) AttrNames() []string {
	return []string{"name", "matches_kind", "ensure_list_attr_contains", "deps"}
}

var ruleContextReportViolation = starlark.NewBuiltin("report_violation", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*ruleContextValue)
	var message string
	if err := starlark.UnpackArgs("report_violation", args, kwargs, "message", &message); err != nil {
		return nil, err
	}
	if message == "" {
		return nil, fmt.Errorf("report_violation message must not be empty")
	}
	if self.reportViolation != nil {
		self.reportViolation(policyViolation{
			File:       self.file,
			PolicyName: self.name,
			RuleKind:   self.ruleKind,
			RuleName:   self.ruleName,
			Message:    message,
		})
	}
	return starlark.None, nil
})

var ruleMatchesKind = starlark.NewBuiltin("matches_kind", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*ruleValue)
	var patterns starlark.Value
	if err := starlark.UnpackArgs("matches_kind", args, kwargs, "patterns", &patterns); err != nil {
		return nil, err
	}
	values, err := readStringSequence("matches_kind.patterns", patterns)
	if err != nil {
		return nil, err
	}
	return starlark.Bool(matchesAnyKind(values, self.rule.Kind())), nil
})

var ruleEnsureListAttrContains = starlark.NewBuiltin("ensure_list_attr_contains", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*ruleValue)
	var (
		name   string
		values starlark.Value
	)
	if err := starlark.UnpackArgs("ensure_list_attr_contains", args, kwargs,
		"name", &name,
		"values", &values,
	); err != nil {
		return nil, err
	}
	required, err := readStringSequence("ensure_list_attr_contains.values", values)
	if err != nil {
		return nil, err
	}
	ensureListAttrContains(self.file, self.rule, name, required)
	return starlark.None, nil
})

type foldContextValue struct {
	lang        *foldLang
	args        language.GenerateArgs
	active      activation
	params      *starlark.Dict
	depPatterns *depPatternCache
	gen         []*rule.Rule
	empty       []*rule.Rule
	exports     map[string]string
	complete    bool
}

func newFoldContextValue(lang *foldLang, args language.GenerateArgs, active effectiveDefinition) (*foldContextValue, error) {
	params, err := active.normalizedParams()
	if err != nil {
		return nil, err
	}
	return &foldContextValue{
		lang:        lang,
		args:        args,
		active:      active.Activation,
		params:      params,
		depPatterns: newDepPatternCache(),
		exports:     make(map[string]string),
		complete:    true,
	}, nil
}

func (*foldContextValue) String() string       { return "fold_context" }
func (*foldContextValue) Type() string         { return "fold_context" }
func (*foldContextValue) Freeze()              {}
func (*foldContextValue) Truth() starlark.Bool { return starlark.True }
func (*foldContextValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("fold context is unhashable")
}

func (ctx *foldContextValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "params":
		return ctx.params, nil
	case "matching_files":
		return packageMatchingFiles.BindReceiver(ctx), nil
	case "rules_matching":
		return packageRulesMatching.BindReceiver(ctx), nil
	case "child_exports":
		return packageChildExports.BindReceiver(ctx), nil
	default:
		return nil, nil
	}
}

func (*foldContextValue) AttrNames() []string {
	return []string{
		"params",
		"matching_files",
		"rules_matching",
		"child_exports",
	}
}

var packageMatchingFiles = starlark.NewBuiltin("matching_files", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*foldContextValue)
	var include starlark.Value
	if err := starlark.UnpackArgs("matching_files", args, kwargs, "include", &include); err != nil {
		return nil, err
	}
	patterns, err := readStringSequence("matching_files.include", include)
	if err != nil {
		return nil, err
	}
	return stringList(matchingFiles(self.args.RegularFiles, patterns)), nil
})

var packageRulesMatching = starlark.NewBuiltin("rules_matching", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*foldContextValue)
	var kinds starlark.Value
	if err := starlark.UnpackArgs("rules_matching", args, kwargs, "kinds", &kinds); err != nil {
		return nil, err
	}
	patterns, err := readStringSequence("rules_matching.kinds", kinds)
	if err != nil {
		return nil, err
	}
	if self.args.File == nil {
		return starlark.NewList(nil), nil
	}
	rules := make([]starlark.Value, 0, len(self.args.File.Rules))
	for _, r := range self.args.File.Rules {
		if !matchesAnyKind(patterns, r.Kind()) {
			continue
		}
		rules = append(rules, &ruleValue{
			file:        self.args.File.Path,
			pkg:         self.args.Rel,
			rule:        r,
			depPatterns: self.depPatterns,
		})
	}
	return starlark.NewList(rules), nil
})

func (ctx *foldContextValue) applyFoldResult(value starlark.Value) error {
	if value == nil || value == starlark.None {
		return nil
	}
	var iterable starlark.Iterable
	switch typed := value.(type) {
	case *starlark.List:
		iterable = typed
	case starlark.Tuple:
		iterable = typed
	default:
		return fmt.Errorf("fold %q must return None or a list or tuple of fold output values", ctx.active.Name)
	}
	iter := iterable.Iterate()
	defer iter.Done()

	seenRules := make(map[string]struct{})
	seenExports := make(map[string]struct{})
	var item starlark.Value
	for iter.Next(&item) {
		switch typed := item.(type) {
		case *managedRuleSpecValue:
			if err := recordManagedRuleOutput(ctx.active.Name, seenRules, typed.spec.Name); err != nil {
				return err
			}
			if err := ctx.applyManagedRule(typed.spec); err != nil {
				return err
			}
		case *exportSpecValue:
			if _, exists := seenExports[typed.spec.Name]; exists {
				return fmt.Errorf("fold %q returned duplicate export %q", ctx.active.Name, typed.spec.Name)
			}
			seenExports[typed.spec.Name] = struct{}{}
			if err := ctx.applyExport(typed.spec); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"fold %q returned %s, want gazelle_fold.rule(...) or gazelle_fold.export(...)",
				ctx.active.Name,
				item.Type(),
			)
		}
	}
	return nil
}

func recordManagedRuleOutput(foldName string, seen map[string]struct{}, name string) error {
	if _, exists := seen[name]; exists {
		return fmt.Errorf("fold %q returned duplicate managed rule name %q", foldName, name)
	}
	seen[name] = struct{}{}
	return nil
}

func (ctx *foldContextValue) applyManagedRule(spec managedRuleSpec) error {
	if spec.Kind == "filegroup" {
		// filegroups are native Gazelle-owned rules. Route them through the
		// generation path so Gazelle can merge and delete them with the same
		// semantics it already applies to its built-in kinds, while folds still
		// see one uniform declarative rule output.
		ctx.applyGeneratedRule(spec)
		return nil
	}
	if !spec.Present {
		removeManagedRule(ctx.args.File, spec.Kind, spec.Name)
		return nil
	}
	managed, err := ensureManagedRule(ctx.args.File, spec.Kind, spec.Name)
	if err != nil {
		return err
	}
	setSortedAttrs(managed, spec.Attrs)
	if ctx.args.File == nil {
		// New BUILD files do not have an AST to edit yet. Let Gazelle insert the
		// first managed rule through its normal generation path; later runs will
		// update the checked-in rule in place.
		ctx.gen = append(ctx.gen, managed)
	}
	return nil
}

func (ctx *foldContextValue) applyGeneratedRule(spec managedRuleSpec) {
	if !spec.Present {
		ctx.empty = append(ctx.empty, emptyRule(spec.Kind, spec.Name))
		return
	}
	generated := rule.NewRule(spec.Kind, spec.Name)
	setSortedAttrs(generated, spec.Attrs)
	ctx.gen = append(ctx.gen, generated)
}

func ensureManagedRule(f *rule.File, kind, name string) (*rule.Rule, error) {
	if f == nil {
		return rule.NewRule(kind, name), nil
	}
	for _, existing := range f.Rules {
		if existing.Name() != name {
			continue
		}
		if existing.Kind() != kind {
			return nil, fmt.Errorf(
				"cannot ensure %s %q because %s %q already exists",
				kind,
				name,
				existing.Kind(),
				name,
			)
		}
		return existing, nil
	}
	managed := rule.NewRule(kind, name)
	managed.Insert(f)
	return managed, nil
}

func removeManagedRule(f *rule.File, kind, name string) {
	if f == nil {
		return
	}
	for _, existing := range f.Rules {
		if existing.Kind() == kind && existing.Name() == name {
			existing.Delete()
		}
	}
}

var packageChildExports = starlark.NewBuiltin("child_exports", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*foldContextValue)
	var name string
	if err := starlark.UnpackArgs("child_exports", args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	complete := true
	var labels []string
	for _, subdir := range self.args.Subdirs {
		childRel := path.Join(self.args.Rel, subdir)
		if !self.active.Scope.covers(self.active.Origin, childRel) {
			continue
		}
		childState, ok := self.lang.foldStates[childRel][self.active.Name]
		if !ok || !childState.Complete {
			complete = false
			continue
		}
		if label := childState.Exports[name]; label != "" {
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	if !complete {
		self.complete = false
	}
	return &childExportsValue{
		labels:   labels,
		complete: complete,
	}, nil
})

func (ctx *foldContextValue) applyExport(spec exportSpec) error {
	normalized, err := normalizeExportLabel(ctx.args.Rel, spec.Label)
	if err != nil {
		return err
	}
	ctx.exports[spec.Name] = normalized
	return nil
}

type childExportsValue struct {
	labels   []string
	complete bool
}

func (*childExportsValue) String() string       { return "child_exports" }
func (*childExportsValue) Type() string         { return "child_exports" }
func (*childExportsValue) Freeze()              {}
func (*childExportsValue) Truth() starlark.Bool { return starlark.True }
func (*childExportsValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("child exports are unhashable")
}

func (c *childExportsValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "labels":
		return stringList(c.labels), nil
	case "complete":
		return starlark.Bool(c.complete), nil
	default:
		return nil, nil
	}
}

func (*childExportsValue) AttrNames() []string {
	return []string{"labels", "complete"}
}

func paramsDict(params map[string]any) (*starlark.Dict, error) {
	out := starlark.NewDict(len(params))
	for name, raw := range params {
		value, err := toStarlarkValue(raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", name, err)
		}
		if err := out.SetKey(starlark.String(name), value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func toStarlarkValue(value any) (starlark.Value, error) {
	switch typed := value.(type) {
	case nil:
		return starlark.None, nil
	case string:
		return starlark.String(typed), nil
	case bool:
		return starlark.Bool(typed), nil
	case int64:
		return starlark.MakeInt64(typed), nil
	case []string:
		return stringList(typed), nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func stringList(values []string) *starlark.List {
	items := make([]starlark.Value, 0, len(values))
	for _, value := range values {
		items = append(items, starlark.String(value))
	}
	return starlark.NewList(items)
}

func readStringSequence(name string, value starlark.Value) ([]string, error) {
	var iterable starlark.Iterable
	switch typed := value.(type) {
	case *starlark.List:
		iterable = typed
	case starlark.Tuple:
		iterable = typed
	default:
		return nil, fmt.Errorf("%s must be a list or tuple of strings", name)
	}
	iter := iterable.Iterate()
	defer iter.Done()

	var out []string
	var item starlark.Value
	for iter.Next(&item) {
		str, ok := starlark.AsString(item)
		if !ok {
			return nil, fmt.Errorf("%s must contain only strings", name)
		}
		out = append(out, str)
	}
	return out, nil
}

func readManagedAttrs(api string, value starlark.Value) (map[string]any, error) {
	if value == nil || value == starlark.None {
		return nil, nil
	}
	dict, ok := value.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("%s must be a dict", api)
	}
	out := make(map[string]any, dict.Len())
	for _, item := range dict.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("%s keys must be strings", api)
		}
		parsed, err := readManagedAttrValue(fmt.Sprintf("%s[%q]", api, key), item[1])
		if err != nil {
			return nil, err
		}
		out[key] = parsed
	}
	return out, nil
}

func readManagedAttrValue(name string, value starlark.Value) (any, error) {
	switch typed := value.(type) {
	case starlark.Bool:
		return bool(typed), nil
	case starlark.String:
		return string(typed), nil
	case *starlark.List:
		return readStringSequence(name, typed)
	case starlark.Tuple:
		return readStringSequence(name, typed)
	}
	return nil, fmt.Errorf("%s must be a bool, string, or list or tuple of strings", name)
}

func setSortedAttrs(r *rule.Rule, attrs map[string]any) {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r.SetAttr(name, attrs[name])
	}
}

func normalizeExportLabel(rel, label string) (string, error) {
	switch {
	case strings.HasPrefix(label, ":") && len(label) > 1:
		return recursiveLabel(rel, strings.TrimPrefix(label, ":")), nil
	case strings.HasPrefix(label, "//"):
		return label, nil
	default:
		return "", fmt.Errorf("export label must be local or absolute, got %q", label)
	}
}
