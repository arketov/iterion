//go:build windows

package proc

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// treeProc is one process of a tree being swept. create and exit are
// FILETIME values; exit is 0 while the process is alive (or is treated as
// unbounded). h is 0 for a root the caller holds no handle on.
type treeProc struct {
	pid          uint32
	create, exit uint64
	h            windows.Handle
}

func fileTime(lo, hi uint32) uint64 { return uint64(hi)<<32 | uint64(lo) }

// snapshotParents maps a parent pid to the pids of the processes it created,
// as the Toolhelp snapshot reports them.
func snapshotParents() (map[uint32][]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	children := make(map[uint32][]uint32)
	err = windows.Process32First(snap, &entry)
	for err == nil {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		err = windows.Process32Next(snap, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return children, nil
}

// openForKill opens pid with the rights the sweep needs and reads its
// creation and exit times. The open handle pins the pid: it cannot be
// recycled while the handle lives. ok is false when the process is gone or
// cannot be opened.
func openForKill(pid uint32) (h windows.Handle, create, exit uint64, ok bool) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, 0, 0, false
	}
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		windows.CloseHandle(h)
		return 0, 0, 0, false
	}
	return h, fileTime(c.LowDateTime, c.HighDateTime), fileTime(e.LowDateTime, e.HighDateTime), true
}

// killDescendants terminates every descendant of root found through the
// parent-pid links of Toolhelp snapshots, re-snapshotting until a pass finds
// nothing new. A link parent→child is accepted only when the child was
// created inside the parent's lifetime, so a pid recycled after the parent
// exited is never mistaken for a child. Every descendant is held open until
// the sweep ends, so its pid cannot be recycled meanwhile. The root's handle,
// when it has one, belongs to the caller. It reports whether anything was
// terminated.
func killDescendants(root treeProc) (killed bool, err error) {
	self := windows.GetCurrentProcessId()
	known := map[uint32]*treeProc{root.pid: &root}
	defer func() {
		for pid, p := range known {
			if pid != root.pid && p.h != 0 {
				windows.CloseHandle(p.h)
			}
		}
	}()

	for {
		children, serr := snapshotParents()
		if serr != nil {
			return killed, serr
		}
		parents := make([]*treeProc, 0, len(known))
		for _, p := range known {
			parents = append(parents, p)
		}
		added := false
		for _, p := range parents {
			for _, c := range children[p.pid] {
				if c == 0 || c == self {
					continue
				}
				if _, seen := known[c]; seen {
					continue
				}
				h, create, exit, ok := openForKill(c)
				if !ok {
					continue
				}
				if create < p.create || (p.exit != 0 && create > p.exit) {
					windows.CloseHandle(h)
					continue
				}
				child := &treeProc{pid: c, create: create, exit: exit, h: h}
				if exit == 0 {
					if windows.TerminateProcess(h, 1) == nil {
						killed = true
					}
					// A killed child's late-created children are still its own.
					child.exit = 0
				}
				known[c] = child
				added = true
			}
		}
		if !added {
			return killed, nil
		}
	}
}
