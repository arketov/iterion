//go:build !windows

package delegate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	iterlog "github.com/arketov/iterion/pkg/log"
)

// These tests spawn fakeClaudeScripted, a #!/bin/sh stand-in CLI that
// Windows cannot run; its helpers live in claude_code_background_test.go,
// which carries the same tag.

// The formatting pass runs with the node's tools (it is the --max-turns
// fallback of a first pass cut off mid-run): what it launches and never sees
// report back is lost with its process, and recorded like pass 1's — secrets
// masked, in the ledger and in what the call hands back.
func TestTheFormattingPassRecordsTheWorkItLost(t *testing.T) {
	const secret = "sk-live-4f9a8b7c6d5e"
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(fake, []byte(fakeClaudeScripted), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script")
	script := []string{
		lnInit("2.1.280"),
		lnAssistant("f1", "", cToolUse("tuB", "Bash", map[string]any{"command": "API_KEY=" + secret + " ./watch", "run_in_background": true})),
		lnSnapshotOf(map[string]any{"task_id": "b9", "task_type": "local_bash", "description": "API_KEY=" + secret + " ./watch"}),
		lnToolResult("tuB", "Command running in background with ID: b9", false, ""),
		lnAssistant("f2", "", cToolUse("tuSO", "StructuredOutput", map[string]any{"answer": "x"})),
		lnToolResult("tuSO", "Structured output provided successfully", false, ""),
		lnResult(resultSpec{text: `{"answer":"x"}`, turns: 2, cost: 0.01, structured: map[string]any{"answer": "x"}}),
	}
	if err := os.WriteFile(scriptPath, []byte(strings.Join(script, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_SCRIPT", scriptPath)
	ledger := NewSessionLedger(map[string][]string{"s1": {"auditor (local_agent, t1)"}})
	b := &ClaudeCodeBackend{Logger: iterlog.Nop()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task := Task{Command: fake, WorkDir: dir, OutputSchema: []byte(`{"type":"object"}`), SessionLedger: ledger,
		RedactSecrets: func(s string) string { return strings.ReplaceAll(s, secret, "__ITERION_SECRET_API_KEY__") }}
	var events []BackgroundWork
	task.Hooks.OnBackgroundWork = func(w BackgroundWork) { events = append(events, w) }
	rm, _, lost, err := b.formatOutput(ctx, task, "s1")
	if err != nil || rm == nil {
		t.Fatalf("formatOutput: rm=%v err=%v", rm, err)
	}
	want := []string{"API_KEY=__ITERION_SECRET_API_KEY__ ./watch (local_bash, b9)"}
	if !reflect.DeepEqual(lost, want) {
		t.Fatalf("lost = %q, want %q", lost, want)
	}
	if got := ledger.Terminated("s1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("s1 = %q, want the told entry settled and the pass's lost work recorded: %q", got, want)
	}
	if len(events) != 1 || events[0].Phase != BackgroundAbandoned || events[0].Backend != BackendClaudeCode || !reflect.DeepEqual(events[0].Tasks, want) {
		t.Fatalf("events = %+v, want one abandoned event naming %q", events, want)
	}
}

// The wiring witness: an armed task's spawn really carries --add-dir, and a
// disarmed one does not. The scripted fake CLI records its argv.
func TestClaudeCodeSpawnCarriesTheSkillChannelRoot(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("POSIX shell fixture")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(fake, []byte(fakeClaudeScripted), 0o755); err != nil {
		t.Fatal(err)
	}
	script := []string{
		lnInit("2.1.280"),
		lnAssistant("m1", "", cText("done")),
		lnResult(resultSpec{text: "done", turns: 1, cost: 0.01, in: 1, out: 1}),
	}
	scriptPath := filepath.Join(dir, "script")
	if err := os.WriteFile(scriptPath, []byte(strings.Join(script, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_SCRIPT", scriptPath)

	channelRoot := filepath.Join(dir, "channel")
	for i, tc := range []struct {
		name string
		root string
		want bool
	}{
		{"an armed task", channelRoot, true},
		{"a disarmed task", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spawnLog := filepath.Join(dir, fmt.Sprintf("spawn-%d.log", i))
			t.Setenv("FAKE_CLAUDE_SPAWN_LOG", spawnLog)
			b := &ClaudeCodeBackend{Logger: iterlog.Nop()}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := b.Execute(ctx, Task{NodeID: "n", Command: fake, WorkDir: dir, UserPrompt: "say ok", SkillChannelRoot: tc.root})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			raw, err := os.ReadFile(spawnLog)
			if err != nil {
				t.Fatalf("the fake CLI recorded nothing: %v", err)
			}
			got := strings.Contains(string(raw), "--add-dir "+tc.root)
			if tc.want && !got {
				t.Errorf("the spawn argv lacks --add-dir %s:\n%s", tc.root, raw)
			}
			if !tc.want && strings.Contains(string(raw), "--add-dir") {
				t.Errorf("a disarmed task still spawned with --add-dir:\n%s", raw)
			}
		})
	}
}
