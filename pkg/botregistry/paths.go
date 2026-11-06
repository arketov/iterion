package botregistry

import (
	"path/filepath"
	"runtime"
	"strings"
)

// PathKey returns the key two spellings of one path share, for comparing and
// deduplicating paths: the absolute cleaned form, symlinks resolved when the
// path exists, and on Windows lower-cased (the file system ignores case, so
// `C:\Proj\a.bot` and `c:\proj\A.bot` are one file; `EvalSymlinks` also
// expands 8.3 short names for a path that exists). A path that cannot be
// resolved keys as its cleaned absolute form — a mismatch stays a mismatch
// rather than becoming a guess. Equal keys mean the same path; different keys
// do not prove different files (a hard link, a case-sensitive Windows
// directory).
func PathKey(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if res, err := filepath.EvalSymlinks(abs); err == nil {
		abs = res
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}
