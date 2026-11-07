//go:build !windows

package dispatcher

import "os"

// renameReplace atomically replaces newpath with oldpath. On Windows the same
// call has to retry while a reader holds the destination open (see
// workspace_rename_windows.go); elsewhere a rename over an open file is fine.
func renameReplace(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
