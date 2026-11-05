package delegate

import (
	"testing"

	"github.com/arketov/iterion/internal/pricingtest"
)

// A fresh host cache must not change the prices used by stand-in backends.
func TestThisPackagePricesFromTheStaticTable(t *testing.T) {
	pricingtest.RequireIsolated(t)
}
