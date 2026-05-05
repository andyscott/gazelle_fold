package fold

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

type foldLang struct {
	language.BaseLang
	language.BaseLifecycleManager
	foldStates map[string]map[string]foldState
	policyRuns []rulePolicyRun
	violations []policyViolation
}

var _ language.Language = (*foldLang)(nil)

// NewLanguage is the public Gazelle integration point for gazelle_fold.
func NewLanguage() language.Language {
	return &foldLang{
		foldStates: make(map[string]map[string]foldState),
	}
}

func (*foldLang) Name() string { return languageName }

func (*foldLang) RegisterFlags(_ *flag.FlagSet, _ string, c *config.Config) {
	c.Exts[configKey] = newFoldConfig()
}

func (*foldLang) KnownDirectives() []string { return []string{"fold"} }

func (*foldLang) Configure(c *config.Config, rel string, f *rule.File) {
	cfg := currentConfig(c).clone()
	c.Exts[configKey] = cfg
	if f == nil {
		return
	}
	for _, directive := range f.Directives {
		if directive.Key != "fold" {
			continue
		}
		parsed, err := parseDirective(directive.Value)
		if err != nil {
			log.Printf("%s: invalid gazelle:fold directive %q: %v", f.Path, directive.Value, err)
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
					log.Printf("%s: %s %q: %v", f.Path, def.Kind, parsed.Name, err)
					continue
				}
			}
			cfg.addActivation(parsed.Name, rel, parsed.Scope, parsed.Params)
		case directiveSkip:
			// rule.ParseDirectives currently sees comments attached to all
			// statements. Anchored skips are intentionally interpreted only in
			// Fix from the following rule's own leading comments.
		}
	}
}

func (l *foldLang) Fix(c *config.Config, f *rule.File) {
	cfg := currentConfig(c)
	rewrites := effectiveRuleDefinitions(cfg, f.Pkg, kindRuleRewrite)
	policies := effectiveRuleDefinitions(cfg, f.Pkg, kindRulePolicy)
	for _, active := range append(append([]effectiveDefinition(nil), rewrites...), policies...) {
		for _, r := range f.Rules {
			if ruleIsSkipped(r, active.Activation.Name) {
				continue
			}
			if err := runRuleDefinition(active, f.Pkg, f.Path, r, nil); err != nil {
				log.Printf("%s: %s %q on %s %q: %v", f.Path, active.Definition.Kind, active.Activation.Name, r.Kind(), r.Name(), err)
			}
		}
	}
	if len(policies) > 0 {
		// Gazelle resolves generated deps after Fix. Keep the final file and
		// re-run policies after that merge so they describe the BUILD file we
		// emit, not only the pre-generation snapshot we first saw.
		l.policyRuns = append(l.policyRuns, rulePolicyRun{
			file:     f,
			policies: policies,
		})
	}
}

func (l *foldLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	cfg := currentConfig(args.Config)
	var result language.GenerateResult
	for _, active := range effectivePolicies(cfg, args.Rel) {
		if active.Definition.Kind != kindFold {
			continue
		}
		ctx, err := newFoldContextValue(l, args, active)
		if err != nil {
			log.Printf("%s: fold %q: %v", args.Rel, active.Activation.Name, err)
			continue
		}
		if err := runFold(active, ctx); err != nil {
			log.Printf("%s: fold %q: %v", args.Rel, active.Activation.Name, err)
			continue
		}
		result.Gen = append(result.Gen, ctx.gen...)
		result.Empty = append(result.Empty, ctx.empty...)
		for range ctx.gen {
			result.Imports = append(result.Imports, nil)
		}
		if l.foldStates[args.Rel] == nil {
			l.foldStates[args.Rel] = make(map[string]foldState)
		}
		l.foldStates[args.Rel][active.Activation.Name] = foldState{
			Generated: true,
			Complete:  ctx.complete,
			Exports:   cloneExports(ctx.exports),
		}
	}
	return result
}

