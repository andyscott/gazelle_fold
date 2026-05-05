package policy

import (
	"fmt"
	"strings"

	"go.starlark.net/syntax"
)

type directiveKind int

const (
	directiveImport directiveKind = iota
	directiveUse
	directiveExempt
)

type parsedDirective struct {
	Kind   directiveKind
	Name   string
	Label  string
	Scope  packageScope
	Params map[string]any
	Reason string
}

func parseDirective(value string) (parsedDirective, error) {
	// The directive language deliberately exposes import(...), but Starlark
	// reserves "import" as a keyword. Rewrite only that leading command name
	// before parsing so users keep the intended public syntax without turning
	// the whole directive surface into a bespoke parser.
	parseValue := value
	if strings.HasPrefix(strings.TrimSpace(value), "import(") {
		parseValue = strings.Replace(value, "import(", "policy_import(", 1)
	}
	expr, err := syntax.ParseExpr("gazelle:policy", parseValue, 0)
	if err != nil {
		return parsedDirective{}, err
	}
	call, ok := expr.(*syntax.CallExpr)
	if !ok {
		return parsedDirective{}, fmt.Errorf("policy directive must be one function call")
	}
	fn, ok := call.Fn.(*syntax.Ident)
	if !ok {
		return parsedDirective{}, fmt.Errorf("policy directive must call a bare function")
	}

	positional, kwargs, err := splitArgs(call.Args)
	if err != nil {
		return parsedDirective{}, err
	}

	switch fn.Name {
	case "policy_import":
		if len(positional) != 1 || len(kwargs) != 0 {
			return parsedDirective{}, fmt.Errorf("import expects exactly one label")
		}
		label, ok := positional[0].(string)
		if !ok {
			return parsedDirective{}, fmt.Errorf("import label must be a string")
		}
		return parsedDirective{Kind: directiveImport, Label: label}, nil
	case "use":
		if len(positional) != 1 {
			return parsedDirective{}, fmt.Errorf("use expects exactly one positional policy name")
		}
		name, ok := positional[0].(string)
		if !ok {
			return parsedDirective{}, fmt.Errorf("use policy name must be a string")
		}
		rawScope, ok := kwargs["scope"].(string)
		if !ok {
			return parsedDirective{}, fmt.Errorf("use requires string scope")
		}
		scope, err := parseScope(rawScope)
		if err != nil {
			return parsedDirective{}, err
		}
		delete(kwargs, "scope")
		return parsedDirective{
			Kind:   directiveUse,
			Name:   name,
			Scope:  scope,
			Params: kwargs,
		}, nil
	case "exempt":
		if len(positional) != 1 {
			return parsedDirective{}, fmt.Errorf("exempt expects exactly one positional policy name")
		}
		name, ok := positional[0].(string)
		if !ok {
			return parsedDirective{}, fmt.Errorf("exempt policy name must be a string")
		}
		reason, ok := kwargs["reason"].(string)
		if !ok || reason == "" {
			return parsedDirective{}, fmt.Errorf("exempt requires a non-empty string reason")
		}
		if len(kwargs) != 1 {
			return parsedDirective{}, fmt.Errorf("exempt only accepts reason")
		}
		return parsedDirective{
			Kind:   directiveExempt,
			Name:   name,
			Reason: reason,
		}, nil
	default:
		return parsedDirective{}, fmt.Errorf("unknown policy directive %q", fn.Name)
	}
}

func splitArgs(args []syntax.Expr) ([]any, map[string]any, error) {
	var positional []any
	kwargs := make(map[string]any)
	for _, arg := range args {
		if kw, ok := arg.(*syntax.BinaryExpr); ok && kw.Op == syntax.EQ {
			name, ok := kw.X.(*syntax.Ident)
			if !ok {
				return nil, nil, fmt.Errorf("keyword argument must use an identifier")
			}
			if _, exists := kwargs[name.Name]; exists {
				return nil, nil, fmt.Errorf("duplicate keyword argument %q", name.Name)
			}
			value, err := literalValue(kw.Y)
			if err != nil {
				return nil, nil, err
			}
			kwargs[name.Name] = value
			continue
		}
		value, err := literalValue(arg)
		if err != nil {
			return nil, nil, err
		}
		positional = append(positional, value)
	}
	return positional, kwargs, nil
}

func literalValue(expr syntax.Expr) (any, error) {
	switch e := expr.(type) {
	case *syntax.Literal:
		switch value := e.Value.(type) {
		case string:
			return value, nil
		case int64:
			return value, nil
		default:
			return nil, fmt.Errorf("unsupported literal %T", e.Value)
		}
	case *syntax.Ident:
		switch e.Name {
		case "True":
			return true, nil
		case "False":
			return false, nil
		case "None":
			return nil, nil
		default:
			return nil, fmt.Errorf("unsupported identifier %q", e.Name)
		}
	case *syntax.ListExpr:
		values := make([]string, 0, len(e.List))
		for _, item := range e.List {
			value, ok := item.(*syntax.Literal)
			if !ok {
				return nil, fmt.Errorf("only string lists are supported in policy directives")
			}
			str, ok := value.Value.(string)
			if !ok {
				return nil, fmt.Errorf("only string lists are supported in policy directives")
			}
			values = append(values, str)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported directive expression %T", expr)
	}
}
