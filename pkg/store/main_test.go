package store_test

import (
	"os"
	"testing"

	"github.com/arketov/iterion/internal/hometest"
)

func TestMain(m *testing.M) { os.Exit(hometest.Isolate(m.Run)) }
