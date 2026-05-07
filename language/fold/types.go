package fold

import (
	"fmt"

	"github.com/bazelbuild/bazel-gazelle/rule"
	"go.starlark.net/starlark"
)

const (
	configKey    = "gazelle_fold"
	languageName = "gazelle_fold"
)

type definitionKind int

const (
	kindRuleRewrite definitionKind = iota
	kindRulePolicy
	kindFold
)

func (k definitionKind) String() string {
	switch k {
	case kindRuleRewrite:
		return "rewrite"
	case kindRulePolicy:
		return "policy"
	case kindFold:
		return "fold"
	default:
		return "definition"
	}
}

type paramType int

const (
	paramStrings paramType = iota
	paramString
	paramBool
	paramInt
)

type paramSpec struct {
	Name     string
	Type     paramType
	Required bool
	Default  any
}

type definition struct {
	Name   string
	Kind   definitionKind
	Params map[string]paramSpec
	Apply  *starlark.Function
}

type activation struct {
	Name   string
	Origin string
	Scope  packageScope
	Params map[string]any
	Order  int
}

type foldConfig struct {
	Definitions map[string]definition
	Activations []activation
	nextOrder   int
}

type foldState struct {
	Generated bool
	Complete  bool
	Exports   map[string]string
}

type managedRuleSpec struct {
	Kind            string
	Name            string
	Present         bool
	BoolAttrs       map[string]bool
	StringListAttrs map[string][]string
}

type managedFilegroupSpec struct {
	Name    string
	Srcs    []string
	Present bool
	Public  bool
}

type exportSpec struct {
	Name  string
	Label string
}

type policyViolation struct {
	File       string
	PolicyName string
	RuleKind   string
	RuleName   string
	Message    string
}

func (v policyViolation) String() string {
	return fmt.Sprintf(
		"%s: policy %q on %s %q: %s",
		v.File,
		v.PolicyName,
		v.RuleKind,
		v.RuleName,
		v.Message,
	)
}

func newFoldConfig() *foldConfig {
	return &foldConfig{
		Definitions: make(map[string]definition),
	}
}

func (c *foldConfig) clone() *foldConfig {
	if c == nil {
		return newFoldConfig()
	}
	out := &foldConfig{
		Definitions: make(map[string]definition, len(c.Definitions)),
		Activations: make([]activation, len(c.Activations)),
		nextOrder:   c.nextOrder,
	}
	for name, def := range c.Definitions {
		out.Definitions[name] = def.clone()
	}
	for i, act := range c.Activations {
		out.Activations[i] = act.clone()
	}
	return out
}

func (d definition) clone() definition {
	out := d
	out.Params = cloneParamSpecs(d.Params)
	return out
}

func (a activation) clone() activation {
	out := a
	out.Params = cloneParams(a.Params)
	return out
}

func cloneParamSpecs(params map[string]paramSpec) map[string]paramSpec {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]paramSpec, len(params))
	for name, spec := range params {
		spec.Default = cloneParamValue(spec.Default)
		out[name] = spec
	}
	return out
}

func cloneParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = cloneParamValue(v)
	}
	return out
}

func cloneParamValue(value any) any {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func cloneExports(exports map[string]string) map[string]string {
	if len(exports) == 0 {
		return nil
	}
	out := make(map[string]string, len(exports))
	for name, label := range exports {
		out[name] = label
	}
	return out
}

func (c *foldConfig) addActivation(name, origin string, scope packageScope, params map[string]any) {
	c.nextOrder++
	c.Activations = append(c.Activations, activation{
		Name:   name,
		Origin: origin,
		Scope:  scope,
		Params: cloneParams(params),
		Order:  c.nextOrder,
	})
}

// emptyRule is intentionally tiny: generated empties let Gazelle delete stale
// filegroups without us needing to own Bazel's native filegroup kind.
func emptyRule(kind, name string) *rule.Rule {
	return rule.NewRule(kind, name)
}
