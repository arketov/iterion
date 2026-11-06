//go:build windows

package store

import (
	"errors"
	"math/rand"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameRetryWindow bounds how long renameReplace keeps retrying a blocked
// rename. A package var so a test can shorten it.
var renameRetryWindow = 2 * time.Second

// renameReplace replaces to with from, retrying while the destination is
// transiently held open.
//
// os.Rename is MoveFileEx(MOVEFILE_REPLACE_EXISTING), which fails with
// ERROR_ACCESS_DENIED when the destination has any open handle — even one
// opened with FILE_SHARE_DELETE, such as the native board's own index reads —
// and with ERROR_SHARING_VIOLATION / ERROR_LOCK_VIOLATION while a scanner or
// another process holds it. These holds are brief, so a short bounded backoff
// turns a lost write into a short wait. The Go team's robustio.Rename does the
// same (https://go.googlesource.com/tools/+/master/internal/robustio/robustio.go,
// golang/go#31247 and #32188). A missing source is a real error, not retried.
//
// At the deadline the last error is returned unwrapped, so the caller's
// message is the same as for an immediate failure.
func renameReplace(from, to string) error {
	start := time.Now()
	backoff := time.Millisecond
	for {
		err := os.Rename(from, to)
		if err == nil || !isTransientRenameErr(err) {
			return err
		}
		if time.Since(start)+backoff > renameRetryWindow {
			return err
		}
		time.Sleep(backoff + time.Duration(rand.Int63n(int64(backoff)+1)))
		if backoff < 50*time.Millisecond {
			backoff *= 2
			if backoff > 50*time.Millisecond {
				backoff = 50 * time.Millisecond
			}
		}
	}
}

// isTransientRenameErr reports whether err is a handle-hold failure worth
// retrying. os.LinkError unwraps to the Errno, which errors.Is matches.
func isTransientRenameErr(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
