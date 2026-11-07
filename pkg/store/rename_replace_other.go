//go:build !windows

package store

import "os"

// renameReplace replaces to with from. POSIX rename(2) replaces atomically
// whatever handles are open on the destination, so it never hits the
// Windows sharing failure rename_replace_windows.go retries.
func renameReplace(from, to string) error { return os.Rename(from, to) }
