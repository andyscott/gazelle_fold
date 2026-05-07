package fold

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"go.starlark.net/starlark"
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

func TestEffectiveDefinitionsLayerNearestParams(t *testing.T) {
	scopeAll, _ := parseScope("...")
	scopeHere, _ := parseScope(".")
	cfg := newFoldConfig()
	cfg.Definitions["required_tags"] = definition{Kind: kindRulePolicy}
	cfg.addActivation("required_tags", "", scopeAll, map[string]any{
		"kinds": []string{"rust_library"},
		"tags":  []string{"root"},
	})
	cfg.addActivation("required_tags", "child", scopeHere, map[string]any{"tags": []string{"child"}})

	child := effectiveDefinitions(cfg, "child")
	if got := child[0].Activation.Params["tags"].([]string)[0]; got != "child" {
		t.Fatalf("child effective tags = %q, want child", got)
	}
	if got := child[0].Activation.Params["kinds"].([]string)[0]; got != "rust_library" {
		t.Fatalf("child effective kinds = %q, want rust_library", got)
	}
	grandchild := effectiveDefinitions(cfg, "child/grandchild")
	if got := grandchild[0].Activation.Params["tags"].([]string)[0]; got != "root" {
		t.Fatalf("grandchild effective tags = %q, want root", got)
	}
}

func TestEffectiveDefinitionsPreferLaterDirectiveInSameFile(t *testing.T) {
	scopeAll, _ := parseScope("...")
	cfg := newFoldConfig()
	cfg.Definitions["required_tags"] = definition{Kind: kindRulePolicy}
	cfg.addActivation("required_tags", "pkg", scopeAll, map[string]any{"tags": []string{"first"}})
	cfg.addActivation("required_tags", "pkg", scopeAll, map[string]any{"tags": []string{"second"}})

	effective := effectiveDefinitions(cfg, "pkg/child")
	if got := effective[0].Activation.Params["tags"].([]string)[0]; got != "second" {
		t.Fatalf("effective tags = %q, want second", got)
	}
}

func TestParseImportDirective(t *testing.T) {
	got, err := parseDirective(`import("root:build/gazelle_fold/rust.star")`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != directiveImport || got.Label != "root:build/gazelle_fold/rust.star" {
		t.Fatalf("parseDirective(import) = %#v", got)
	}
}

func TestLoadDefinitionFileSupportsRootAndRelativeLoads(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "build/gazelle_fold/helpers.star", `
def register(name):
    gazelle_fold.rewrite(
        name = name,
        apply = lambda ctx, rule: None,
    )
`)
	writeDefinitionFile(t, root, "build/gazelle_fold/definitions.star", `
load("helpers.star", "register")

register("required_tags")
`)

	definitions, err := loadDefinitionFile(root, "", "root:build/gazelle_fold/definitions.star")
	if err != nil {
		t.Fatal(err)
	}
	if got := definitions["required_tags"].Kind; got != kindRuleRewrite {
		t.Fatalf("required_tags kind = %v, want rewrite", got)
	}
}

func TestLoadDefinitionFileSupportsRelativeImportsFromBuildPackage(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "pkg/definitions.star", `
gazelle_fold.rewrite(
    name = "required_tags",
    apply = lambda ctx, rule: None,
)
`)

	definitions, err := loadDefinitionFile(root, "pkg", "definitions.star")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definitions["required_tags"]; !ok {
		t.Fatal("relative import from BUILD package did not register definition")
	}
}

func TestModuleRefsCannotEscapeMount(t *testing.T) {
	loader := newStarlarkLoader(t.TempDir())
	_, err := loader.resolveModuleRef("../../outside.star", moduleID{
		Mount: "root",
		Path:  "pkg/definitions.star",
	})
	if err == nil {
		t.Fatal("resolveModuleRef unexpectedly allowed a path to escape its mount")
	}
}

func TestLoadDefinitionFileSupportsBuiltinRewrites(t *testing.T) {
	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:rewrites/required_tags.star")
	if err != nil {
		t.Fatal(err)
	}
	def, ok := definitions["required_tags"]
	if !ok {
		t.Fatal("built-in required_tags rewrite was not registered")
	}
	if !def.Params["kinds"].Required {
		t.Fatal("built-in required_tags kinds param should be required")
	}
}