func (l *foldLang) AfterResolvingDeps(_ context.Context) {
	violations := l.collectRulePolicyViolations()
	if len(violations) == 0 {
		return
	}
	for _, violation := range violations {
		log.Print(violation)
	}
	log.Fatalf("gazelle_fold: %d policy violation(s)", len(violations))
}

func (l *foldLang) collectRulePolicyViolations() []policyViolation {
	l.violations = nil
	for _, run := range l.policyRuns {
		for _, active := range run.policies {
			for _, r := range run.file.Rules {
				if ruleIsSkipped(r, active.Activation.Name) {
					continue
				}
				if err := runRuleDefinition(active, run.file.Pkg, run.file.Path, r, l.recordViolation); err != nil {
					log.Printf("%s: policy %q on %s %q: %v", run.file.Path, active.Activation.Name, r.Kind(), r.Name(), err)
				}
			}
		}
	}
	sort.SliceStable(l.violations, func(i, j int) bool {
		left := l.violations[i]
		right := l.violations[j]
		switch {
		case left.File != right.File:
			return left.File < right.File
		case left.PolicyName != right.PolicyName:
			return left.PolicyName < right.PolicyName
		case left.RuleKind != right.RuleKind:
			return left.RuleKind < right.RuleKind
		case left.RuleName != right.RuleName:
			return left.RuleName < right.RuleName
		default:
			return left.Message < right.Message
		}
	})
	return append([]policyViolation(nil), l.violations...)
}

func (l *foldLang) recordViolation(violation policyViolation) {
	l.violations = append(l.violations, violation)
}

type effectiveDefinition struct {
	Activation activation
	Definition definition
}

type rulePolicyRun struct {
	file     *rule.File
	policies []effectiveDefinition
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

func (p effectiveDefinition) normalizedParams() (*starlark.Dict, error) {
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

func effectivePolicies(cfg *foldConfig, rel string) []effectiveDefinition {
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
	out := make([]effectiveDefinition, 0, len(names))
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
		out = append(out, effectiveDefinition{Activation: winner, Definition: def})
	}
	return out
}

func effectiveRuleDefinitions(cfg *foldConfig, rel string, kind definitionKind) []effectiveDefinition {
	var out []effectiveDefinition
	for _, active := range effectivePolicies(cfg, rel) {
		if active.Definition.Kind == kind {
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

func currentConfig(c *config.Config) *foldConfig {
	if cfg, ok := c.Exts[configKey].(*foldConfig); ok && cfg != nil {
		return cfg
	}
	return newFoldConfig()
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

func ruleIsSkipped(r *rule.Rule, definitionName string) bool {
	for _, comment := range r.Comments() {
		const prefix = "# gazelle:fold "
		if !strings.HasPrefix(comment, prefix) {
			continue
		}
		parsed, err := parseDirective(strings.TrimSpace(strings.TrimPrefix(comment, prefix)))
		if err != nil {
			continue
		}
		if parsed.Kind == directiveSkip && parsed.Name == definitionName {
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

func depsMatching(file string, r *rule.Rule, pkg string, rawPatterns []string) ([]string, bool, error) {
	if r.Attr("deps") == nil || len(rawPatterns) == 0 {
		return nil, true, nil
	}
	current, ok := literalStringListAttr(r.Attr("deps"))
	if !ok {
		return nil, false, nil
	}
	patterns, err := parseDepPatterns(rawPatterns)
	if err != nil {
		return nil, true, err
	}

	var matched []string
	for _, dep := range current {
		parsed, err := label.Parse(dep)
		if err != nil {
			log.Printf("%s: skipping unparsable dep %q on %s %q: %v", file, dep, r.Kind(), r.Name(), err)
			continue
		}
		if depMatchesAnyPattern(parsed.Abs("", pkg), patterns) {
			matched = append(matched, dep)
		}
	}
	return matched, true, nil
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
