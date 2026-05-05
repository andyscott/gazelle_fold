package policy

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type moduleID struct {
	Mount string
	Path  string
}

func (id moduleID) String() string {
	return id.Mount + ":" + id.Path
}

type moduleEntry struct {
	globals starlark.StringDict
	err     error
}

type moduleSource struct {
	id       moduleID
	filename string
	src      any
}

type moduleMount struct {
	name string
	read func(id moduleID) (moduleSource, error)
}

type mountTable map[string]moduleMount

type starlarkLoader struct {
	definitions map[string]definition
	modules     map[string]*moduleEntry
	mounts      mountTable
	predeclared starlark.StringDict
}

func loadPolicyFile(repoRoot, packageRel, spec string) (map[string]definition, error) {
	loader := newStarlarkLoader(repoRoot)
	base := moduleID{
		Mount: "root",
		Path:  path.Join(packageRel, "BUILD.bazel"),
	}
	id, err := loader.resolveModuleRef(spec, base)
	if err != nil {
		return nil, err
	}
	if _, err := loader.loadModule(id); err != nil {
		return nil, err
	}
	return loader.definitions, nil
}

func newStarlarkLoader(repoRoot string) *starlarkLoader {
	loader := &starlarkLoader{
		definitions: make(map[string]definition),
		modules:     make(map[string]*moduleEntry),
		mounts:      newMountTable(repoRoot),
	}
	loader.predeclared = starlark.StringDict{
		"gazelle_fold": starlarkstruct.FromStringDict(
			starlark.String("gazelle_fold"),
			starlark.StringDict{
				"param":          starlark.NewBuiltin("gazelle_fold.param", loader.paramBuiltin),
				"rule_policy":    starlark.NewBuiltin("gazelle_fold.rule_policy", loader.rulePolicyBuiltin),
				"package_policy": starlark.NewBuiltin("gazelle_fold.package_policy", loader.packagePolicyBuiltin),
			},
		),
	}
	return loader
}

func newMountTable(repoRoot string) mountTable {
	return mountTable{
		"std": {
			name: "std",
			read: func(id moduleID) (moduleSource, error) {
				src, err := builtinModuleSource(id.Path)
				if err != nil {
					return moduleSource{}, err
				}
				return moduleSource{
					id:       id,
					filename: id.String(),
					src:      src,
				}, nil
			},
		},
		"root": {
			name: "root",
			read: func(id moduleID) (moduleSource, error) {
				return moduleSource{
					id:       id,
					filename: filepath.Join(repoRoot, filepath.FromSlash(id.Path)),
				}, nil
			},
		},
	}
}

func (l *starlarkLoader) loadModule(id moduleID) (starlark.StringDict, error) {
	key := id.String()
	if entry, ok := l.modules[key]; ok {
		if entry == nil {
			return nil, fmt.Errorf("cycle in policy load graph at %q", key)
		}
		return entry.globals, entry.err
	}

	source, err := l.moduleSource(id)
	if err != nil {
		return nil, err
	}

	l.modules[key] = nil // sentinel for cycle detection while the file runs
	thread := &starlark.Thread{
		Name: "gazelle_fold " + key,
		Load: func(_ *starlark.Thread, spec string) (starlark.StringDict, error) {
			child, err := l.resolveModuleRef(spec, source.id)
			if err != nil {
				return nil, err
			}
			return l.loadModule(child)
		},
	}
	globals, err := starlark.ExecFile(thread, source.filename, source.src, l.predeclared)
	l.modules[key] = &moduleEntry{globals: globals, err: err}
	return globals, err
}

func (l *starlarkLoader) moduleSource(id moduleID) (moduleSource, error) {
	mount, ok := l.mounts[id.Mount]
	if !ok {
		return moduleSource{}, fmt.Errorf("unknown policy mount %q", id.Mount)
	}
	return mount.read(id)
}

func (l *starlarkLoader) resolveModuleRef(spec string, importer moduleID) (moduleID, error) {
	if spec == "" {
		return moduleID{}, fmt.Errorf("policy module path must not be empty")
	}
	if prefix, mountedPath, ok := strings.Cut(spec, ":"); ok {
		if prefix == "" {
			return moduleID{}, fmt.Errorf("policy mount prefix must not be empty in %q", spec)
		}
		if _, exists := l.mounts[prefix]; !exists {
			return moduleID{}, fmt.Errorf("unknown policy mount %q", prefix)
		}
		clean, err := cleanModulePath(mountedPath)
		if err != nil {
			return moduleID{}, fmt.Errorf("invalid %s module path %q: %w", prefix, mountedPath, err)
		}
		return moduleID{Mount: prefix, Path: clean}, nil
	}
	if strings.HasPrefix(spec, "/") {
		return moduleID{}, fmt.Errorf("relative policy module path must not start with /: %q", spec)
	}
	clean, err := cleanModulePath(path.Join(path.Dir(importer.Path), spec))
	if err != nil {
		return moduleID{}, fmt.Errorf("invalid relative module path %q from %s: %w", spec, importer.String(), err)
	}
	return moduleID{Mount: importer.Mount, Path: clean}, nil
}

func cleanModulePath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("path escapes its mount")
	}
	return clean, nil
}

