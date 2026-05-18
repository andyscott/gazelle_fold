package fold

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"go.starlark.net/starlark"

	bzl "github.com/bazelbuild/buildtools/build"
)

type depsValue struct {
	pkg         string
	rule        *rule.Rule
	depPatterns *depPatternCache
}

func (*depsValue) String() string       { return "deps" }
func (*depsValue) Type() string         { return "deps" }
func (*depsValue) Freeze()              {}
func (*depsValue) Truth() starlark.Bool { return starlark.True }
func (*depsValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("deps is unhashable")
}

func (d *depsValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "labels_matching":
		return depsLabelsMatchingBuiltin.BindReceiver(d), nil
	case "label_literals_matching":
		return depsLabelLiteralsMatchingBuiltin.BindReceiver(d), nil
	default:
		return nil, nil
	}
}

func (*depsValue) AttrNames() []string {
	return []string{"labels_matching", "label_literals_matching"}
}

var depsLabelsMatchingBuiltin = starlark.NewBuiltin("labels_matching", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*depsValue)
	var patterns starlark.Value
	if err := starlark.UnpackArgs("labels_matching", args, kwargs, "patterns", &patterns); err != nil {
		return nil, err
	}
	values, err := readStringSequence("labels_matching.patterns", patterns)
	if err != nil {
		return nil, err
	}
	matched, supported, err := depsMatching(self.rule, self.pkg, values, self.depPatterns)
	if err != nil {
		return nil, err
	}
	if !supported {
		return starlark.None, nil
	}
	return stringList(matched), nil
})

var depsLabelLiteralsMatchingBuiltin = starlark.NewBuiltin("label_literals_matching", func(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := fn.Receiver().(*depsValue)
	var (
		patterns     starlark.Value
		allowedCalls starlark.Value
	)
	if err := starlark.UnpackArgs("label_literals_matching", args, kwargs,
		"patterns", &patterns,
		"allowed_calls?", &allowedCalls,
	); err != nil {
		return nil, err
	}
	values, err := readStringSequence("label_literals_matching.patterns", patterns)
	if err != nil {
		return nil, err
	}
	var allowed []string
	if allowedCalls != nil {
		allowed, err = readStringSequence("label_literals_matching.allowed_calls", allowedCalls)
		if err != nil {
			return nil, err
		}
	}
	matched, err := depsLabelLiteralsMatching(self.rule, self.pkg, values, allowed, self.depPatterns)
	if err != nil {
		return nil, err
	}
	return &depLabelLiteralMatchesValue{
		matches:  matched.matches,
		complete: matched.complete,
	}, nil
})

type depLabelLiteralMatchesValue struct {
	matches  []string
	complete bool
}

func (*depLabelLiteralMatchesValue) String() string       { return "label_literals_matching" }
func (*depLabelLiteralMatchesValue) Type() string         { return "label_literals_matching" }
func (*depLabelLiteralMatchesValue) Freeze()              {}
func (*depLabelLiteralMatchesValue) Truth() starlark.Bool { return starlark.True }
func (*depLabelLiteralMatchesValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("deps label literal matches are unhashable")
}

func (m *depLabelLiteralMatchesValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "matches":
		return stringList(m.matches), nil
	case "complete":
		return starlark.Bool(m.complete), nil
	default:
		return nil, nil
	}
}

func (*depLabelLiteralMatchesValue) AttrNames() []string {
	return []string{"matches", "complete"}
}

type depPattern struct {
	exact   *label.Label
	subtree *label.Label
}

type depPatternCache struct {
	entries map[string]depPatternCacheEntry
}

type depPatternCacheEntry struct {
	patterns []depPattern
	err      error
}

func newDepPatternCache() *depPatternCache {
	return &depPatternCache{}
}

func (c *depPatternCache) parse(rawPatterns []string) ([]depPattern, error) {
	if c == nil {
		return parseDepPatterns(rawPatterns)
	}
	if c.entries == nil {
		c.entries = make(map[string]depPatternCacheEntry)
	}
	key := depPatternCacheKey(rawPatterns)
	if entry, ok := c.entries[key]; ok {
		return entry.patterns, entry.err
	}
	patterns, err := parseDepPatterns(rawPatterns)
	c.entries[key] = depPatternCacheEntry{
		patterns: patterns,
		err:      err,
	}
	return patterns, err
}

