// Package hostshelltest is for test binaries whose fixtures are written in
// sh (a hook script, a guard, a tool node's `command:`). On Windows the host
// shell is PowerShell (pkg/internal/hostshell), which reads such a fixture
// differently; these helpers point the shell choice at a POSIX shell for the
// whole binary, or at PowerShell for one test.
package hostshelltest

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/arketov/iterion/pkg/internal/hostshell"
)

// PreferPosix, for a TestMain, makes the host shell a POSIX one when the
// platform default is PowerShell and an operator has not chosen already:
// ITERION_SHELL is set to sh (else bash) when either is on PATH. Where none is
// found nothing changes and the sh fixtures fail loudly, naming the missing
// shell. It does nothing off Windows, so Linux and macOS keep running the
// platform default.
func PreferPosix() {
	if runtime.GOOS != "windows" || os.Getenv(hostshell.EnvVar) != "" {
		return
	}
	for _, name := range []string{"sh", "bash"} {
		if _, err := exec.LookPath(name); err == nil {
			_ = os.Setenv(hostshell.EnvVar, name)
			return
		}
	}
}

// UsePowerShell makes the host shell PowerShell for one test, skipping it
// where neither pwsh nor powershell is on PATH. Not for parallel tests.
func UsePowerShell(t testing.TB) {
	t.Helper()
	// ITERION_TEST_POWERSHELL=powershell runs the PowerShell tests under
	// Windows PowerShell 5.1 instead of pwsh.
	names := []string{"pwsh", "powershell"}
	if only := os.Getenv("ITERION_TEST_POWERSHELL"); only != "" {
		names = []string{only}
	}
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			t.Setenv(hostshell.EnvVar, name)
			return
		}
	}
	t.Skip("PowerShell (pwsh or powershell) is not on PATH")
}
