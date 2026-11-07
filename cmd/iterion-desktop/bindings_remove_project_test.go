//go:build desktop

package main

import (
	"testing"
	"time"
)

// TestRemoveProject_ReadsConfigUnderAppLock pins that RemoveProject reads
// the project snapshot inside the App.mu critical section. The test holds
// Config.mu so the first Config access blocks: when it is already inside
// a.mu (fixed), the goroutine owns a.mu while it waits; when the snapshot
// is taken before a.mu.Lock (bug), it waits on Config.mu holding nothing.
func TestRemoveProject_ReadsConfigUnderAppLock(t *testing.T) {
	app := &App{config: &Config{}}

	app.config.mu.Lock()
	cfgHeld := true
	defer func() {
		if cfgHeld {
			app.config.mu.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() { done <- app.RemoveProject("missing") }()

	deadline := time.Now().Add(2 * time.Second)
	ownsAppLock := false
	for time.Now().Before(deadline) {
		if app.mu.TryLock() {
			app.mu.Unlock()
			time.Sleep(time.Millisecond)
			continue
		}
		ownsAppLock = true
		break
	}

	app.config.mu.Unlock()
	cfgHeld = false
	if err := <-done; err == nil {
		t.Fatal("RemoveProject of an unknown id must fail")
	}
	if !ownsAppLock {
		t.Fatal("RemoveProject read the config before taking App.mu")
	}
}
