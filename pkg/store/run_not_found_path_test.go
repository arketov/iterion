package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// The not-found error is relayed in HTTP bodies; it must not carry the
// absolute path of the user's store.
func TestLoadRunNotFoundDoesNotLeakStorePath(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = s.LoadRun(context.Background(), "nope-run-id")
	if !errors.Is(err, ErrRunNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrRunNotFound + os.ErrNotExist, got %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "run.json") {
		t.Fatalf("error leaks the store path: %q", err.Error())
	}
}
