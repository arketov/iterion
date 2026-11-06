package server

import (
	"testing"

	"github.com/arketov/iterion/pkg/backend/modelspecs"
	"github.com/arketov/iterion/pkg/dsl/ir"
)

func TestValidate_UnknownModelIssue(t *testing.T) {
	t.Cleanup(modelspecs.SetDefault(modelspecs.NewSeeded(nil)))
	wf := &ir.Workflow{Nodes: map[string]ir.Node{
		"ask": &ir.AgentNode{BaseNode: ir.BaseNode{ID: "ask"}, LLMFields: ir.LLMFields{Model: "anthropic/claude-sonnet-9-9"}},
	}}
	for name, cfg := range map[string]Config{
		"cloud": {Mode: "cloud"},
		"local": {WorkDir: t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			resp := validateResponse{Valid: true}
			(&Server{cfg: cfg}).appendUnknownModelIssues(&resp, wf)

			if len(resp.Issues) != 1 || len(resp.Warnings) != 1 {
				t.Fatalf("issues %+v, warnings %v: want one of each", resp.Issues, resp.Warnings)
			}
			if resp.Issues[0].Code != string(ir.DiagUnknownModel) || resp.Issues[0].Severity != "warning" {
				t.Errorf("bad issue: %+v", resp.Issues[0])
			}
			if !resp.Valid {
				t.Error("a warning must not invalidate the document")
			}
		})
	}
}