func (l *starlarkLoader) paramBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		typ          string
		required     bool
		defaultValue starlark.Value
	)
	if err := starlark.UnpackArgs("gazelle_fold.param", args, kwargs,
		"type", &typ,
		"required?", &required,
		"default??", &defaultValue,
	); err != nil {
		return nil, err
	}
	specType, err := parseParamType(typ)
	if err != nil {
		return nil, err
	}
	if required && defaultValue != nil {
		return nil, fmt.Errorf("gazelle_fold.param cannot be both required and have a default")
	}
	var value any
	if defaultValue != nil {
		value, err = readParamValue("gazelle_fold.param.default", specType, defaultValue)
		if err != nil {
			return nil, err
		}
	}
	return &paramSpecValue{spec: paramSpec{
		Type:     specType,
		Required: required,
		Default:  value,
	}}, nil
}

func (l *starlarkLoader) rulePolicyBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name   string
		params *starlark.Dict
		apply  *starlark.Function
	)
	if err := starlark.UnpackArgs("gazelle_fold.rule_policy", args, kwargs,
		"name", &name,
		"params?", &params,
		"apply", &apply,
	); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("rule policy name must not be empty")
	}
	paramSpecs, err := unpackParamSpecs(params)
	if err != nil {
		return nil, err
	}
	if err := validateCallback("rule policy", name, apply, 2); err != nil {
		return nil, err
	}
	if _, exists := l.definitions[name]; exists {
		return nil, fmt.Errorf("policy %q is already registered", name)
	}
	l.definitions[name] = definition{
		Name:   name,
		Kind:   kindRulePolicy,
		Params: paramSpecs,
		Apply:  apply,
	}
	return starlark.None, nil
}

func (l *starlarkLoader) packagePolicyBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name   string
		params *starlark.Dict
		apply  *starlark.Function
	)
	if err := starlark.UnpackArgs("gazelle_fold.package_policy", args, kwargs,
		"name", &name,
		"params?", &params,
		"apply", &apply,
	); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("package policy name must not be empty")
	}
	paramSpecs, err := unpackParamSpecs(params)
	if err != nil {
		return nil, err
	}
	if err := validateCallback("package policy", name, apply, 1); err != nil {
		return nil, err
	}
	if _, exists := l.definitions[name]; exists {
		return nil, fmt.Errorf("policy %q is already registered", name)
	}
	l.definitions[name] = definition{
		Name:   name,
		Kind:   kindPackagePolicy,
		Params: paramSpecs,
		Apply:  apply,
	}
	return starlark.None, nil
}

type paramSpecValue struct {
	spec paramSpec
}

func (*paramSpecValue) String() string        { return "param(...)" }
func (*paramSpecValue) Type() string          { return "param" }
func (*paramSpecValue) Freeze()               {}
func (*paramSpecValue) Truth() starlark.Bool  { return starlark.True }
func (*paramSpecValue) Hash() (uint32, error) { return 0, fmt.Errorf("param is unhashable") }

func unpackParamSpecs(dict *starlark.Dict) (map[string]paramSpec, error) {
	if dict == nil || dict.Len() == 0 {
		return nil, nil
	}
	out := make(map[string]paramSpec, dict.Len())
	iter := dict.Iterate()
	defer iter.Done()
	var key starlark.Value
	for iter.Next(&key) {
		name, ok := starlark.AsString(key)
		if !ok {
			return nil, fmt.Errorf("policy params keys must be strings")
		}
		raw, _, err := dict.Get(key)
		if err != nil {
			return nil, err
		}
		value, ok := raw.(*paramSpecValue)
		if !ok {
			return nil, fmt.Errorf("policy param %q must be declared with gazelle_fold.param(...)", name)
		}
		spec := value.spec
		spec.Name = name
		out[name] = spec
	}
	return out, nil
}

func parseParamType(raw string) (paramType, error) {
	switch raw {
	case "strings":
		return paramStrings, nil
	case "string":
		return paramString, nil
	case "bool":
		return paramBool, nil
	case "int":
		return paramInt, nil
	default:
		return 0, fmt.Errorf("unsupported param type %q", raw)
	}
}

func readParamValue(name string, typ paramType, value starlark.Value) (any, error) {
	switch typ {
	case paramStrings:
		return readStringSequence(name, value)
	case paramString:
		str, ok := starlark.AsString(value)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", name)
		}
		return str, nil
	case paramBool:
		boolean, ok := value.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("%s must be a bool", name)
		}
		return bool(boolean), nil
	case paramInt:
		integer, ok := value.(starlark.Int)
		if !ok {
			return nil, fmt.Errorf("%s must be an int", name)
		}
		i64, ok := integer.Int64()
		if !ok {
			return nil, fmt.Errorf("%s must fit in int64", name)
		}
		return i64, nil
	default:
		return nil, fmt.Errorf("%s has unsupported param type", name)
	}
}

func validateCallback(kind, name string, fn *starlark.Function, wantParams int) error {
	if fn == nil {
		return fmt.Errorf("%s %q apply must be a function", kind, name)
	}
	if fn.NumParams() != wantParams || fn.NumKwonlyParams() != 0 || fn.HasVarargs() || fn.HasKwargs() {
		return fmt.Errorf("%s %q apply must accept exactly %d positional argument(s)", kind, name, wantParams)
	}
	return nil
}
