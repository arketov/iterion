package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arketov/iterion/pkg/authoringlock"
	"golang.org/x/sys/windows"
)

// openDependencyDirectory opens a rename parent pinned to the directory the
// caller stat'd, through the same code as the authoring lock's control
// directory.
func openDependencyDirectory(path string, expected os.FileInfo) (*os.File, error) {
	return authoringlock.OpenDirectory(path, expected)
}

func renameDependencyNoReplace(from, to string, fromInfo, toInfo os.FileInfo) error {
	source, err := openDependencyDirectory(filepath.Dir(from), fromInfo)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := openDependencyDirectory(filepath.Dir(to), toInfo)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	return renameAuthoringAt(source, filepath.Base(from), target, filepath.Base(to))
}

func requireDependencySameFilesystem(a, b string) error {
	left, err := dependencyVolume(a)
	if err != nil {
		return err
	}
	right, err := dependencyVolume(b)
	if err != nil {
		return err
	}
	if left != right {
		return fmt.Errorf("dependency recovery storage must share the install filesystem: %w", windows.ERROR_NOT_SAME_DEVICE)
	}
	return nil
}

// dependencyVolume is the serial number of the volume path is on, read from an
// open handle (os.Open takes directories on Windows).
func dependencyVolume(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, err
	}
	return info.VolumeSerialNumber, nil
}
