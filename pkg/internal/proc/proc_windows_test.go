//go:build windows

package proc

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	treeHelperEnv   = "ITERION_PROC_TREE_HELPER"
	treePidFileEnv  = "ITERION_PROC_TREE_PIDFILE"
	treeHelperSleep = 30 * time.Second
)

// TestProcTreeHelper is the body of the helper processes the tests below
// re-exec the test binary into. It returns at once in a normal test run.
func TestProcTreeHelper(t *testing.T) {
	role := os.Getenv(treeHelperEnv)
	if role == "" {
		return
	}
	switch role {
	case "parent", "orphaner":
		gc := exec.Command(os.Args[0], "-test.run=^TestProcTreeHelper$")
		gc.Env = append(os.Environ(), treeHelperEnv+"=grandchild")
		if role == "parent" {
			gc.Stdout, gc.Stderr = os.Stdout, os.Stderr
		}
		if err := gc.Start(); err != nil {
			os.Exit(3)
		}
		pidFile := os.Getenv(treePidFileEnv)
		if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(gc.Process.Pid)), 0o600); err != nil {
			os.Exit(4)
		}
		if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
			os.Exit(4)
		}
		if role == "orphaner" {
			os.Exit(0)
		}
		time.Sleep(treeHelperSleep)
	case "grandchild":
		time.Sleep(treeHelperSleep)
	}
}

func helperCmd(ctx context.Context, role, pidFile string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcTreeHelper$")
	cmd.Env = append(os.Environ(), treeHelperEnv+"="+role, treePidFileEnv+"="+pidFile)
	return cmd
}

func readPid(t *testing.T, file string) uint32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
				return uint32(n)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild pid file %s never appeared", file)
	return 0
}

// pin opens the grandchild so its pid cannot be recycled, and terminates it
// at cleanup so a failing run leaks nothing.
func pin(t *testing.T, pid uint32) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		t.Fatalf("open grandchild %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	})
	return h
}

func signalled(h windows.Handle, wait time.Duration) bool {
	ev, err := windows.WaitForSingleObject(h, uint32(wait.Milliseconds()))
	return err == nil && ev == windows.WAIT_OBJECT_0
}

// TestTerminateGroupOnCancelReachesTheGrandchild: cancelling a command
// whose child started a grandchild sharing the output pipe ends both, and
// Run returns instead of waiting on the pipe. Mutation: drop the
// killDescendants call from the Cancel closure and Run hangs while the
// grandchild lives.
func TestTerminateGroupOnCancelReachesTheGrandchild(t *testing.T) {
	pidFile := t.TempDir() + `\gc.pid`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := helperCmd(ctx, "parent", pidFile)
	TerminateGroupOnCancel(cmd)
	cmd.Stdout = &bytes.Buffer{}

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	h := pin(t, readPid(t, pidFile))
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel: the grandchild kept the pipe")
	}
	if !signalled(h, 5*time.Second) {
		t.Fatal("grandchild survived the cancellation")
	}
}

// TestKillProcessGroupKillsWhatTheReapedCommandLeft: a command that exited
// after starting a grandchild leaves it behind; KillProcessGroup finds it
// through the reaped root's recorded lifetime. Mutation: make
// KillProcessGroup return (false, nil) and the grandchild lives.
func TestKillProcessGroupKillsWhatTheReapedCommandLeft(t *testing.T) {
	pidFile := t.TempDir() + `\gc.pid`
	cmd := helperCmd(context.Background(), "orphaner", pidFile)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	h := pin(t, readPid(t, pidFile))
	if signalled(h, 0) {
		t.Fatal("precondition: the grandchild must outlive its parent")
	}

	killed, err := KillProcessGroup(cmd)
	if err != nil || !killed {
		t.Fatalf("KillProcessGroup = (%v, %v), want (true, nil)", killed, err)
	}
	if !signalled(h, 5*time.Second) {
		t.Fatal("grandchild survived KillProcessGroup")
	}
}

// TestKillProcessGroupAnswersFalseWhenNothingToKill: no shape without a
// leftover reports a kill. Mutation: return true unconditionally.
func TestKillProcessGroupAnswersFalseWhenNothingToKill(t *testing.T) {
	check := func(name string, cmd *exec.Cmd) {
		t.Helper()
		if killed, err := KillProcessGroup(cmd); killed || err != nil {
			t.Errorf("%s: KillProcessGroup = (%v, %v), want (false, nil)", name, killed, err)
		}
	}
	check("nil", nil)
	check("unstarted", exec.Command("cmd", "/c", "exit 0"))

	running := exec.Command("cmd", "/c", "exit 0")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	check("not waited", running)
	if err := running.Wait(); err != nil {
		t.Fatal(err)
	}

	reaped := exec.Command("cmd", "/c", "exit 0")
	if err := reaped.Run(); err != nil {
		t.Fatal(err)
	}
	check("reaped, nothing left", reaped)
}

// TestTerminateGroupOnCancelLeavesASuccessfulRunAlone: arming the hook does
// not change a normal run's output or status. Mutation: make the Cancel
// closure kill unconditionally at Start.
func TestTerminateGroupOnCancelLeavesASuccessfulRunAlone(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "echo ok")
	TerminateGroupOnCancel(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "ok" {
		t.Fatalf("output = %q, want ok", got)
	}
}
