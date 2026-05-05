package policy

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"github.com/bmatcuk/doublestar/v4"
	"go.starlark.net/starlark"

	bzl "github.com/bazelbuild/buildtools/build"
)

type policyLang struct {
	language.BaseLang
	language.BaseLifecycleManager
	packageStates map[string]map[string]packagePolicyState
	ruleRuns      []rulePolicyRun
}

var _ language.Language = (*policyLang)(nil)

// NewLanguage is the public Gazelle integration point for gazelle_policy.
func NewLanguage() language.Language {
	return &policyLang{
		packageStates: make(map[string]map[string]packagePolicyState),
	}
}

func (*policyLang) Name() string { return languageName }

func (*policyLang) RegisterFlags(_ *flag.FlagSet, _ string, c *config.Config) {
	c.Exts[configKey] = newPolicyConfig()
}

func (*policyLang) KnownDirectives() []string { return []string{"policy"} }

func (*policyLang) Configure(c *config.Config, rel string, f *rule.File) {
	cfg := currentConfig(c).clone()
	c.Exts[configKey] = cfg
	if f == nil {
		return
	}
	for _, directive := range f.Directives {
		if directive.Key != "policy" {
			continue
		}
		parsed, err := parseDirective(directive.Value)
		if err != nil {
			log.Printf("%s: invalid gazelle:policy directive %q: %v", f.Path, directive.Value, err)
			continue
		}
		switch parsed.Kind {
		case directiveImport:
			definitions, err := loadPolicyFile(c.RepoRoot, rel, parsed.Label)
			if err != nil {
				log.Printf("%s: loading %s: %v", f.Path, parsed.Label, err)
				continue
			}
			if len(definitions) == 0 {
				log.Printf("%s: import %s registered no policies", f.Path, parsed.Label)
			}
			for name, def := range definitions {
				cfg.Definitions[name] = def
			}
		case directiveUse:
			if def, ok := cfg.Definitions[parsed.Name]; ok {
				if err := def.validateProvidedParams(parsed.Params); err != nil {
					log.Printf("%s: policy %q: %v", f.Path, parsed.Name, err)
					continue
				}
			}
			cfg.addActivation(parsed.Name, rel, parsed.Scope, parsed.Params)
		case directiveExempt:
			// rule.ParseDirectives currently sees comments attached to all
			// statements. Anchored exemptions are intentionally interpreted only
			// in Fix from the following rule's own leading comments.
		}
	}
}

func (l *policyLang) Fix(c *config.Config, f *rule.File) {
	cfg := currentConfig(c)
	policies := effectiveRulePolicies(cfg, f.Pkg)
	for _, active := range policies {
		for _, r := range f.Rules {
			if ruleIsExempt(r, active.Activation.Name) {
				continue
			}
			if err := runRulePolicy(active, f.Pkg, f.Path, r); err != nil {
				log.Printf("%s: policy %q on %s %q: %v", f.Path, active.Activation.Name, r.Kind(), r.Name(), err)
			}
		}
	}
	if len(policies) > 0 {
		// Gazelle resolves generated deps after Fix. Keep the final file and
		// re-run rule policies after that merge so policies describe the BUILD
		// file we emit, not only the pre-generation snapshot we first saw.
		l.ruleRuns = append(l.ruleRuns, rulePolicyRun{
			file:     f,
			policies: policies,
		})
	}
}

func (l *policyLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	cfg := currentConfig(args.Config)
	var result language.GenerateResult
	for _, active := range effectivePolicies(cfg, args.Rel) {
		if active.Definition.Kind != kindPackagePolicy {
			continue
		}
		ctx, err := newPackageContextValue(l, args, active)
		if err != nil {
			log.Printf("%s: policy %q: %v", args.Rel, active.Activation.Name, err)
			continue
		}
		if err := runPackagePolicy(active, ctx); err != nil {
			log.Printf("%s: policy %q: %v", args.Rel, active.Activation.Name, err)
			continue
		}
		result.Gen = append(result.Gen, ctx.gen...)
		result.Empty = append(result.Empty, ctx.empty...)
		for range ctx.gen {
			result.Imports = append(result.Imports, nil)
		}
		if l.packageStates[args.Rel] == nil {
			l.packageStates[args.Rel] = make(map[string]packagePolicyState)
		}
		l.packageStates[args.Rel][active.Activation.Name] = packagePolicyState{
			Generated: true,
			Complete:  ctx.complete,
			Exports:   cloneExports(ctx.exports),
		}
	}
	return result
}

