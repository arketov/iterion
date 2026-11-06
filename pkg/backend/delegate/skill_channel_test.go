package delegate

import (
	"context"
	"os/exec"
	"testing"

	"github.com/arketov/iterion/pkg/sandbox"
)

// The claude_code half of the pinned skill channel (iterion#1719): the
// engine-owned root reaches the CLI as --add-dir — its documented discovery
// for skills outside the working tree — on the host, and stays OUT of a
// container spawn, whose workspace is not this process's filesystem (the
// run-owned bind mount is #1730's admission-half design).
func TestSkillChannelAddDir(t *testing.T) {
	root := t.TempDir()
	if got, ok := skillChannelAddDir(Task{}); ok || got != "" {
		t.Errorf("no channel root = (%q, %v), want none", got, ok)
	}
	if got, ok := skillChannelAddDir(Task{SkillChannelRoot: root}); !ok || got != root {
		t.Errorf("a host run = (%q, %v), want the root", got, ok)
	}
	// The noop driver is a host passthrough: the channel reaches it, through
	// the same Task.Hostless() reading every other caller makes.
	if got, ok := skillChannelAddDir(Task{SkillChannelRoot: root, Sandbox: fakeSandboxRun{driver: "noop"}}); !ok || got != root {
		t.Errorf("a noop-driver run = (%q, %v), want the root", got, ok)
	}
	if got, ok := skillChannelAddDir(Task{SkillChannelRoot: root, Sandbox: fakeSandboxRun{driver: "docker"}}); ok || got != "" {
		t.Errorf("a container run = (%q, %v), want the channel withheld", got, ok)
	}
}

// fakeSandboxRun is the sandbox.Run surface skillChannelAddDir reads: the
// driver name.
type fakeSandboxRun struct{ driver string }

func (f fakeSandboxRun) Driver() string { return f.driver }

func (f fakeSandboxRun) Command(context.Context, []string, sandbox.ExecOpts) *exec.Cmd {
	return nil
}

func (f fakeSandboxRun) Exec(context.Context, []string, sandbox.ExecOpts) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, nil
}

func (f fakeSandboxRun) Cleanup(context.Context) error { return nil }
