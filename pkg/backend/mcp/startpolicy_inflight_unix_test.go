//go:build unix

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A start is slow — a process spawn, a dial, a handshake — and the run's
// sandbox settles while it is in flight. Checking the policy only on the way
// IN therefore protects nothing at the one moment that matters: the launcher
// learns it may not run this server, and the server is already coming up.
//
// What used to happen: the manager closed a session that did not exist yet
// (nil), dropped its only reference to the client, and the start then
// published a live session into it. The refused server's process ran beside
// the launcher for the rest of the run and past it, reachable by nobody —
// Manager.Close could not see it either.
func TestTighteningWhileAStartIsInFlightLeavesNoProcessBehind(t *testing.T) {
	if slowHelperMode() {
		runSlowStdioHelper()
		return
	}
	pidfile := filepath.Join(t.TempDir(), "pid")
	cfg := &ServerConfig{
		Name: "slow", Origin: OriginProject, Transport: TransportStdio,
		Command: os.Args[0],
		Args: []string{"-test.run=TestTighteningWhileAStartIsInFlightLeavesNoProcessBehind",
			"--", "mcp-slow-helper"},
		Env: map[string]string{"ITERION_TEST_MCP_PIDFILE": pidfile},
	}
	m := NewManager(map[string]*ServerConfig{"slow": cfg}, WithStartPolicy(StartAllServers))
	t.Cleanup(func() { _ = m.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := m.ListResources(context.Background(), "slow")
		done <- err
	}()

	// Wait until the child is actually up, so the tightening lands INSIDE the
	// handshake rather than before the spawn (which the entry gate already
	// covers, and which would make this test pass for the wrong reason).
	pid := waitForPID(t, pidfile)
	m.SetStartPolicy(StartOperatorServersOnly)

	err := <-done
	if !ServerNotStartable(err) {
		t.Fatalf("the call must be refused once the policy tightened, got %v", err)
	}
	if alive, why := processAliveWithin(pid, 5*time.Second); alive {
		t.Errorf("pid %d (origin %s) is still running beside the launcher after the refusal: %s",
			pid, cfg.Origin, why)
	}

	state, stateErr := m.state("slow")
	if stateErr != nil {
		t.Fatalf("state: %v", stateErr)
	}
	state.mu.Lock()
	client := state.client
	state.mu.Unlock()
	if client != nil {
		t.Error("a refused server must not be left with a usable client")
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper server never reported its pid — the premise of this test is gone")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// processAliveWithin polls for the process to disappear, so a slow exit does
// not read as a leak. It reports whether the process is still there and, when
// it is, why we know.
//
// Only ESRCH is "gone". EPERM says the opposite — the process EXISTS and this
// uid may not signal it — and reading it as gone is how the leak this test
// guards would report as absent: a process running beside the launcher,
// reachable by nobody, is exactly the state that answers EPERM once its pid
// belongs to someone else. Any other errno is unexpected and says so rather
// than being folded into either answer.
func processAliveWithin(pid int, within time.Duration) (alive bool, why string) {
	deadline := time.Now().Add(within)
	for {
		err := syscall.Kill(pid, 0)
		switch {
		case err == nil:
			if time.Now().After(deadline) {
				return true, "signalable"
			}
		case errors.Is(err, syscall.ESRCH):
			return false, ""
		case errors.Is(err, syscall.EPERM):
			return true, "alive but owned by another uid (EPERM) — the pid was recycled, or the child outlived us"
		default:
			return true, "kill(pid, 0): " + err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
}
