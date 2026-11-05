package server

import (
	"strings"

	"github.com/arketov/iterion/pkg/dsl/workflowfile"
)

// isSkippedDir returns true for directories that should not be walked or watched.
func isSkippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// isWorkflowFile returns true if the path is a recognised workflow source
// file. See pkg/dsl/workflowfile for the canonical list.
func isWorkflowFile(path string) bool {
	return workflowfile.IsWorkflowFile(path)
}
