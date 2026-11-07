//go:build desktop

package main

import (
	"reflect"
	"testing"
)

// GetOpenConnections lists the open connections in the order they were
// opened — not in Go's randomised map order — and a re-open (or sign-in
// replacement) of an open connection keeps its original position.
func TestGetOpenConnections_ReturnsOpeningOrder(t *testing.T) {
	app := &App{}
	app.mu.Lock()
	for _, id := range []string{"zeta", "alpha", "mid"} {
		app.registerConnLocked(&activeConn{id: id, kind: ProjectKindLocal, serverURL: "http://127.0.0.1:1/"})
	}
	// Re-opening "alpha" replaces its entry but must not move its tab.
	app.registerConnLocked(&activeConn{id: "alpha", kind: ProjectKindLocal, serverURL: "http://127.0.0.1:2/"})
	app.mu.Unlock()

	want := []string{"zeta", "alpha", "mid"}
	for i := 0; i < 50; i++ {
		if got := app.GetOpenConnections(); !reflect.DeepEqual(got, want) {
			t.Fatalf("GetOpenConnections() = %v, want %v (opening order)", got, want)
		}
	}
}

// A connection closed and opened again is a new tab at the end.
func TestGetOpenConnections_ReopenAfterCloseGoesLast(t *testing.T) {
	app := &App{}
	app.mu.Lock()
	for _, id := range []string{"b", "a", "c"} {
		app.registerConnLocked(&activeConn{id: id, kind: ProjectKindLocal, serverURL: "http://127.0.0.1:1/"})
	}
	app.mu.Unlock()
	if err := app.CloseConnection("b"); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
	app.mu.Lock()
	app.registerConnLocked(&activeConn{id: "b", kind: ProjectKindLocal, serverURL: "http://127.0.0.1:1/"})
	app.mu.Unlock()

	want := []string{"a", "c", "b"}
	if got := app.GetOpenConnections(); !reflect.DeepEqual(got, want) {
		t.Fatalf("GetOpenConnections() = %v, want %v", got, want)
	}
}