func depPatternCacheKey(rawPatterns []string) string {
	var b strings.Builder
	for _, pattern := range rawPatterns {
		b.WriteString(strconv.Itoa(len(pattern)))
		b.WriteByte(':')
		b.WriteString(pattern)
		b.WriteByte(';')
	}
	return b.String()
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

func depsMatching(r *rule.Rule, pkg string, rawPatterns []string, cache *depPatternCache) ([]string, bool, error) {
	if r.Attr("deps") == nil || len(rawPatterns) == 0 {
		return nil, true, nil
	}
	current, ok := literalStringListAttr(r.Attr("deps"))
	if !ok {
		return nil, false, nil
	}
	patterns, err := cache.parse(rawPatterns)
	if err != nil {
		return nil, true, err
	}

	matched, supported := matchingDepLabels(pkg, current, patterns, invalidDepLabelUnsupported)
	return matched, supported, nil
}

type invalidDepLabelMode int

const (
	invalidDepLabelUnsupported invalidDepLabelMode = iota
	invalidDepLabelIgnored
)

func matchingDepLabels(pkg string, labels []string, patterns []depPattern, invalid invalidDepLabelMode) ([]string, bool) {
	var matched []string
	for _, raw := range labels {
		parsed, err := label.Parse(raw)
		if err != nil {
			if invalid == invalidDepLabelUnsupported {
				return nil, false
			}
			continue
		}
		if depMatchesAnyPattern(parsed.Abs("", pkg), patterns) {
			matched = append(matched, raw)
		}
	}
	return matched, true
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

type depLabelLiteralMatches struct {
	matches  []string
	complete bool
}

func depsLabelLiteralsMatching(r *rule.Rule, pkg string, rawPatterns, rawAllowedCalls []string, cache *depPatternCache) (depLabelLiteralMatches, error) {
	if r.Attr("deps") == nil || len(rawPatterns) == 0 {
		return depLabelLiteralMatches{complete: true}, nil
	}
	patterns, err := cache.parse(rawPatterns)
	if err != nil {
		return depLabelLiteralMatches{complete: true}, err
	}
	scan := scanDepLabelLiterals(r.Attr("deps"), stringSet(rawAllowedCalls))
	matches, _ := matchingDepLabels(pkg, scan.labels, patterns, invalidDepLabelIgnored)

	return depLabelLiteralMatches{
		matches:  matches,
		complete: scan.complete,
	}, nil
}

type depLabelLiteralScan struct {
	labels   []string
	complete bool
}

// scanDepLabelLiterals collects source-spelled label strings from a deps
// expression without evaluating Starlark. Unsupported subexpressions make the
// scan incomplete, but their surrounding literals are still useful evidence for
// source-style policies such as "do not handwrite these labels here".
func scanDepLabelLiterals(expr bzl.Expr, allowedCalls map[string]bool) depLabelLiteralScan {
	if expr == nil {
		return depLabelLiteralScan{complete: true}
	}
	switch typed := expr.(type) {
	case *bzl.StringExpr:
		return depLabelLiteralScan{
			labels:   []string{typed.Value},
			complete: true,
		}
	case *bzl.ListExpr:
		return scanDepLabelLiteralExprs(typed.List, allowedCalls)
	case *bzl.TupleExpr:
		return scanDepLabelLiteralExprs(typed.List, allowedCalls)
	case *bzl.DictExpr:
		return scanDepLabelLiteralDictItems(typed.List, allowedCalls)
	case *bzl.KeyValueExpr:
		key := scanDepLabelLiterals(typed.Key, allowedCalls)
		value := scanDepLabelLiterals(typed.Value, allowedCalls)
		return depLabelLiteralScan{
			labels:   append(key.labels, value.labels...),
			complete: key.complete && value.complete,
		}
	case *bzl.AssignExpr:
		return scanDepLabelLiterals(typed.RHS, allowedCalls)
	case *bzl.BinaryExpr:
		if typed.Op != "+" {
			return depLabelLiteralScan{}
		}
		left := scanDepLabelLiterals(typed.X, allowedCalls)
		right := scanDepLabelLiterals(typed.Y, allowedCalls)
		return depLabelLiteralScan{
			labels:   append(left.labels, right.labels...),
			complete: left.complete && right.complete,
		}
	case *bzl.ParenExpr:
		return scanDepLabelLiterals(typed.X, allowedCalls)
	case *bzl.CallExpr:
		if scan, ok := scanSelectDepLabelLiterals(typed, allowedCalls); ok {
			return scan
		}
		scan := scanDepLabelLiteralExprs(typed.List, allowedCalls)
		if name, ok := callName(typed); ok && allowedCalls[name] {
			scan.complete = true
		} else {
			scan.complete = false
		}
		return scan
	default:
		return depLabelLiteralScan{}
	}
}

func scanDepLabelLiteralExprs(exprs []bzl.Expr, allowedCalls map[string]bool) depLabelLiteralScan {
	out := depLabelLiteralScan{complete: true}
	for _, expr := range exprs {
		scan := scanDepLabelLiterals(expr, allowedCalls)
		out.labels = append(out.labels, scan.labels...)
		out.complete = out.complete && scan.complete
	}
	return out
}

func scanDepLabelLiteralDictItems(items []*bzl.KeyValueExpr, allowedCalls map[string]bool) depLabelLiteralScan {
	out := depLabelLiteralScan{complete: true}
	for _, item := range items {
		scan := scanDepLabelLiterals(item, allowedCalls)
		out.labels = append(out.labels, scan.labels...)
		out.complete = out.complete && scan.complete
	}
	return out
}

func scanSelectDepLabelLiterals(call *bzl.CallExpr, allowedCalls map[string]bool) (depLabelLiteralScan, bool) {
	ident, ok := call.X.(*bzl.Ident)
	if !ok || ident.Name != "select" {
		return depLabelLiteralScan{}, false
	}
	out := depLabelLiteralScan{complete: true}
	for _, arg := range call.List {
		dict, ok := arg.(*bzl.DictExpr)
		if !ok {
			if isSelectNoMatchErrorArg(arg) {
				continue
			}
			out.complete = false
			continue
		}
		for _, item := range dict.List {
			scan := scanDepLabelLiterals(item.Value, allowedCalls)
			out.labels = append(out.labels, scan.labels...)
			out.complete = out.complete && scan.complete
		}
	}
	return out, true
}

func isSelectNoMatchErrorArg(expr bzl.Expr) bool {
	assign, ok := expr.(*bzl.AssignExpr)
	if !ok {
		return false
	}
	ident, ok := assign.LHS.(*bzl.Ident)
	return ok && ident.Name == "no_match_error"
}

func callName(call *bzl.CallExpr) (string, bool) {
	ident, ok := call.X.(*bzl.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
