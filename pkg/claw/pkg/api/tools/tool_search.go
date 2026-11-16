package tools

import (
	"context"

	intl "github.com/arketov/iterion/pkg/claw/internal/tools"
	"github.com/arketov/iterion/pkg/claw/pkg/api"
)

func ToolSearchTool() api.Tool { return intl.ToolSearchTool() }

// ExecuteToolSearch ranks `allTools` against input["query"]. Hosts
// pass the full set of tools registered for the current agent so the
// search has the right haystack.
func ExecuteToolSearch(ctx context.Context, input map[string]any, allTools []api.Tool) (string, error) {
	return intl.ExecuteToolSearch(input, allTools)
}
