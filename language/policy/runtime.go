package policy

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"go.starlark.net/starlark"
)

func runRulePolicy(active effectivePolicy, rel, file string, r *rule.Rule) error {
	params, err := active.normalizedParams()
	if err != nil {
		return err
	}
	_, err = starlark.Call(
		&starlark.Thread{Name: "gazelle_policy rule " + active.Activation.Name},
		active.Definition.Apply,
		starlark.Tuple{
			&ruleContextValue{
				rel:        rel,
				policyName: active.Activation.Name,
				params:     params,
			},
			&ruleValue{
				file: file,
				pkg:  rel,
				rule: r,
			},
		},
		nil,
	)
	return err
}

func runPackagePolicy(active effectivePolicy, ctx *packageContextValue) error {
	_, err := starlark.Call(
		&starlark.Thread{Name: "gazelle_policy package " + active.Activation.Name},
		active.Definition.Apply,
		starlark.Tuple{ctx},
		nil,
	)
	return err
}

type ruleContextValue struct {
	rel        string
	policyName string
	params     *starlark.Dict
}

func (*ruleContextValue) String() string       { return "rule_policy_context" }
func (*ruleContextValue) Type() string         { return "rule_policy_context" }
func (*ruleContextValue) Freeze()              {}
func (*ruleContextValue) Truth() starlark.Bool { return starlark.True }
func (*ruleContextValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("rule policy context is unhashable")
}

func (ctx *ruleContextValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "rel":
		return starlark.String(ctx.rel), nil
	case "policy_name":
		return starlark.String(ctx.policyName), nil
	case "params":
		return ctx.params, nil
	default:
		return nil, nil
	}
}

func (*ruleContextValue) AttrNames() []string {
	return []string{"rel", "policy_name", "params"}
}

type ruleValue struct {
	file string
	pkg  string
	rule *rule.Rule
}

func (*ruleValue) String() string        { return "rule" }
func (*ruleValue) Type() string          { return "rule" }
func (*ruleValue) Freeze()               {}
func (*ruleValue) Truth() starlark.Bool  { return starlark.True }
func (*ruleValue) Hash() (uint32, error) { return 0, fmt.Errorf("rule is unhashable") }

func (r *ruleValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "kind":
		return starlark.String(r.rule.Kind()), nil
	case "name":
		return starlark.String(r.rule.Name()), nil
	case "matches_kind":
		return ruleMatchesKind.BindReceiver(r), nil
	case "list_attr":
		return ruleListAttr.BindReceiver(r), nil
	case "ensure_list_attr_contains":
		return ruleEnsureListAttrContains.BindReceiver(r), nil
	case "remove_deps_matching":
		return ruleRemoveDepsMatching.BindReceiver(r), nil
	default:
		return nil, nil
	}
}

func (*ruleValue) AttrNames() []string {
	return []string{"kind", "name", "matches_kind", "list_attr", "ensure_list_attr_contains", "remove_deps_matching"}
}

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

var ruleListAttr = starlark.NewBuiltin("list_attr", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*ruleValue)
	var name string
	if err := starlark.UnpackArgs("list_attr", args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	values, ok := literalStringListAttr(self.rule.Attr(name))
	if !ok {
		return starlark.None, nil
	}
	return stringList(values), nil
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

var ruleRemoveDepsMatching = starlark.NewBuiltin("remove_deps_matching", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*ruleValue)
	var patterns starlark.Value
	if err := starlark.UnpackArgs("remove_deps_matching", args, kwargs, "patterns", &patterns); err != nil {
		return nil, err
	}
	values, err := readStringSequence("remove_deps_matching.patterns", patterns)
	if err != nil {
		return nil, err
	}
	if err := removeDepsMatching(self.file, self.rule, self.pkg, values); err != nil {
		return nil, err
	}
	return starlark.None, nil
})

type packageContextValue struct {
	lang     *policyLang
	args     language.GenerateArgs
	active   activation
	params   *starlark.Dict
	gen      []*rule.Rule
	empty    []*rule.Rule
	exports  map[string]string
	complete bool
}

