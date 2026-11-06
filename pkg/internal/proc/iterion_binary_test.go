package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLocateIterionBinary_AbsolutisesARelativeITERIONBIN pins the one
// absolute form every consumer of the resolver relies on: a relative
// ITERION_BIN is stat'd against the engine's cwd, but the path travels to
// subprocesses running with a DIFFERENT cwd (a tool node's workDir, an
// MCP server, a bind-mount source) where a relative path resolves
// elsewhere or nowhere — silently. A shim symlinked to it would dangle
// with the staging reporting success.
//
// Mutation seen red: drop LocateIterionBinary's filepath.Abs tail — the
// resolver returns the bare relative name.
func TestLocateIterionBinary_AbsolutisesARelativeITERIONBIN(t *testing.T) {
	dir := t.TempDir()
	name := iterionBinaryName(runtime.GOOS)
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("ITERION_BIN", name) // relative, exists, executable

	got := LocateIterionBinary()
	if !filepath.IsAbs(got) {
		t.Fatalf("LocateIterionBinary() = %q, want an absolute path", got)
	}
	if got != bin {
		t.Fatalf("LocateIterionBinary() = %q, want %q", got, bin)
	}
}

// The fixed install-path tier is injectable: a pinned tier is the one the
// lookup consults, and the restore hands the decision back to the host.
func TestSetInstallPathCandidatesPinsTheFixedTier(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, iterionBinaryName(runtime.GOOS))
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ITERION_BIN", "")
	t.Setenv("HOME", t.TempDir())

	restore := SetInstallPathCandidates(func() []string { return []string{fake} })
	defer restore()
	if got := LocateIterionBinary(); got != fake {
		t.Fatalf("LocateIterionBinary() = %q, want the pinned tier candidate %q", got, fake)
	}

	restore()
	if got := LocateIterionBinary(); got == fake {
		t.Fatal("the pinned candidate survived the restore")
	}
}

// ITERION_BIN keeps its precedence over the fixed tier: the operator's
// escape hatch wins without touching the injected candidates.
func TestInstallTierYieldsToTheEnvOverride(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "pinned-iterion"+exeSuffix())
	envBin := filepath.Join(dir, "env-iterion"+exeSuffix())
	for _, p := range []string{pinned, envBin} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ITERION_BIN", envBin)
	t.Setenv("HOME", t.TempDir())

	restore := SetInstallPathCandidates(func() []string { return []string{pinned} })
	defer restore()
	if got := LocateIterionBinary(); got != envBin {
		t.Fatalf("LocateIterionBinary() = %q, want the ITERION_BIN override %q", got, envBin)
	}
}

// On Windows Go reports no execute bits for any file, so "executable" means
// a regular file with an .exe extension.
func TestIsExecutableFileFor_WindowsUsesExeExtension(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "iterion.exe")
	bare := filepath.Join(dir, "iterion")
	for _, p := range []string{exe, bare} {
		if err := os.WriteFile(p, []byte("MZ"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exeDir := filepath.Join(dir, "x.exe")
	if err := os.Mkdir(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if !isExecutableFileFor("windows", exe) {
		t.Error("a regular .exe file must be executable on windows")
	}
	if isExecutableFileFor("windows", bare) {
		t.Error("a file without .exe must not be executable on windows")
	}
	if isExecutableFileFor("windows", exeDir) {
		t.Error("a directory named x.exe must not be executable")
	}
	if isExecutableFileFor("windows", filepath.Join(dir, "missing.exe")) {
		t.Error("a missing path must not be executable")
	}

	if runtime.GOOS == "windows" {
		return // permission bits are meaningless here
	}
	x := filepath.Join(dir, "x")
	nx := filepath.Join(dir, "nx")
	if err := os.WriteFile(x, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nx, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFileFor("linux", x) {
		t.Error("a 0755 file must be executable on linux")
	}
	if isExecutableFileFor("linux", nx) {
		t.Error("a 0644 file must not be executable on linux")
	}
}

func TestIterionBinaryName(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "iterion.exe",
		"linux":   "iterion",
		"darwin":  "iterion",
	} {
		if got := iterionBinaryName(goos); got != want {
			t.Errorf("iterionBinaryName(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestIsVolatileBuildPath_BackslashPaths(t *testing.T) {
	for p, want := range map[string]bool{
		`C:\Users\u\AppData\Local\Temp\go-build123\b001\exe\x.exe`: true,
		"/tmp/go-build123/b001/exe/x":                              true,
		"/var/folders/ab/T/go-build9/b001/x":                       true,
		`C:\Program Files\Iterion\iterion.exe`:                     false,
	} {
		if got := isVolatileBuildPath(p); got != want {
			t.Errorf("isVolatileBuildPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// exeSuffix is the extension the lookup requires on this host's binaries.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
