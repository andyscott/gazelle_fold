package fold

import (
	"embed"
	"fmt"
	"path"
)

//go:embed builtins/folds/*.star builtins/policies/*.star builtins/rewrites/*.star
var builtinModuleFS embed.FS

func builtinModuleSource(modulePath string) ([]byte, error) {
	filename := path.Join("builtins", modulePath)
	src, err := builtinModuleFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading std module %s: %w", modulePath, err)
	}
	return src, nil
}
