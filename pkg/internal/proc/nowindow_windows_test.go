//go:build windows

package proc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

const (
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	probeEnv              = "ITERION_PROC_CONSOLE_PROBE"
)

// TestHideWindow_AddsCreateNoWindowAndKeepsTheRest pins what HideWindow
// writes: CREATE_NO_WINDOW is OR-ed in next to flags and fields the caller
// already set, so a detached runner that wants its own process group keeps it.
func TestHideWindow_AddsCreateNoWindowAndKeepsTheRest(t *testing.T) {
	forceHideNeeded(t, true)
	t.Run("nil SysProcAttr is created", func(t *testing.T) {
		cmd := exec.Command("cmd", "/C", "exit 0")
		HideWindow(cmd)
		if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags != createNoWindow {
			t.Fatalf("CreationFlags = %#x, want exactly CREATE_NO_WINDOW %#x", flagsOf(cmd), createNoWindow)
		}
	})
	t.Run("existing flags and fields survive", func(t *testing.T) {
		cmd := exec.Command("cmd", "/C", "exit 0")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup, NoInheritHandles: true}
		HideWindow(cmd)
		if got, want := flagsOf(cmd), uint32(createNewProcessGroup|createNoWindow); got != want {
			t.Fatalf("CreationFlags = %#x, want %#x", got, want)
		}
		if !cmd.SysProcAttr.NoInheritHandles {
			t.Fatal("HideWindow dropped a SysProcAttr field the caller set")
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		cmd := exec.Command("cmd", "/C", "exit 0")
		HideWindow(cmd)
		HideWindow(cmd)
		if got := flagsOf(cmd); got != createNoWindow {
			t.Fatalf("CreationFlags = %#x after two calls, want %#x", got, createNoWindow)
		}
	})
	t.Run("does not set the STARTF_USESHOWWINDOW HideWindow field", func(t *testing.T) {
		// SW_HIDE would also hide the first window of a GUI program a caller
		// meant to show.
		cmd := exec.Command("cmd", "/C", "exit 0")
		HideWindow(cmd)
		if cmd.SysProcAttr.HideWindow {
			t.Fatal("HideWindow set SysProcAttr.HideWindow; only CREATE_NO_WINDOW is wanted")
		}
	})
	t.Run("an explicit console request is left alone", func(t *testing.T) {
		for _, flag := range []uint32{detachedProcess, createNewConsole} {
			cmd := exec.Command("cmd", "/C", "exit 0")
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flag}
			HideWindow(cmd)
			if got := flagsOf(cmd); got != flag {
				t.Fatalf("flags %#x became %#x: CREATE_NO_WINDOW is ignored next to them and must not be mixed in", flag, got)
			}
		}
	})
	t.Run("nil cmd is a no-op", func(t *testing.T) { HideWindow(nil) })
}

func TestCommand_HidesTheWindowByDefault(t *testing.T) {
	forceHideNeeded(t, true)
	if got := flagsOf(Command("cmd", "/C", "exit 0")); got&createNoWindow == 0 {
		t.Fatalf("Command CreationFlags = %#x, want CREATE_NO_WINDOW set", got)
	}
	if got := flagsOf(CommandContext(context.Background(), "cmd", "/C", "exit 0")); got&createNoWindow == 0 {
		t.Fatalf("CommandContext CreationFlags = %#x, want CREATE_NO_WINDOW set", got)
	}
	if HiddenWindowFlags != createNoWindow {
		t.Fatalf("HiddenWindowFlags = %#x, want %#x", HiddenWindowFlags, createNoWindow)
	}
}

