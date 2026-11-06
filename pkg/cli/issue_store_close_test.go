package cli

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// liveNativeStoreGoroutines counts the goroutines a native.Store keeps while
// it is open: the fsnotify index watcher loop, or the fallback rescan ticker
// on a host that refuses the watch. Close stops and waits for both.
func liveNativeStoreGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "native.(*indexWatcher).loop") ||
			strings.Contains(g, "native.startFallbackRescan.func1") {
			count++
		}
	}
	return count
}

// A Run* that opens the native store must close it: the watcher holds a
// directory handle, which on Windows blocks removing the store dir and
// outlives the call in any in-process caller.
func TestIssueRunClosesTheNativeStore(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "close me"})
	common := IssueCommonOptions{StoreDir: storeDir}
	newP := func() *Printer { return &Printer{W: &bytes.Buffer{}, Format: OutputJSON} }

	title := "renamed"
	cases := []struct {
		name string
		run  func() error
	}{
		{"create", func() error {
			return RunIssueCreate(newP(), IssueCreateOptions{IssueCommonOptions: common, Title: "new"})
		}},
		{"list", func() error { return RunIssueList(newP(), IssueListOptions{IssueCommonOptions: common}) }},
		{"show", func() error {
			return RunIssueShow(newP(), IssueRefOptions{IssueCommonOptions: common, IDOrPrefix: id})
		}},
		{"move", func() error {
			return RunIssueMove(newP(), IssueMoveOptions{IssueCommonOptions: common, IDOrPrefix: id, To: "Todo"})
		}},
		{"update", func() error {
			return RunIssueUpdate(newP(), IssueUpdateOptions{IssueCommonOptions: common, IDOrPrefix: id, Title: &title})
		}},
		{"update unknown card", func() error {
			return RunIssueUpdate(newP(), IssueUpdateOptions{IssueCommonOptions: common, IDOrPrefix: "zzzzzzzz", Title: &title})
		}},
		{"close", func() error {
			return RunIssueClose(newP(), IssueRefOptions{IssueCommonOptions: common, IDOrPrefix: id})
		}},
		{"board show", func() error { return RunIssueBoardShow(newP(), common) }},
		{"board init", func() error {
			return RunIssueBoardInit(newP(), IssueBoardInitOptions{IssueCommonOptions: common, Force: true})
		}},
		{"delete empty ref", func() error {
			return RunIssueDelete(newP(), IssueDeleteOptions{IssueCommonOptions: common, Yes: true})
		}},
		{"delete", func() error {
			return RunIssueDelete(newP(), IssueDeleteOptions{IssueCommonOptions: common, IDOrPrefix: id, Yes: true})
		}},
	}
	for _, tc := range cases {
		before := liveNativeStoreGoroutines()
		_ = tc.run() // the outcome is each command's own test; here only the handle matters
		// Close waits for the watcher loop, so the goroutine is already gone;
		// the short poll only absorbs the scheduler's exit bookkeeping.
		deadline := time.Now().Add(2 * time.Second)
		after := liveNativeStoreGoroutines()
		for after > before && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			after = liveNativeStoreGoroutines()
		}
		if after > before {
			t.Errorf("%s: left %d native store goroutine(s) running — the store was never closed", tc.name, after-before)
		}
	}
}