func (l *policyLang) AfterResolvingDeps(_ context.Context) {
	for _, run := range l.ruleRuns {
		for _, active := range run.policies {
			for _, r := range run.file.Rules {
				if ruleIsExempt(r, active.Activation.Name) {
					continue
				}
				if err := runRulePolicy(active, run.file.Pkg, run.file.Path, r); err != nil {
					log.Printf("%s: policy %q on %s %q: %v", run.file.Path, active.Activation.Name, r.Kind(), r.Name(), err)
				}
			}
		}
	}
}

type effectivePolicy struct {
	Activation activation
	Definition definition
}

type rulePolicyRun struct {
	file     *rule.File
	policies []effectivePolicy
}

func (d definition) validateParams(params map[string]any) error {
	if err := d.validateProvidedParams(params); err != nil {
		return err
	}
	for name, spec := range d.Params {
		if spec.Required {
			if _, ok := params[name]; !ok {
				return fmt.Errorf("missing required parameter %q", name)
			}
		}
	}
	return nil
}

func (d definition) validateProvidedParams(params map[string]any) error {
	for name, value := range params {
		spec, ok := d.Params[name]
		if !ok {
			return fmt.Errorf("unknown parameter %q", name)
		}
		if err := validateDirectiveParam(name, spec.Type, value); err != nil {
			return err
		}
	}
	return nil
}

func (p effectivePolicy) normalizedParams() (*starlark.Dict, error) {
	if err := p.Definition.validateParams(p.Activation.Params); err != nil {
		return nil, err
	}
	normalized := make(map[string]any, len(p.Definition.Params))
	for name, spec := range p.Definition.Params {
		if spec.Default != nil {
			normalized[name] = cloneParamValue(spec.Default)
		}
	}
	for name, value := range p.Activation.Params {
		normalized[name] = cloneParamValue(value)
	}
	return paramsDict(normalized)
}

func validateDirectiveParam(name string, typ paramType, value any) error {
	switch typ {
	case paramStrings:
		if _, ok := value.([]string); !ok {
			return fmt.Errorf("parameter %q must be a string list", name)
		}
	case paramString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("parameter %q must be a string", name)
		}
	case paramBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("parameter %q must be a bool", name)
		}
	case paramInt:
		if _, ok := value.(int64); !ok {
			return fmt.Errorf("parameter %q must be an int", name)
		}
	default:
		return fmt.Errorf("parameter %q has unsupported type", name)
	}
	return nil
}

func effectivePolicies(cfg *policyConfig, rel string) []effectivePolicy {
	covering := make(map[string][]activation)
	for _, act := range cfg.Activations {
		if !act.Scope.covers(act.Origin, rel) {
			continue
		}
		covering[act.Name] = append(covering[act.Name], act)
	}
	names := make([]string, 0, len(covering))
	for name := range covering {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]effectivePolicy, 0, len(names))
	for _, name := range names {
		def, ok := cfg.Definitions[name]
		if !ok {
			continue
		}
		acts := covering[name]
		sort.SliceStable(acts, func(i, j int) bool {
			return activationPrecedes(acts[i], acts[j])
		})
		winner := acts[len(acts)-1].clone()
		winner.Params = mergedActivationParams(acts)
		out = append(out, effectivePolicy{Activation: winner, Definition: def})
	}
	return out
}

func effectiveRulePolicies(cfg *policyConfig, rel string) []effectivePolicy {
	var out []effectivePolicy
	for _, active := range effectivePolicies(cfg, rel) {
		if active.Definition.Kind == kindRulePolicy {
			out = append(out, active)
		}
	}
	return out
}

func activationPrecedes(left, right activation) bool {
	leftDepth := packageDepth(left.Origin)
	rightDepth := packageDepth(right.Origin)
	if leftDepth != rightDepth {
		return leftDepth < rightDepth
	}
	return left.Order < right.Order
}

func mergedActivationParams(acts []activation) map[string]any {
	var merged map[string]any
	for _, act := range acts {
		if len(act.Params) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]any)
		}
		for name, value := range act.Params {
			merged[name] = cloneParamValue(value)
		}
	}
	return merged
}

func currentConfig(c *config.Config) *policyConfig {
	if cfg, ok := c.Exts[configKey].(*policyConfig); ok && cfg != nil {
		return cfg
	}
	return newPolicyConfig()
}

