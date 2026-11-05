package runview

import (
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

func TestResumeExecutorSpecReplaysPermissionOverride(t *testing.T) {
	svc, err := NewService(t.TempDir(), WithLogger(iterlog.Nop()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	spec := svc.resumeExecutorSpec(&ir.Workflow{Name: "wf", Permission: "off"}, &store.Run{
		ID: "run-1", PermissionOverride: "deny",
	}, iterlog.Nop(), "")
	if spec.Permission != "deny" {
		t.Fatalf("resume Permission = %q, want original run-level deny", spec.Permission)
	}
}
