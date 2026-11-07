package projects

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"github.com/arketov/iterion/pkg/dispatcher"
	"github.com/arketov/iterion/pkg/dispatcher/native"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// movedProject lays out the live case of the card: a project folder that was
// moved (the old Dir no longer exists, its store slot is still full of data)
// and the new, empty store slot the studio minted for the new path.
type movedProject struct {
	oldProj, newProj Project
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newMovedProject(t *testing.T) movedProject {
	t.Helper()
	parent := t.TempDir()
	oldDir := filepath.Join(parent, "old", "proj") // never created: the folder moved away
	newDir := filepath.Join(parent, "new", "proj")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldStore := filepath.Join(parent, "slot-old")
	newStore := filepath.Join(parent, "slot-new")
	if err := os.MkdirAll(newStore, 0o755); err != nil {
		t.Fatal(err)
	}
	slashed := filepath.ToSlash(oldDir)
	dispatcherJSON, err := json.Marshal(map[string]any{
		"workflow": filepath.Join(oldDir, "bots", "x", "main.bot"),
		"tracker":  map[string]any{"kind": "native"},
		"bots":     map[string]any{"paths": []string{filepath.Join(oldDir, "bots"), "./relative/bots"}},
		"workspace": map[string]any{
			"root": filepath.Join(oldDir, ".ws"),
		},
		"hooks": map[string]any{
			"after_create": map[string]any{"path": slashed + "/hooks/after.sh"},
		},
		"sibling":  oldDir + "2" + string(filepath.Separator) + "keep", // another folder sharing the prefix
		"relative": "./bots/x/main.bot",
		"token":    "$ITERION_TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(oldStore, "dispatcher", "dispatcher.json"), string(dispatcherJSON))
	writeFile(t, filepath.Join(oldStore, "dispatcher", "board.json"), `{"states":[{"name":"todo","eligible":true},{"name":"done","terminal":true}]}`)
	writeFile(t, filepath.Join(oldStore, "dispatcher", "issues", "a.json"), `{"id":"a","title":"first"}`)
	writeFile(t, filepath.Join(oldStore, "dispatcher", "issues", "b.json"), `{"id":"b","title":"second"}`)
	writeFile(t, filepath.Join(oldStore, "dispatcher", "events.jsonl"), "{\"seq\":0}\n{\"seq\":1}\n")
	writeFile(t, filepath.Join(oldStore, "dispatcher", "runtime.json"), `{"desired":"running"}`)
	writeFile(t, filepath.Join(oldStore, "dispatcher", "claims.json"), `{}`)
	writeFile(t, filepath.Join(oldStore, "runs", "r1", "run.json"), `{"id":"r1"}`)
	writeFile(t, filepath.Join(oldStore, "worktrees", "w", ".git"), "gitdir: elsewhere")
	writeFile(t, filepath.Join(oldStore, "secrets.json"), `{"secret":"x"}`)
	return movedProject{
		oldProj: Project{ID: "old", Name: "proj", Dir: oldDir, StoreDir: oldStore},
		newProj: Project{ID: "new", Name: "proj", Dir: newDir, StoreDir: newStore},
	}
}

func (m movedProject) registry() *Config {
	return &Config{Version: 1, RecentProjects: []Project{m.newProj, m.oldProj}}
}

func TestRelocationSources_FindsMovedProject(t *testing.T) {
	m := newMovedProject(t)
	got := m.registry().RelocationSources(m.newProj)
	if len(got) != 1 {
		t.Fatalf("sources = %+v, want exactly the moved project's old slot", got)
	}
	src := got[0]
	if src.ID != "old" || src.Dir != m.oldProj.Dir || src.StoreDir != m.oldProj.StoreDir {
		t.Errorf("source = %+v, want the old registry entry", src)
	}
	if src.Cards != 2 || src.Runs != 1 || !src.HasDispatcherConfig {
		t.Errorf("counters = cards %d runs %d config %v, want 2 / 1 / true", src.Cards, src.Runs, src.HasDispatcherConfig)
	}
}

func TestRelocationSources_RejectsLookalikes(t *testing.T) {
	cases := map[string]func(m *movedProject){
		"different folder name": func(m *movedProject) { m.oldProj.Dir = filepath.Join(filepath.Dir(m.oldProj.Dir), "other") },
		"old folder still exists": func(m *movedProject) {
			if err := os.MkdirAll(m.oldProj.Dir, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"same store as the target": func(m *movedProject) { m.oldProj.StoreDir = m.newProj.StoreDir },
		"entry without a store":    func(m *movedProject) { m.oldProj.StoreDir = "" },
		"source holds no data": func(m *movedProject) {
			empty := t.TempDir()
			m.oldProj.StoreDir = empty
		},
		"source slot is gone": func(m *movedProject) { m.oldProj.StoreDir = filepath.Join(t.TempDir(), "missing") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMovedProject(t)
			mutate(&m)
			if got := m.registry().RelocationSources(m.newProj); len(got) != 0 {
				t.Fatalf("sources = %+v, want none", got)
			}
		})
	}
}

func TestRelocate_CopiesBoardRunsAndRewritesPaths(t *testing.T) {
	m := newMovedProject(t)
	srcSnapshot := snapshotTree(t, m.oldProj.StoreDir)

	if err := Relocate(m.oldProj, m.newProj); err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	dst, src := m.newProj.StoreDir, m.oldProj.StoreDir
	for _, rel := range []string{
		filepath.Join("dispatcher", "board.json"),
		filepath.Join("dispatcher", "issues", "a.json"),
		filepath.Join("dispatcher", "issues", "b.json"),
		filepath.Join("dispatcher", "events.jsonl"),
		filepath.Join("runs", "r1", "run.json"),
	} {
		if got, want := readFile(t, filepath.Join(dst, rel)), readFile(t, filepath.Join(src, rel)); got != want {
			t.Errorf("%s = %q, want the source's %q", rel, got, want)
		}
	}
	for _, rel := range []string{"worktrees", "secrets.json", filepath.Join("dispatcher", "claims.json")} {
		if exists(filepath.Join(dst, rel)) {
			t.Errorf("%s was copied; it must stay behind", rel)
		}
	}
	entries, _ := os.ReadDir(dst)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".relocate-") {
			t.Errorf("staging folder %s left behind", e.Name())
		}
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dst, "dispatcher", "dispatcher.json"))), &cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := cfg["workflow"], filepath.Join(m.newProj.Dir, "bots", "x", "main.bot"); got != want {
		t.Errorf("workflow = %v, want %v", got, want)
	}
	paths := cfg["bots"].(map[string]any)["paths"].([]any)
	if paths[0] != filepath.Join(m.newProj.Dir, "bots") || paths[1] != "./relative/bots" {
		t.Errorf("bots.paths = %v, want the first moved and the relative one kept", paths)
	}
	if got := cfg["workspace"].(map[string]any)["root"]; got != filepath.Join(m.newProj.Dir, ".ws") {
		t.Errorf("workspace.root = %v", got)
	}
	hook := cfg["hooks"].(map[string]any)["after_create"].(map[string]any)["path"].(string)
	if !strings.HasPrefix(filepath.FromSlash(hook), m.newProj.Dir) || !strings.HasSuffix(hook, "/hooks/after.sh") {
		t.Errorf("hook path = %q, want it under the new root", hook)
	}
	if got, want := cfg["sibling"], m.oldProj.Dir+"2"+string(filepath.Separator)+"keep"; got != want {
		t.Errorf("sibling = %v, want %v: a longer folder name sharing the prefix is not the old root", got, want)
	}
	if cfg["relative"] != "./bots/x/main.bot" || cfg["token"] != "$ITERION_TOKEN" {
		t.Errorf("relative path or $ENV reference rewritten: %v / %v", cfg["relative"], cfg["token"])
	}

	// runtime.json must not carry the source's "running": without a stopped
	// intent a dispatcher with a config starts by itself on the next boot.
	var state struct {
		Desired string `json:"desired"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dst, "dispatcher", "runtime.json"))), &state); err != nil {
		t.Fatal(err)
	}
	if state.Desired != "stopped" {
		t.Errorf("runtime.json desired = %q, want stopped", state.Desired)
	}

	if got := snapshotTree(t, src); got != srcSnapshot {
		t.Errorf("the source changed:\nbefore %s\nafter  %s", srcSnapshot, got)
	}
}

func TestRelocate_RefusesNonEmptyDestination(t *testing.T) {
	nonEmpty := map[string]func(t *testing.T, dst string){
		"a saved dispatcher config": func(t *testing.T, dst string) {
			writeFile(t, filepath.Join(dst, "dispatcher", "dispatcher.json"), `{}`)
		},
		"a card": func(t *testing.T, dst string) {
			writeFile(t, filepath.Join(dst, "dispatcher", "issues", "x.json"), `{}`)
		},
		"a run": func(t *testing.T, dst string) {
			writeFile(t, filepath.Join(dst, "runs", "r9", "run.json"), `{}`)
		},
	}
	for name, seed := range nonEmpty {
		t.Run(name, func(t *testing.T) {
			m := newMovedProject(t)
			seed(t, m.newProj.StoreDir)
			before := snapshotTree(t, m.newProj.StoreDir)
			err := Relocate(m.oldProj, m.newProj)
			if !errors.Is(err, ErrDestinationNotEmpty) {
				t.Fatalf("Relocate = %v, want ErrDestinationNotEmpty", err)
			}
			if after := snapshotTree(t, m.newProj.StoreDir); after != before {
				t.Errorf("the destination changed:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	t.Run("the defaults NativeStore creates are not data", func(t *testing.T) {
		m := newMovedProject(t)
		writeFile(t, filepath.Join(m.newProj.StoreDir, "dispatcher", "board.json"), `{"states":[{"name":"default","eligible":true}]}`)
		if err := os.MkdirAll(filepath.Join(m.newProj.StoreDir, "dispatcher", "issues"), 0o755); err != nil {
			t.Fatal(err)
		}
		if empty, err := SlotIsEmpty(m.newProj.StoreDir); err != nil || !empty {
			t.Fatalf("SlotIsEmpty = %v, %v, want true", empty, err)
		}
		if err := Relocate(m.oldProj, m.newProj); err != nil {
			t.Fatalf("Relocate: %v", err)
		}
		if got := readFile(t, filepath.Join(m.newProj.StoreDir, "dispatcher", "board.json")); got != `{"states":[{"name":"todo","eligible":true},{"name":"done","terminal":true}]}` {
			t.Errorf("board.json = %q, want the source's board", got)
		}
	})
}

func TestRelocate_FailureLeavesNoPartialState(t *testing.T) {
	m := newMovedProject(t)
	before := snapshotTree(t, m.newProj.StoreDir)

	calls := 0
	boom := errors.New("disk on fire")
	err := relocate(m.oldProj, m.newProj, func(src, dst string) error {
		calls++
		if calls == 3 {
			return boom
		}
		return copyFileContents(src, dst)
	})
	if !errors.Is(err, boom) {
		t.Fatalf("relocate = %v, want the injected copy error", err)
	}
	if calls < 3 {
		t.Fatalf("copy ran %d times; the failure was not reached mid-copy", calls)
	}
	if after := snapshotTree(t, m.newProj.StoreDir); after != before {
		t.Errorf("a failed relocation left state behind:\nbefore %s\nafter  %s", before, after)
	}

	// The slot is still empty, so the owner can simply try again.
	if err := Relocate(m.oldProj, m.newProj); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRelocate_RefusesBusySource(t *testing.T) {
	m := newMovedProject(t)
	lockPath := filepath.Join(m.oldProj.StoreDir, workspaceLockName)
	writeFile(t, lockPath, "")
	held := flock.New(lockPath)
	if locked, err := held.TryLock(); err != nil || !locked {
		t.Fatalf("TryLock = %v, %v", locked, err)
	}
	defer held.Unlock()
	if err := Relocate(m.oldProj, m.newProj); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("Relocate = %v, want ErrSourceBusy", err)
	}
	if empty, err := SlotIsEmpty(m.newProj.StoreDir); err != nil || !empty {
		t.Errorf("a refused relocation changed the destination (empty = %v, err = %v)", empty, err)
	}
}

// A relocated slot must not start the dispatcher by itself: the config points
// at the new root (so it is loadable) and runtime.json says stopped. This also
// pins the runtime.json format to the one pkg/dispatcher reads.
func TestRelocatedConfigDoesNotAutostart(t *testing.T) {
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "")
	m := newMovedProject(t)
	// A workflow the rewritten config can resolve under the NEW root.
	writeFile(t, filepath.Join(m.newProj.Dir, "bots", "x", "main.bot"), "tool noop:\n  command: \"true\"\n\nworkflow minimal:\n  entry: noop\n  noop -> done\n")

	if err := Relocate(m.oldProj, m.newProj); err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	ns, err := native.NewStore(filepath.Join(m.newProj.StoreDir, "dispatcher"))
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := dispatcher.NewManager(dispatcher.ManagerOptions{
		StoreDir:    m.newProj.StoreDir,
		NativeStore: ns,
		Logger:      iterlog.Nop(),
		ProjectDir:  m.newProj.Dir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Stop()
	status := mgr.Status()
	if status.State != dispatcher.ManagerStateIdle {
		t.Errorf("state = %q, want idle: the relocated dispatcher started by itself", status.State)
	}
	if !status.HasConfig {
		t.Error("has_config = false, want the relocated config to load")
	}
}

func TestRewriteRoot(t *testing.T) {
	sep := string(filepath.Separator)
	root := sep + "old" + sep + "proj"
	if runtime.GOOS == "windows" {
		root = `C:\Old\proj`
	}
	newRoot := filepath.Join(filepath.Dir(root), "..", "moved")
	newRoot = filepath.Clean(newRoot)
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{root, newRoot, true},
		{root + sep + "bots" + sep + "x", newRoot + sep + "bots" + sep + "x", true},
		{root + "2" + sep + "x", "", false},
		{"./bots", "", false},
		{"$ITERION_HOME" + sep + "x", "", false},
		{"", "", false},
	} {
		got, ok := rewriteRoot(tc.in, root, newRoot)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("rewriteRoot(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if runtime.GOOS == "windows" {
		for in, want := range map[string]string{
			`c:\old\PROJ\bots`:    `D:\new\bots`,
			`C:/Old/proj/hook.sh`: `D:\new/hook.sh`,
		} {
			if got, ok := rewriteRoot(in, root, `D:\new`); !ok || got != want {
				t.Errorf("rewriteRoot(%q) = %q, %v; want %q (Windows paths ignore case and slash direction)", in, got, ok, want)
			}
		}
	}
}

// snapshotTree is a deterministic description of every file under dir (name and
// content), used to prove a folder was or was not touched.
func snapshotTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			b.WriteString("D " + filepath.ToSlash(rel) + "\n")
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString("F " + filepath.ToSlash(rel) + " " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
