package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchWindowMinimised_EmitsOnlyOnChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var minimised atomic.Bool
	emitted := make(chan bool, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWindowMinimised(ctx, time.Millisecond, minimised.Load, func(v bool) { emitted <- v })
	}()

	expect := func(want bool) {
		t.Helper()
		select {
		case got := <-emitted:
			if got != want {
				t.Fatalf("emitted %v, want %v", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no event, want %v", want)
		}
	}
	noEvent := func() {
		t.Helper()
		select {
		case got := <-emitted:
			t.Fatalf("unexpected event %v", got)
		case <-time.After(30 * time.Millisecond):
		}
	}

	// Not minimised at the start: nothing is emitted.
	noEvent()

	// Minimised for many ticks: one event, not one per tick.
	minimised.Store(true)
	expect(true)
	noEvent()

	// Back to normal: one event again.
	minimised.Store(false)
	expect(false)
	noEvent()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchWindowMinimised did not return after the context was cancelled")
	}
}
