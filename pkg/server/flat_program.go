package server

import (
	"fmt"

	"github.com/arketov/iterion/pkg/dsl/ast"
	"github.com/arketov/iterion/pkg/dsl/ir"
	"github.com/arketov/iterion/pkg/dsl/unparse"
)

// flatProgram writes a merged unit out as one program — its {{include}}
// markers inlined, the text verified to read back as the unit it came
// from: what launches inline or is saved as a new file must be the program
// the unit is, not a writer's approximation of it.
func flatProgram(name string, merged *ast.File) (string, error) {
	if err := ir.InlinePromptIncludes(merged); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	flat := unparse.Unparse(merged)
	if err := unparse.Verify(merged, flat); err != nil {
		return "", fmt.Errorf("%s: the flat program does not read back as the unit: %w", name, err)
	}
	return flat, nil
}