func newPackageContextValue(lang *policyLang, args language.GenerateArgs, active effectivePolicy) (*packageContextValue, error) {
	params, err := active.normalizedParams()
	if err != nil {
		return nil, err
	}
	return &packageContextValue{
		lang:     lang,
		args:     args,
		active:   active.Activation,
		params:   params,
		exports:  make(map[string]string),
		complete: true,
	}, nil
}

func (*packageContextValue) String() string       { return "package_policy_context" }
func (*packageContextValue) Type() string         { return "package_policy_context" }
func (*packageContextValue) Freeze()              {}
func (*packageContextValue) Truth() starlark.Bool { return starlark.True }
func (*packageContextValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("package policy context is unhashable")
}

func (ctx *packageContextValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "rel":
		return starlark.String(ctx.args.Rel), nil
	case "policy_name":
		return starlark.String(ctx.active.Name), nil
	case "params":
		return ctx.params, nil
	case "matching_files":
		return packageMatchingFiles.BindReceiver(ctx), nil
	case "ensure_filegroup":
		return packageEnsureFilegroup.BindReceiver(ctx), nil
	case "remove_filegroup":
		return packageRemoveFilegroup.BindReceiver(ctx), nil
	case "child_exports":
		return packageChildExports.BindReceiver(ctx), nil
	case "export":
		return packageExport.BindReceiver(ctx), nil
	default:
		return nil, nil
	}
}

func (*packageContextValue) AttrNames() []string {
	return []string{"rel", "policy_name", "params", "matching_files", "ensure_filegroup", "remove_filegroup", "child_exports", "export"}
}

var packageMatchingFiles = starlark.NewBuiltin("matching_files", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*packageContextValue)
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

var packageEnsureFilegroup = starlark.NewBuiltin("ensure_filegroup", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*packageContextValue)
	var (
		name   string
		srcs   starlark.Value
		public bool
	)
	if err := starlark.UnpackArgs("ensure_filegroup", args, kwargs,
		"name", &name,
		"srcs", &srcs,
		"public?", &public,
	); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("ensure_filegroup name must not be empty")
	}
	values, err := readStringSequence("ensure_filegroup.srcs", srcs)
	if err != nil {
		return nil, err
	}
	generated := rule.NewRule("filegroup", name)
	generated.SetAttr("srcs", values)
	if public && (self.args.File == nil || !self.args.File.HasDefaultVisibility()) {
		generated.SetAttr("visibility", []string{"//visibility:public"})
	}
	self.gen = append(self.gen, generated)
	return starlark.None, nil
})

var packageRemoveFilegroup = starlark.NewBuiltin("remove_filegroup", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*packageContextValue)
	var name string
	if err := starlark.UnpackArgs("remove_filegroup", args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("remove_filegroup name must not be empty")
	}
	self.empty = append(self.empty, emptyRule("filegroup", name))
	return starlark.None, nil
})

var packageChildExports = starlark.NewBuiltin("child_exports", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*packageContextValue)
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
		childState, ok := self.lang.packageStates[childRel][self.active.Name]
		if !ok || !childState.Generated || !childState.Complete {
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

var packageExport = starlark.NewBuiltin("export", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*packageContextValue)
	var (
		name  string
		label string
	)
	if err := starlark.UnpackArgs("export", args, kwargs,
		"name", &name,
		"label", &label,
	); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("export name must not be empty")
	}
	normalized, err := normalizeExportLabel(self.args.Rel, label)
	if err != nil {
		return nil, err
	}
	self.exports[name] = normalized
	return starlark.None, nil
})

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
	if _, ok := value.(starlark.String); ok {
		return nil, fmt.Errorf("%s must be a list or tuple of strings", name)
	}
	iterable, ok := value.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("%s must be a list or tuple of strings", name)
	}
	iter := iterable.Iterate()
	if iter == nil {
		return nil, fmt.Errorf("%s must be a list or tuple of strings", name)
	}
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
