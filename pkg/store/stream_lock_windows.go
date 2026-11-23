//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/gofrs/flock"
)

// tryStreamReadLock is the Windows counterpart of stream_lock_unix.go: an
// existing reader-accessible lock file is probed with the very lock the
// writers take (lock_windows.go: an OS LockFileEx lock, released when its
// holder lets go or dies), never by reading a PID out of it. A held lock is
// busy; a free one is returned owned, as flock is on Unix; a missing file is
// quiescent and is never created here.
//
// (Fork-only. Upstream's Windows reader read a decimal PID from the lock file
// and asked the OS whether it was alive; that protocol needs writers that
// publish their PID into it, which the OS lock does not — an empty lock file
// would read as "PID publication pending" forever.)
func tryStreamReadLock(path, label string) (RunLock, streamReadLockOutcome, error) {
	before, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, streamReadLockQuiescent, nil
	}
	if e != nil {
		return nil, streamReadLockFailure, e
	}
	if !before.Mode().IsRegular() {
		return nil, streamReadLockFailure, ErrRunDataIdentityChanged
	}
	fl := flock.New(path, flock.SetPermissions(filePerm))
	ok, e := fl.TryLock()
	if e != nil {
		return nil, streamReadLockFailure, e
	}
	if !ok {
		return nil, streamReadLockBusy, fmt.Errorf("store: %s is locked by another process", label)
	}
	current, e := os.Lstat(path)
	if e != nil || !current.Mode().IsRegular() || !os.SameFile(before, current) {
		err := errors.Join(e, fl.Unlock())
		if err == nil {
			err = ErrRunDataIdentityChanged
		}
		return nil, streamReadLockFailure, err
	}
	return &fileLock{fl: fl}, streamReadLockOwned, nil
}
