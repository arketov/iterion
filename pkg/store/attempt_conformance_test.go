package store_test

import (
	"github.com/arketov/iterion/pkg/store"
	"github.com/arketov/iterion/pkg/store/storetest"
	"testing"
)

func TestAttemptSnapshotsFilesystem(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storetest.AttemptSnapshots(t, st)
}
