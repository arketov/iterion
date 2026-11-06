//go:build windows

package dispatcher

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION; package syscall does not
// export a constant for it.
const errorSharingViolation = syscall.Errno(32)

// renameBackoffs are the pauses between attempts to replace a file that
// another reader holds open: 9 attempts, about 1.3 s in all.
var renameBackoffs = []time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
	80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond, 640 * time.Millisecond,
}

// renameReplace atomically replaces newpath with oldpath. Go opens files on
// Windows without FILE_SHARE_DELETE (see dispatcher/native/readfile_windows.go),
// so a concurrent reader of newpath — a lock-free probe, an antivirus, OneDrive,
// the studio — makes MoveFileEx fail with ACCESS_DENIED or a sharing violation.
// Those two errors are transient: the rename is retried with a short backoff,
// and the last error is returned when the budget is spent. Any other error is
// returned at once.
func renameReplace(oldpath, newpath string) error {
	err := os.Rename(oldpath, newpath)
	for _, pause := range renameBackoffs {
		if err == nil || !(errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)) {
			return err
		}
		time.Sleep(pause)
		err = os.Rename(oldpath, newpath)
	}
	return err
}
