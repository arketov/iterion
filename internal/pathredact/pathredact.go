// Package pathredact reduces the absolute filesystem paths in a message to
// their last element, so a text written for an operator's log can be shown to
// an API caller without naming the host's directories.
//
// It is platform independent on purpose. A path is recognised by how it is
// SPELLED, never by the operating system the server runs on: filepath.IsAbs
// answers false for `/srv/catalog/x` on Windows and for `C:\x\y` on Linux, and
// an error text may carry either — a store or a catalog written on one host
// and read on another, a container path quoted by a Windows studio, a Windows
// path inside a Linux pod's log line.
//
// Two functions, two postures:
//
//   - Strict reduces EVERY absolute path in the text. For a message whose paths
//     are all the server's own (the bot catalog's discovery diagnostics): a
//     message it has never seen loses a path instead of leaking one.
//   - Host reduces the paths that are the HOST's — under a registered root
//     (the workspace, the store), the home or temp directory, a Windows drive
//     or UNC path, or a well-known host prefix (/home, /var, /tmp, …) — and
//     leaves everything else alone, so the URL paths and identifiers an API
//     error legitimately names (`/api/v1/runs`, `run_1`) survive.
package pathredact

import (
	"os"
	"strings"
	"sync"
)

// IsAbs reports whether s is an absolute path in any spelling: `/x`, a drive
// path `C:\x` or `C:/x`, or a UNC path `\\host\share`.
func IsAbs(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '/' {
		return true
	}
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	return hasDrivePrefix(s)
}

