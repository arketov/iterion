package store_test

import (
	"github.com/arketov/iterion/pkg/store"
	"github.com/arketov/iterion/pkg/store/storetest"
	"testing"
)

func TestAwaitAnswersWaitConformance(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storetest.RunAwaitAnswersWaitConformance(t, s)
}
