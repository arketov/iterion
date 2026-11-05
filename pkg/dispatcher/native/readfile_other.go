//go:build !windows

package native

import "os"

// readFileShared is os.ReadFile off Windows, where an open file never blocks
// another process's unlink or rename; see readfile_windows.go.
func readFileShared(path string) ([]byte, error) { return os.ReadFile(path) }
