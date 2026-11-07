package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/arketov/iterion/pkg/cli"
	"github.com/arketov/iterion/pkg/retrypolicy"
	"github.com/arketov/iterion/pkg/store"
)

// First attempt fails with a needle-free error; the resume (no recovery
// dispatcher on `iterion resume`) meets an HTTP-client wording in stderr.
func TestR3ResumeSurfaceNeedles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("[platform] the tool command is POSIX sh with a marker path; the default shell on a Windows host is PowerShell")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	marker := filepath.Join(t.TempDir(), "second")
	bot := `dsl: 2

schema out:
  ok: bool

tool call_api:
  command: "if [ -f ` + marker + ` ]; then echo 'AxiosError: Request failed with status code 400' >&2; else echo 'first attempt broke' >&2; fi; exit 1"
  output: out

workflow api:
  worktree: none
  sandbox: none
  entry: call_api
  call_api -> done
`
	path := r3WriteBot(t, bot)
	storeDir := t.TempDir()
	p := &cli.Printer{W: io.Discard, Format: cli.OutputJSON}
	_ = cli.RunRun(context.Background(), cli.RunOptions{File: path, StoreDir: storeDir, RunID: "rn", NoInteractive: true, MergeInto: "none"}, p)
	st, err := store.New(storeDir)
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}
	r, err := st.LoadRun(context.Background(), "rn")
	if err != nil || r == nil {
		t.Fatalf("the first run was never recorded: run=%v err=%v", r, err)
	}
	t.Logf("run:    status=%s code=%s", r.Status, r.FailureCode)
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = cli.RunResumeWithFile(context.Background(), path, cli.ResumeOptions{RunID: "rn", StoreDir: storeDir, MergeInto: "none"}, p)
	r, err = st.LoadRun(context.Background(), "rn")
	if err != nil || r == nil {
		t.Fatalf("the resumed run is unreadable: run=%v err=%v", r, err)
	}
	if r.FailureCode != store.FailureExecutionFailed || !retrypolicy.AutoResumable(r.FailureCode) {
		t.Errorf("resume reads tool stderr as provider: %+v", r)
	}
	t.Logf("resume: status=%s code=%s autoResumable=%v deterministic=%v", r.Status, r.FailureCode, retrypolicy.AutoResumable(r.FailureCode), retrypolicy.IsDeterministic(r.FailureCode))
}