func TestLoadDefinitionFileSupportsBuiltinForbiddenDepsPolicy(t *testing.T) {
	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
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
		"name",
		"matches_kind",
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

func TestFoldContextExposesReadOnlyPackageAPI(t *testing.T) {
	value := &foldContextValue{}
	if got, want := value.AttrNames(), []string{
		"params",
		"matching_files",
		"rules_matching",
		"child_exports",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fold attrs = %#v, want %#v", got, want)
	}
}

func TestFoldContextRulesMatchingReturnsLocalRules(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
rust_library(name = "lib")
rust_binary(name = "bin")
rust_test(name = "tests")
filegroup(name = "files")
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &foldContextValue{
		args: language.GenerateArgs{
			Rel:  "pkg",
			File: f,
		},
	}
	attr, err := ctx.Attr("rules_matching")
	if err != nil {
		t.Fatal(err)
	}
	got, err := starlark.Call(
		&starlark.Thread{Name: "test rules_matching"},
		attr.(starlark.Callable),
		nil,
		[]starlark.Tuple{{
			starlark.String("kinds"),
			stringList([]string{"rust_library", "rust_test"}),
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*starlark.List)
	if list.Len() != 2 {
		t.Fatalf("rules_matching len = %d, want 2", list.Len())
	}
	var names []string
	for i := 0; i < list.Len(); i++ {
		names = append(names, list.Index(i).(*ruleValue).rule.Name())
	}
	if want := []string{"lib", "tests"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("rules_matching names = %#v, want %#v", names, want)
	}
}

func TestFoldCanReturnDeclarativeManagedRules(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = "clippy",
            attrs = {
                "edition": "2024",
                "testonly": True,
                "deps": [":lib"],
                "tags": ["clippy"],
            },
        ),
    ]

gazelle_fold.fold(
    name = "clippy",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
rust_library(name = "lib")

rust_clippy(
    name = "clippy",
    deps = [":old"],
    tags = ["legacy"],
)
`))
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "clippy"},
		Definition: definitions["clippy"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{
		Rel:  "pkg",
		File: f,
	}, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := runFold(active, ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.Rules) != 2 {
		t.Fatalf("rules after declarative fold = %d, want 2", len(f.Rules))
	}
	managed := f.Rules[1]
	if got, want := managed.Kind(), "rust_clippy"; got != want {
		t.Fatalf("managed kind = %q, want %q", got, want)
	}
	if got, want := managed.Name(), "clippy"; got != want {
		t.Fatalf("managed name = %q, want %q", got, want)
	}
	if got, want := managed.AttrStrings("deps"), []string{":lib"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed deps = %#v, want %#v", got, want)
	}
	if got, want := managed.AttrStrings("tags"), []string{"clippy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed tags = %#v, want %#v", got, want)
	}
	if got, want := managed.AttrString("edition"), "2024"; got != want {
		t.Fatalf("managed edition = %q, want %q", got, want)
	}
	if !managed.AttrBool("testonly") {
		t.Fatal("managed rule missing testonly = True")
	}
}

func TestFoldCanReturnExplicitlyAbsentManagedRules(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = "clippy",
            present = False,
        ),
    ]

gazelle_fold.fold(
    name = "clippy",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
rust_clippy(
    name = "clippy",
)
`))
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "clippy"},
		Definition: definitions["clippy"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{
		Rel:  "pkg",
		File: f,
	}, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := runFold(active, ctx); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	if got := string(f.Format()); got != "" {
		t.Fatalf("formatted file after declarative removal = %q, want empty", got)
	}
}

func TestFoldRejectsInvalidDeclarativeRuleResults(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return "not a rule list"

gazelle_fold.fold(
    name = "clippy",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "clippy"},
		Definition: definitions["clippy"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{Rel: "pkg"}, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := runFold(active, ctx); err == nil {
		t.Fatal("runFold unexpectedly accepted a non-list declarative result")
	} else if got, want := err.Error(), `fold "clippy" must return None or a list or tuple of fold output values`; got != want {
		t.Fatalf("runFold error = %q, want %q", got, want)
	}
}

func TestFoldRejectsDeclarativeRuleKindConflicts(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = "clippy",
        ),
    ]

