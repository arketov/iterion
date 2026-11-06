package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/arketov/iterion/pkg/store"
)

func TestRunStillExists(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"loaded", nil, true},
		{"not found", store.ErrRunNotFound, false},
		{"not found wrapped", fmt.Errorf("load run: %w", store.ErrRunNotFound), false},
		{"deleted tombstone", store.ErrRunDeleted, false},
		{"deleted wrapped", fmt.Errorf("load run: %w", store.ErrRunDeleted), false},
		{"timeout is not proof of absence", context.DeadlineExceeded, true},
		{"arbitrary store error is not proof of absence", errors.New("connection reset"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runStillExists(c.err); got != c.want {
				t.Fatalf("runStillExists(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