// TestCommand_KeepsPipesAndExitCodes: the flag must not change anything a
// caller observes — stdout through a pipe, stderr, the exit code.
func TestCommand_KeepsPipesAndExitCodes(t *testing.T) {
	forceHideNeeded(t, true)
	cmd := Command("cmd", "/C", "echo out& echo err 1>&2& exit 7")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("Run error = %v (%T), want *exec.ExitError carrying exit code 7", err, err)
	}
	if ee.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", ee.ExitCode())
	}
	if !strings.Contains(stdout.String(), "out") || !strings.Contains(stderr.String(), "err") {
		t.Fatalf("pipes lost output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// TestCommand_ChildHasAConsoleButNoWindow is the behavioural proof: a child
// started through Command has a console to hand down to ITS children (so a
// grandchild opens no window of its own) and that console has no window.
// A child started without the flag from a console-less parent would have no
// console at all, and each of its console children would open a visible one.
func TestCommand_ChildHasAConsoleButNoWindow(t *testing.T) {
	forceHideNeeded(t, true)
	cmd := Command(os.Args[0], "-test.run=^TestConsoleProbeHelper$")
	cmd.Env = append(os.Environ(), probeEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe child failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "probe: console=true window=0") {
		t.Fatalf("child console state = %q, want an attached console with no window (\"probe: console=true window=0\")", got)
	}
}

// TestConsoleProbeHelper is the child half of the test above; it does nothing
// in a normal run.
func TestConsoleProbeHelper(t *testing.T) {
	if os.Getenv(probeEnv) == "" {
		t.Skip("helper process for TestCommand_ChildHasAConsoleButNoWindow")
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	var pids [4]uint32
	n, _, _ := k32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
	fmt.Printf("probe: console=%v window=%d\n", n > 0, hwnd)
}

// forceHideNeeded pins the "does this process need the flag" decision, which in
// a real run depends on whether `go test` was started from a terminal.
func forceHideNeeded(t *testing.T, v bool) {
	t.Helper()
	old := hideNeeded
	hideNeeded = func() bool { return v }
	t.Cleanup(func() { hideNeeded = old })
}

// TestHideWindow_SkippedWhenTheProcessHasAConsole: with a console to inherit
// (a terminal, or a hidden one) a fresh hidden console would only cut the
// child off from Ctrl+C, so the flag stays off.
func TestHideWindow_SkippedWhenTheProcessHasAConsole(t *testing.T) {
	forceHideNeeded(t, false)
	cmd := Command("cmd", "/C", "exit 0")
	if cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %+v, want untouched when no hiding is needed", cmd.SysProcAttr)
	}
}

// TestDefaultHideNeeded_EnvOverride pins ITERION_CHILD_WINDOWS: "hide" forces
// the flag, "show" forbids it, anything else asks whether we have a console.
func TestDefaultHideNeeded_EnvOverride(t *testing.T) {
	t.Setenv(ChildWindowsEnv, "hide")
	if !defaultHideNeeded() {
		t.Error("hide: want true")
	}
	t.Setenv(ChildWindowsEnv, " SHOW ")
	if defaultHideNeeded() {
		t.Error("show: want false")
	}
	for _, v := range []string{"", "auto", "garbage"} {
		t.Setenv(ChildWindowsEnv, v)
		if got, want := defaultHideNeeded(), !HasConsole(); got != want {
			t.Errorf("%q: defaultHideNeeded = %v, want !HasConsole() = %v", v, got, want)
		}
	}
}

// TestEnsureHiddenConsole_OnlyWhenThereIsNone: it allocates exactly when the
// process has no console, and never when the operator said "show".
func TestEnsureHiddenConsole_OnlyWhenThereIsNone(t *testing.T) {
	calls := 0
	old := allocConsoleAndHideFunc
	allocConsoleAndHideFunc = func() bool { calls++; return true }
	t.Cleanup(func() { allocConsoleAndHideFunc = old })

	t.Setenv(ChildWindowsEnv, "show")
	if EnsureHiddenConsole() || calls != 0 {
		t.Fatalf("show: EnsureHiddenConsole allocated (calls=%d)", calls)
	}

	t.Setenv(ChildWindowsEnv, "")
	got := EnsureHiddenConsole()
	if HasConsole() {
		if got || calls != 0 {
			t.Fatalf("console present: EnsureHiddenConsole = %v calls=%d, want false/0", got, calls)
		}
	} else if !got || calls != 1 {
		t.Fatalf("no console: EnsureHiddenConsole = %v calls=%d, want true/1", got, calls)
	}
}

func flagsOf(cmd *exec.Cmd) uint32 {
	if cmd.SysProcAttr == nil {
		return 0
	}
	return cmd.SysProcAttr.CreationFlags
}