gazelle_fold.fold(
    name = "clippy",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
filegroup(
    name = "clippy",
)
`))
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "clippy"},
		Definition: definitions["clippy"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{
		Rel:  "pkg",
		File: f,
	}, active)
	if err != nil {
		t.Fatal(err)
	}
	err = runFold(active, ctx)
	if err == nil {
		t.Fatal("runFold unexpectedly replaced an unrelated same-name rule")
	}
	if got, want := err.Error(), `cannot ensure rust_clippy "clippy" because filegroup "clippy" already exists`; got != want {
		t.Fatalf("runFold error = %q, want %q", got, want)
	}
}

func TestFoldRejectsDuplicateManagedRuleNames(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "filegroup",
            name = "shared_name",
            attrs = {"srcs": ["README.md"]},
        ),
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = "shared_name",
        ),
    ]

gazelle_fold.fold(
    name = "managed",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "managed"},
		Definition: definitions["managed"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{Rel: "pkg"}, active)
	if err != nil {
		t.Fatal(err)
	}
	err = runFold(active, ctx)
	if err == nil {
		t.Fatal("runFold unexpectedly accepted duplicate managed rule names")
	}
	if got, want := err.Error(), `fold "managed" returned duplicate managed rule name "shared_name"`; got != want {
		t.Fatalf("runFold error = %q, want %q", got, want)
	}
}

func TestFoldCanReturnDeclarativeFilegroupsThroughRule(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "filegroup",
            name = "files",
            attrs = {"srcs": ["README.md"]},
        ),
    ]

gazelle_fold.fold(
    name = "files",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "files"},
		Definition: definitions["files"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{Rel: "pkg"}, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := runFold(active, ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := len(ctx.gen), 1; got != want {
		t.Fatalf("generated rules = %d, want %d", got, want)
	}
	if got, want := ctx.gen[0].Kind(), "filegroup"; got != want {
		t.Fatalf("generated kind = %q, want %q", got, want)
	}
	if got, want := ctx.gen[0].AttrStrings("srcs"), []string{"README.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("generated srcs = %#v, want %#v", got, want)
	}
}

func TestFoldCanRemoveDeclarativeFilegroupsThroughRule(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
def _apply(ctx):
    return [
        gazelle_fold.rule(
            kind = "filegroup",
            name = "files",
            present = False,
        ),
    ]

gazelle_fold.fold(
    name = "files",
    apply = _apply,
)
`)
	definitions, err := loadDefinitionFile(root, "", "managed.star")
	if err != nil {
		t.Fatal(err)
	}
	active := effectiveDefinition{
		Activation: activation{Name: "files"},
		Definition: definitions["files"],
	}
	ctx, err := newFoldContextValue(nil, language.GenerateArgs{Rel: "pkg"}, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := runFold(active, ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := len(ctx.empty), 1; got != want {
		t.Fatalf("empty rules = %d, want %d", got, want)
	}
	if got, want := ctx.empty[0].Kind(), "filegroup"; got != want {
		t.Fatalf("empty kind = %q, want %q", got, want)
	}
	if got, want := ctx.empty[0].Name(), "files"; got != want {
		t.Fatalf("empty name = %q, want %q", got, want)
	}
}

func TestRuleConstructorRejectsUnsupportedAttrShapes(t *testing.T) {
	root := t.TempDir()
	writeDefinitionFile(t, root, "managed.star", `
gazelle_fold.rule(
    kind = "rust_clippy",
    name = "clippy",
    attrs = {"deps": {"bad": "shape"}},
)
`)
	if _, err := loadDefinitionFile(root, "", "managed.star"); err == nil {
		t.Fatal("loadDefinitionFile unexpectedly accepted a dict-valued rule attr")
	} else if got, want := err.Error(), `gazelle_fold.rule.attrs["deps"] must be a bool, string, or list or tuple of strings`; got != want {
		t.Fatalf("loadDefinitionFile error = %q, want %q", got, want)
	}
}

func TestDefinitionRejectsUnknownParams(t *testing.T) {
	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:rewrites/required_tags.star")
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

func TestRuleAnchoredSkip(t *testing.T) {
	f, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
# gazelle:fold skip("required_tags", reason = "vendored")
rust_library(name = "skip")

rust_library(name = "fix")
`))
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:rewrites/required_tags.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newFoldConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("required_tags", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"tags":  []string{"team:runtime"},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*foldLang)
	lang.Fix(c, f)
	if got := f.Rules[0].AttrStrings("tags"); got != nil {
		t.Fatalf("skipped rule tags = %v, want nil", got)
	}
	if got := f.Rules[1].AttrStrings("tags"); len(got) != 1 || got[0] != "team:runtime" {
		t.Fatalf("non-skipped rule tags = %v, want team:runtime", got)
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

	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newFoldConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*foldLang)
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

	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newFoldConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*foldLang)
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

	definitions, err := loadDefinitionFile(t.TempDir(), "", "std:policies/forbidden_deps.star")
	if err != nil {
		t.Fatal(err)
	}

	cfg := newFoldConfig()
	cfg.Definitions = definitions
	scope, _ := parseScope("...")
	cfg.addActivation("forbidden_deps", "", scope, map[string]any{
		"kinds": []string{"rust_library"},
		"deny":  []string{"//legacy/..."},
	})
	c := config.New()
	c.Exts[configKey] = cfg

	lang := NewLanguage().(*foldLang)
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

func writeDefinitionFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	filename := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
