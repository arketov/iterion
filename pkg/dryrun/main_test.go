package dryrun

import (
	"os"
	"testing"

	"github.com/arketov/iterion/internal/pricingtest"
	"github.com/arketov/iterion/pkg/internal/hostshelltest"
)

// The fixtures here are written in sh; on Windows the host shell would be
// PowerShell, so the binary is pointed at a POSIX shell (see hostshelltest).
// The test binary also links a live model-pricing tier: isolate it from the
// host's cache and the network (#3257).
func TestMain(m *testing.M) {
	hostshelltest.PreferPosix()
	os.Exit(pricingtest.Run(m.Run))
}
