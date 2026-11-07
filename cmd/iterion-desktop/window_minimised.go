package main

import (
	"context"
	"time"
)

// windowMinimisedPollInterval is how often the minimised state is sampled.
// Wails v2 exposes no minimise callback and does not hide WebView2 on
// minimise, so the state is polled; the probe is a single GetWindowLong.
const windowMinimisedPollInterval = time.Second

// watchWindowMinimised samples probe every interval and calls emit only when
// the state changes. The state starts as "not minimised", so nothing is
// emitted until the window is first seen minimised. It returns when ctx ends.
func watchWindowMinimised(ctx context.Context, interval time.Duration, probe func() bool, emit func(bool)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if now := probe(); now != last {
				last = now
				emit(now)
			}
		}
	}
}