// hasDrivePrefix reports whether s begins with `X:\` or `X:/`.
func hasDrivePrefix(s string) bool {
	return len(s) >= 3 && isASCIILetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isSep(c byte) bool { return c == '/' || c == '\\' }

// Base is the last element of a path in either separator spelling. A path with
// no element left (`/`, `C:\`) is returned as it is.
func Base(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	if trimmed == "" {
		return p
	}
	if i := strings.LastIndexAny(trimmed, `/\`); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// DirPrefix is everything of an absolute path up to and including its last
// separator (`/srv/catalog/` for `/srv/catalog/revi`), or "" for a path that
// is not absolute or has no parent.
func DirPrefix(p string) string {
	if !IsAbs(p) {
		return ""
	}
	trimmed := strings.TrimRight(p, `/\`)
	i := strings.LastIndexAny(trimmed, `/\`)
	if i < 0 {
		return ""
	}
	return trimmed[:i+1]
}

// Strict rewrites every absolute path in s to its last element, keeping the
// punctuation the sentence wrapped it in (quotes, brackets, a trailing comma or
// period) so the prose still reads. Whitespace and its runs are preserved.
func Strict(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && !isSpace(s[j]) {
			j++
		}
		b.WriteString(strictToken(s[i:j]))
		k := j
		for k < len(s) && isSpace(s[k]) {
			k++
		}
		b.WriteString(s[j:k])
		i = k
	}
	return b.String()
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// strictToken returns one whitespace-delimited token with its absolute path, if
// that is what it holds, reduced to the path's last element.
func strictToken(tok string) string {
	const openers = `"'([{<`
	const closers = `"')]}>,;:.`
	inner := strings.TrimLeft(tok, openers)
	open := tok[:len(tok)-len(inner)]
	body := strings.TrimRight(inner, closers)
	if !IsAbs(body) {
		return tok
	}
	return open + Base(body) + inner[len(body):]
}

// hostPrefixes are the top-level directories that only a host filesystem has:
// an absolute path starting with one is the server's, not an API route. Each is
// matched as a whole first segment, so `/api/v1` and `/runs/x` are not touched.
var hostPrefixes = []string{
	"/home/", "/Users/", "/root/", "/var/", "/tmp/", "/srv/", "/data/", "/app/",
	"/opt/", "/mnt/", "/etc/", "/usr/", "/workspace/", "/private/", "/run/",
	"/media/", "/proc/", "/sys/", "/Volumes/", "/builds/", "/github/", "/nix/",
	"/lib/", "/Library/", "/snap/",
}

// maxRoots bounds the registry: a studio that switches projects all day adds a
// root per switch, and the oldest are the least likely to still be quoted.
const maxRoots = 64

var (
	rootsMu sync.RWMutex
	roots   []string // registered roots, oldest first
	envOnce sync.Once
	envRoot []string // home, temp and working directories, resolved once
)

// AddRoots registers directories that are the host's own — the workspace, the
// run store — so Host reduces a path under one to its remainder (relative to
// the root), spaces and all. Empty entries, filesystem roots and duplicates are
// ignored; the registry keeps the newest maxRoots. Safe for concurrent use.
func AddRoots(paths ...string) {
	rootsMu.Lock()
	defer rootsMu.Unlock()
	for _, p := range paths {
		c := cleanRoot(p)
		if c == "" {
			continue
		}
		dup := false
		for _, have := range roots {
			if sameRoot(have, c) {
				dup = true
				break
			}
		}
		if !dup {
			roots = append(roots, c)
		}
	}
	if len(roots) > maxRoots {
		roots = append([]string(nil), roots[len(roots)-maxRoots:]...)
	}
}

// cleanRoot returns p without trailing separators, or "" when p cannot serve as
// a root: not absolute, or a filesystem root (`/`, `C:\`) that would rewrite
// every path in a message.
func cleanRoot(p string) string {
	p = strings.TrimSpace(p)
	if !IsAbs(p) {
		return ""
	}
	c := strings.TrimRight(p, `/\`)
	if c == "" || (len(c) == 2 && c[1] == ':') || c == "/" {
		return ""
	}
	return c
}

func sameRoot(a, b string) bool {
	return len(a) == len(b) && matchAt(a, 0, b, true)
}

// matchAt reports whether s has root at offset i, treating both separators as
// one and, when fold is set, ASCII letters case-insensitively (a Windows path).
func matchAt(s string, i int, root string, fold bool) bool {
	if i+len(root) > len(s) {
		return false
	}
	for k := 0; k < len(root); k++ {
		a, b := s[i+k], root[k]
		if isSep(a) && isSep(b) {
			continue
		}
		if a == b {
			continue
		}
		if fold && isASCIILetter(a) && isASCIILetter(b) && a|0x20 == b|0x20 {
			continue
		}
		return false
	}
	return true
}

// isWindowsStyle reports whether a root is spelled as a Windows path, whose
// letters compare case-insensitively.
func isWindowsStyle(root string) bool {
	return hasDrivePrefix(root) || strings.HasPrefix(root, `\\`) || strings.Contains(root, `\`)
}

func snapshotRoots() []string {
	envOnce.Do(func() {
		var env []string
		if h, err := os.UserHomeDir(); err == nil {
			env = append(env, h)
		}
		env = append(env, os.TempDir())
		if wd, err := os.Getwd(); err == nil {
			env = append(env, wd)
		}
		for _, p := range env {
			if c := cleanRoot(p); c != "" {
				envRoot = append(envRoot, c)
			}
		}
	})
	rootsMu.RLock()
	all := make([]string, 0, len(roots)+len(envRoot))
	all = append(all, roots...)
	rootsMu.RUnlock()
	for _, e := range envRoot {
		dup := false
		for _, have := range all {
			if sameRoot(have, e) {
				dup = true
				break
			}
		}
		if !dup {
			all = append(all, e)
		}
	}
	// Longest first: a workspace under the home directory is cut at the
	// workspace, so the remainder is relative to the project.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && len(all[j]) > len(all[j-1]); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all
}

// Host reduces the host's own paths in s: first any path under a registered or
// well-known root (AddRoots, home, temp, working directory) to its remainder
// relative to that root — a root itself becomes "." — then any Windows drive or
// UNC path and any path under a host-only top-level directory to its last
// element. Everything else, including an API route, is returned unchanged.
func Host(s string) string {
	if s == "" {
		return s
	}
	return redactPrefixes(redactRoots(s, snapshotRoots()))
}

// redactRoots replaces each occurrence of a root in s, at a path boundary, by
// the remainder that follows it.
func redactRoots(s string, rs []string) string {
	if len(rs) == 0 {
		return s
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		matched := false
		if isRootStart(s, i) {
			for _, r := range rs {
				if !matchAt(s, i, r, isWindowsStyle(r)) {
					continue
				}
				end := i + len(r)
				switch {
				case end < len(s) && isSep(s[end]):
					for end < len(s) && isSep(s[end]) {
						end++
					}
					// The remainder stays: it is relative to the root.
				case end == len(s) || !isNameByte(s[end]):
					b.WriteByte('.')
				default:
					continue // `/a/bc` is not under `/a/b`
				}
				if !changed {
					b.Grow(len(s))
					b.WriteString(s[:i])
					changed = true
				}
				i = end
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if changed {
			b.WriteByte(s[i])
		}
		i++
	}
	if !changed {
		return s
	}
	return b.String()
}

// isRootStart reports whether a path may begin at s[i]: the start of the text
// or after a character that cannot be part of a name.
func isRootStart(s string, i int) bool {
	if i == 0 {
		return true
	}
	c := s[i-1]
	return !isNameByte(c) && c != '/' && c != '\\'
}

// isNameByte reports whether c continues a file name (any non-ASCII byte does:
// a name in another script is a name).
func isNameByte(c byte) bool {
	return c >= 0x80 || isASCIILetter(c) || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '~' || c == '%'
}

// redactPrefixes reduces Windows drive/UNC paths and paths under a host-only
// top-level directory to their last element.
func redactPrefixes(s string) string {
	var b strings.Builder
	changed := false
	emit := func(i int, repl string) {
		if !changed {
			b.Grow(len(s))
			b.WriteString(s[:i])
			changed = true
		}
		b.WriteString(repl)
	}
	for i := 0; i < len(s); {
		end := -1
		switch {
		case isRootStart(s, i) && hasDrivePrefix(s[i:]):
			end = windowsPathEnd(s, i+2)
		case isRootStart(s, i) && strings.HasPrefix(s[i:], `\\`) && i+2 < len(s) && s[i+2] != '\\':
			end = windowsPathEnd(s, i+2)
		case s[i] == '/' && isUnixBoundary(s, i) && hasHostPrefix(s[i:]):
			end = unixPathEnd(s, i)
		}
		if end > i {
			end = trimPathTail(s, i, end)
			emit(i, Base(s[i:end]))
			i = end
			continue
		}
		if changed {
			b.WriteByte(s[i])
		}
		i++
	}
	if !changed {
		return s
	}
	return b.String()
}

// windowsPathEnd is where a Windows path starting before from ends: at the
// first character no Windows file name holds (a quote, `<>|*?`, a colon after
// the drive) or a line end. A space does not end it — directory names have
// them — so the sentence after the path may stay inside the cut; only its last
// element is kept, which can carry that tail but never a directory.
func windowsPathEnd(s string, from int) int {
	i := from
	for i < len(s) {
		switch s[i] {
		case '"', '\'', '<', '>', '|', '*', '?', ':', '\n', '\r', '\t':
			return i
		}
		i++
	}
	return i
}

// unixPathEnd is where a Unix path ends: at whitespace or a quote/bracket the
// sentence wrapped it in.
func unixPathEnd(s string, from int) int {
	i := from
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r', '"', '\'', '<', '>', '|', '*', '?', ')', ']', '}':
			return i
		}
		i++
	}
	return i
}

// trimPathTail gives the sentence's punctuation back: a path that ends a
// clause is followed by a period, comma or colon that is not part of it.
func trimPathTail(s string, start, end int) int {
	for end > start+1 && strings.IndexByte(`.,;:`, s[end-1]) >= 0 {
		end--
	}
	return end
}

func isUnixBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	c := s[i-1]
	return !isNameByte(c) && c != '/' && c != '\\'
}

func hasHostPrefix(s string) bool {
	for _, p := range hostPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
