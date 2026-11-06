package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDependencyRenameNoReplaceMovesDirectoriesAndRefusesAnExistingTarget(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, dir := range []string{a, b, filepath.Join(a, "x"), filepath.Join(a, "z"), filepath.Join(b, "y2")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(a, "x", "f"), filepath.Join(a, "z", "f"), filepath.Join(b, "y2", "f")} {
		if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	infoA, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Lstat(b)
	if err != nil {
		t.Fatal(err)
	}

	if err := renameDependencyNoReplace(filepath.Join(a, "x"), filepath.Join(b, "y"), infoA, infoB); err != nil {
		t.Fatalf("rename into a free name: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(a, "x")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b, "y", "f")); err != nil {
		t.Fatalf("moved content missing: %v", err)
	}

	err = renameDependencyNoReplace(filepath.Join(a, "z"), filepath.Join(b, "y2"), infoA, infoB)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rename onto an existing target = %v, want fs.ErrExist", err)
	}
	for _, file := range []string{filepath.Join(a, "z", "f"), filepath.Join(b, "y2", "f")} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("refused rename disturbed %s: %v", file, err)
		}
	}

	// A parent that is not the directory the caller inspected is refused.
	if err := renameDependencyNoReplace(filepath.Join(a, "z"), filepath.Join(b, "y3"), infoB, infoB); err == nil {
		t.Fatal("rename with another directory's identity succeeded")
	}
	if _, err := os.Stat(filepath.Join(a, "z", "f")); err != nil {
		t.Fatalf("source touched by a refused rename: %v", err)
	}

	if err := requireDependencySameFilesystem(a, b); err != nil {
		t.Fatalf("same filesystem: %v", err)
	}
	if err := requireDependencySameFilesystem(a, filepath.Join(base, "missing")); err == nil {
		t.Fatal("a missing path passed the filesystem check")
	}
}

func TestDependencySnapshotIdentityFollowsTheObjectNotThePath(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	for _, dir := range []bool{false, true} {
		p, q := filepath.Join(base, "p"), filepath.Join(base, "q")
		create := func(path string) {
			t.Helper()
			if dir {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "f")
			}
			if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		create(p)
		snap, err := dependencySnapshot(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(p, q); err != nil {
			t.Fatal(err)
		}
		if err := snap.matches(ctx, q); err != nil {
			t.Fatalf("dir=%v: the renamed object no longer matches its snapshot: %v", dir, err)
		}
		// Another object with the same bytes at the old path is not the same file.
		create(p)
		err = snap.matches(ctx, p)
		var conflict authoringConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("dir=%v: a look-alike at the old path matched: %v", dir, err)
		}
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(q); err != nil {
			t.Fatal(err)
		}
	}
}

// dependencyTestPerm is the permission a file created with want reports: Windows
// keeps no POSIX bits, so a writable file reads back as 0o666 (0o777 for a
// directory that was meant to be traversable).
func dependencyTestPerm(want fs.FileMode) fs.FileMode {
	if runtime.GOOS != "windows" {
		return want
	}
	if want&0o100 != 0 {
		return 0o777
	}
	return 0o666
}

// symlinkOrSkip creates a symlink, skipping the test where the platform or the
// account may not (Windows without the symlink privilege).
func symlinkOrSkip(t *testing.T, target, link string) error {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		return err
	}
	return nil
}
