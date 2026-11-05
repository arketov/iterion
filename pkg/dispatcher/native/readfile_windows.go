//go:build windows

package native

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// readFileShared reads a whole file the way os.ReadFile does, except that the
// handle is opened with FILE_SHARE_DELETE as well as read and write sharing.
// Go's own open on Windows omits FILE_SHARE_DELETE, so while the index (the
// watcher re-reading a card on an event, a rescan, a Get miss) holds the
// handle, another process cannot delete the card or rename a new copy over it:
// `iterion issue delete`, and the temp-file rename of every update and move,
// failed with "The process cannot access the file because it is being used by
// another process" for as long as the studio server was running.
func readFileShared(path string) ([]byte, error) {
	name := path
	if abs, err := filepath.Abs(path); err == nil {
		name = abs
	}
	if !strings.HasPrefix(name, `\\`) { // not already UNC or extended-length
		name = `\\?\` + name
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path) // takes ownership of the handle
	defer f.Close()
	return io.ReadAll(f)
}
