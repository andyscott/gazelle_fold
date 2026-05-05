package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestScopeCoverage(t *testing.T) {
	tests := []struct {
		scope  string
		target string
		want   bool
	}{
		{scope: ".", target: "foo", want: true},
		{scope: ".", target: "foo/bar", want: false},
		{scope: "...", target: "foo/bar", want: true},
		{scope: "bar", target: "foo/bar", want: true},
		{scope: "bar", target: "foo/bar/baz", want: false},
		{scope: "bar/...", target: "foo/bar/baz", want: true},
	}
	for _, tt := range tests {
		scope, err := parseScope(tt.scope)
		if err != nil {
			t.Fatalf("parseScope(%q): %v", tt.scope, err)
		}
		if got := scope.covers("foo", tt.target); got != tt.want {
			t.Errorf("scope %q covers %q = %v, want %v", tt.scope, tt.target, got, tt.want)
		}
	}
}

func TestEffectivePolicyLayersNearestParams(t *testing.T) {
	scopeAll, _ := parseScope("...")
	scopeHere, _ := parseScope(".")
	cfg := newPolicyConfig()
	cfg.Definitions["required_tags"] = definition{Name: "required_tags", Kind: kindRulePolicy}
	cfg.addActivation("required_tags", "", scopeAll, map[string]any{
		"kinds": []string{"rust_library"},
		"tags":  []string{"root"},
	})
	cfg.addActivation("required_tags", "child", scopeHere, map[string]any{"tags": []string{"child"}})

	child := effectivePolicies(cfg, "child")
	if got := child[0].Activation.Params["tags"].([]string)[0]; got != "child" {
		t.Fatalf("child effective tags = %q, want child", got)
	}
	if got := child[0].Activation.Params["kinds"].([]string)[0]; got != "rust_library" {
		t.Fatalf("child effective kinds = %q, want rust_library", got)
	}
	grandchild := effectivePolicies(cfg, "child/grandchild")
	if got := grandchild[0].Activation.Params["tags"].([]string)[0]; got != "root" {
		t.Fatalf("grandchild effective tags = %q, want root", got)
	}
}

func TestEffectivePolicyPrefersLaterDirectiveInSameFile(t *testing.T) {
	scopeAll, _ := parseScope("...")
	cfg := newPolicyConfig()
	cfg.Definitions["required_tags"] = definition{Name: "required_tags", Kind: kindRulePolicy}
	cfg.addActivation("required_tags", "pkg", scopeAll, map[string]any{"tags": []string{"first"}})
	cfg.addActivation("required_tags", "pkg", scopeAll, map[string]any{"tags": []string{"second"}})

	effective := effectivePolicies(cfg, "pkg/child")
	if got := effective[0].Activation.Params["tags"].([]string)[0]; got != "second" {
		t.Fatalf("effective tags = %q, want second", got)
	}
}

func TestParseImportDirective(t *testing.T) {
	got, err := parseDirective(`import("root:build/policies/rust.star")`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != directiveImport || got.Label != "root:build/policies/rust.star" {
		t.Fatalf("parseDirective(import) = %#v", got)
	}
}

func TestLoadPolicyFileSupportsRootAndRelativeLoads(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, "build/policies/helpers.star", `
def register(name):
    gazelle_policy.rule_policy(
        name = name,
        apply = lambda ctx, rule: None,
    )
`)
	writePolicyFile(t, root, "build/policies/policies.star", `
load("helpers.star", "register")

register("required_tags")
`)

	definitions, err := loadPolicyFile(root, "", "root:build/policies/policies.star")
	if err != nil {
		t.Fatal(err)
	}
	if got := definitions["required_tags"].Kind; got != kindRulePolicy {
		t.Fatalf("required_tags kind = %v, want rule policy", got)
	}
}

func TestLoadPolicyFileSupportsRelativeImportsFromBuildPackage(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, "pkg/policies.star", `
gazelle_policy.rule_policy(
    name = "required_tags",
    apply = lambda ctx, rule: None,
)
`)

	definitions, err := loadPolicyFile(root, "pkg", "policies.star")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definitions["required_tags"]; !ok {
		t.Fatal("relative import from BUILD package did not register policy")
	}
}

func TestModuleRefsCannotEscapeMount(t *testing.T) {
	loader := newStarlarkLoader(t.TempDir())
	_, err := loader.resolveModuleRef("../../outside.star", moduleID{
		Mount: "root",
		Path:  "pkg/policies.star",
	})
	if err == nil {
		t.Fatal("resolveModuleRef unexpectedly allowed a path to escape its mount")
	}
}

