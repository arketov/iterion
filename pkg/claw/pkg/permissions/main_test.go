package permissions_test

import (
	"os"
	"testing"

	"github.com/arketov/iterion/pkg/claw/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateLiveRegistry(m.Run))
}
