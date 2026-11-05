package schedgate

import (
	"os"
	"testing"

	"github.com/arketov/iterion/pkg/internal/hostshelltest"
)

// The fixtures here are written in sh; on Windows the host shell would be
// PowerShell, so the binary is pointed at a POSIX shell (see hostshelltest).
func TestMain(m *testing.M) {
	hostshelltest.PreferPosix()
	os.Exit(m.Run())
}