func TestLoadPolicyFileSupportsBuiltinPolicies(t *testing.T) {
	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/required_tags.star")
	if err != nil {
		t.Fatal(err)
	}
	def, ok := definitions["required_tags"]
	if !ok {
		t.Fatal("built-in required_tags policy was not registered")
	}
	if !def.Params["kinds"].Required {
		t.Fatal("built-in required_tags kinds param should be required")
	}
}

func TestLoadPolicyFileSupportsBuiltinForbiddenDepsPolicy(t *testing.T) {
	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}
	def, ok := definitions["forbidden_deps"]
	if !ok {
		t.Fatal("built-in forbidden_deps policy was not registered")
	}
	if !def.Params["kinds"].Required || !def.Params["deny"].Required {
		t.Fatal("built-in forbidden_deps params should be required")
	}
}

func TestRuleValueExposesValidationOnlyDepsAPI(t *testing.T) {
	value := &ruleValue{}
	if got, want := value.AttrNames(), []string{
		"kind",
		"name",
		"matches_kind",
		"list_attr",
		"ensure_list_attr_contains",
		"deps_matching",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rule attrs = %#v, want %#v", got, want)
	}
	attr, err := value.Attr("remove_deps_matching")
	if err != nil {
		t.Fatal(err)
	}
	if attr != nil {
		t.Fatalf("remove_deps_matching attr = %#v, want nil", attr)
	}
}

func TestDefinitionRejectsUnknownParams(t *testing.T) {
	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/required_tags.star")
	if err != nil {
		t.Fatal(err)
	}
	err = definitions["required_tags"].validateParams(map[string]any{
		"kinds": []string{"rust_library"},
		"tagz":  []string{"typo"},
	})
	if err == nil {
		t.Fatal("validateParams unexpectedly accepted typo param")
	}
}

func TestRuleAnchoredExemption(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
# gazelle:policy exempt("required_tags", reason = "vendored")
rust_library(name = "skip")

rust_library(name = "fix")
`))
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/required_tags.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newPolicyConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("required_tags", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"tags":  []string{"team:runtime"},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*policyLang)
	lang.Fix(c, f)
	if got := f.Rules[0].AttrStrings("tags"); got != nil {
		t.Fatalf("exempt rule tags = %v, want nil", got)
	}
	if got := f.Rules[1].AttrStrings("tags"); len(got) != 1 || got[0] != "team:runtime" {
		t.Fatalf("non-exempt rule tags = %v, want team:runtime", got)
	}
}

func TestForbiddenDepsReportsExactAndSubtreeMatches(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "legacy", []byte(`
rust_library(
    name = "lib",
    deps = [
        ":local_bad",
        "//legacy/sub:bad",
        "//safe:ok",
    ],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newPolicyConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*policyLang)
	lang.Fix(c, f)
	violations := lang.collectRulePolicyViolations()
	if got, want := violations, []policyViolation{{
		File:       "BUILD.bazel",
		PolicyName: "forbidden_deps",
		RuleKind:   "rust_library",
		RuleName:   "lib",
		Message:    "forbidden deps: :local_bad, //legacy/sub:bad",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("violations = %#v, want %#v", got, want)
	}
	if got := f.Rules[0].AttrStrings("deps"); len(got) != 3 {
		t.Fatalf("deps after policy = %v, want original deps preserved", got)
	}
}

func TestRulePoliciesRunAgainAfterDependencyResolution(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "app", []byte(`
rust_library(
    name = "lib",
    deps = ["//safe:ok"],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newPolicyConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*policyLang)
	lang.Fix(c, f)
	f.Rules[0].SetAttr("deps", []string{"//safe:ok", "//legacy:bad"})
	violations := lang.collectRulePolicyViolations()
	if got, want := violations, []policyViolation{{
		File:       "BUILD.bazel",
		PolicyName: "forbidden_deps",
		RuleKind:   "rust_library",
		RuleName:   "lib",
		Message:    "forbidden deps: //legacy:bad",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("violations = %#v, want %#v", got, want)
	}
}

func TestForbiddenDepsFailsClosedForNonLiteralDeps(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "app", []byte(`
rust_library(
    name = "lib",
    deps = select({
        "//conditions:default": ["//legacy:bad"],
    }),
)
`))
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := loadPolicyFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newPolicyConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*policyLang)
	lang.Fix(c, f)
	violations := lang.collectRulePolicyViolations()
	if got, want := violations, []policyViolation{{
		File:       "BUILD.bazel",
		PolicyName: "forbidden_deps",
		RuleKind:   "rust_library",
		RuleName:   "lib",
		Message:    "cannot validate forbidden deps because deps is not a literal string list",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("violations = %#v, want %#v", got, want)
	}
}

func writePolicyFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	filename := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