func matchesAnyKind(patterns []string, kind string) bool {
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, kind)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func ruleIsExempt(r *rule.Rule, policyName string) bool {
	for _, comment := range r.Comments() {
		const prefix = "# gazelle:policy "
		if !strings.HasPrefix(comment, prefix) {
			continue
		}
		parsed, err := parseDirective(strings.TrimSpace(strings.TrimPrefix(comment, prefix)))
		if err != nil {
			continue
		}
		if parsed.Kind == directiveExempt && parsed.Name == policyName {
			return true
		}
	}
	return false
}

func literalStringListAttr(expr bzl.Expr) ([]string, bool) {
	if expr == nil {
		return nil, true
	}
	list, ok := expr.(*bzl.ListExpr)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list.List))
	for _, item := range list.List {
		str, ok := item.(*bzl.StringExpr)
		if !ok {
			return nil, false
		}
		out = append(out, str.Value)
	}
	return out, true
}

func ensureListAttrContains(file string, r *rule.Rule, attr string, required []string) {
	if len(required) == 0 {
		return
	}
	current, ok := literalStringListAttr(r.Attr(attr))
	if !ok {
		log.Printf("%s: skipping %s %q because %s is not a literal string list", file, r.Kind(), r.Name(), attr)
		return
	}
	seen := make(map[string]bool, len(current))
	for _, value := range current {
		seen[value] = true
	}
	updated := append([]string(nil), current...)
	for _, value := range required {
		if !seen[value] {
			updated = append(updated, value)
			seen[value] = true
		}
	}
	if len(updated) != len(current) {
		r.SetAttr(attr, updated)
	}
}

type depPattern struct {
	exact   *label.Label
	subtree *label.Label
}

func parseDepPatterns(rawPatterns []string) ([]depPattern, error) {
	out := make([]depPattern, 0, len(rawPatterns))
	for _, raw := range rawPatterns {
		pattern, err := parseDepPattern(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, pattern)
	}
	return out, nil
}

func parseDepPattern(raw string) (depPattern, error) {
	if strings.HasSuffix(raw, "/...") || raw == "//..." || strings.HasSuffix(raw, "//...") {
		base := raw
		switch {
		case raw == "//...":
			base = "//"
		case strings.HasSuffix(raw, "//..."):
			base = strings.TrimSuffix(raw, "...")
		default:
			base = strings.TrimSuffix(raw, "/...")
		}
		root, err := label.Parse(base + ":__subtree__")
		if err != nil || root.Relative {
			return depPattern{}, fmt.Errorf("invalid dependency subtree pattern %q", raw)
		}
		return depPattern{subtree: &root}, nil
	}
	exact, err := label.Parse(raw)
	if err != nil || exact.Relative {
		return depPattern{}, fmt.Errorf("invalid dependency label pattern %q", raw)
	}
	return depPattern{exact: &exact}, nil
}

func removeDepsMatching(file string, r *rule.Rule, pkg string, rawPatterns []string) error {
	if r.Attr("deps") == nil || len(rawPatterns) == 0 {
		return nil
	}
	current, ok := literalStringListAttr(r.Attr("deps"))
	if !ok {
		log.Printf("%s: skipping %s %q because deps is not a literal string list", file, r.Kind(), r.Name())
		return nil
	}
	patterns, err := parseDepPatterns(rawPatterns)
	if err != nil {
		return err
	}

	updated := make([]string, 0, len(current))
	for _, dep := range current {
		parsed, err := label.Parse(dep)
		if err != nil {
			log.Printf("%s: keeping unparsable dep %q on %s %q: %v", file, dep, r.Kind(), r.Name(), err)
			updated = append(updated, dep)
			continue
		}
		if depMatchesAnyPattern(parsed.Abs("", pkg), patterns) {
			continue
		}
		updated = append(updated, dep)
	}
	if len(updated) == len(current) {
		return nil
	}
	if len(updated) == 0 {
		r.DelAttr("deps")
		return nil
	}
	r.SetAttr("deps", updated)
	return nil
}

func depMatchesAnyPattern(dep label.Label, patterns []depPattern) bool {
	for _, pattern := range patterns {
		switch {
		case pattern.exact != nil && pattern.exact.Equal(dep):
			return true
		case pattern.subtree != nil && pattern.subtree.Contains(dep):
			return true
		}
	}
	return false
}

func matchingFiles(files, patterns []string) []string {
	var out []string
	for _, file := range files {
		for _, pattern := range patterns {
			matched, err := doublestar.Match(pattern, file)
			if err == nil && matched {
				out = append(out, file)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func recursiveLabel(rel, name string) string {
	if rel == "" {
		return "//:" + name
	}
	return "//" + rel + ":" + name
}
