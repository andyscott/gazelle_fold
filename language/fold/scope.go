package fold

import (
	"fmt"
	"path"
	"strings"
)

type scopeKind int

const (
	scopeHere scopeKind = iota
	scopeSubtree
	scopePackage
	scopePackageSubtree
)

type packageScope struct {
	raw  string
	kind scopeKind
	path string
}

func parseScope(raw string) (packageScope, error) {
	switch raw {
	case ".":
		return packageScope{raw: raw, kind: scopeHere}, nil
	case "...":
		return packageScope{raw: raw, kind: scopeSubtree}, nil
	}
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") {
		return packageScope{}, fmt.Errorf("invalid fold scope %q", raw)
	}
	if strings.HasSuffix(raw, "/...") {
		p := strings.TrimSuffix(raw, "/...")
		if !validRelativePackage(p) {
			return packageScope{}, fmt.Errorf("invalid fold scope %q", raw)
		}
		return packageScope{raw: raw, kind: scopePackageSubtree, path: p}, nil
	}
	if !validRelativePackage(raw) {
		return packageScope{}, fmt.Errorf("invalid fold scope %q", raw)
	}
	return packageScope{raw: raw, kind: scopePackage, path: raw}, nil
}

func validRelativePackage(p string) bool {
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return path.Clean(p) == p
}

func (s packageScope) covers(origin, target string) bool {
	rel, ok := relativeDescendant(origin, target)
	if !ok {
		return false
	}
	switch s.kind {
	case scopeHere:
		return rel == "."
	case scopeSubtree:
		return true
	case scopePackage:
		return rel == s.path
	case scopePackageSubtree:
		return rel == s.path || strings.HasPrefix(rel, s.path+"/")
	default:
		return false
	}
}

func relativeDescendant(origin, target string) (string, bool) {
	if origin == target {
		return ".", true
	}
	if origin == "" {
		return target, target != ""
	}
	if strings.HasPrefix(target, origin+"/") {
		return strings.TrimPrefix(target, origin+"/"), true
	}
	return "", false
}

func packageDepth(rel string) int {
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}
