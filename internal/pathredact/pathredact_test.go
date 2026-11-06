package pathredact

import "testing"

func TestIsAbs_BothSpellingsOnEveryOS(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"/srv/catalog/x", true},
		{`C:\Users\x`, true},
		{`c:/Users/x`, true},
		{`\\host\share\x`, true},
		{"", false},
		{"relative/path", false},
		{`..\bots\revi`, false},
		{"C:", false},
		{"C:x", false},
		{"manifest.yaml", false},
	} {
		if got := IsAbs(tc.in); got != tc.want {
			t.Errorf("IsAbs(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestBase_BothSeparators(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/srv/catalog/revi", "revi"},
		{`C:\srv\catalog\revi`, "revi"},
		{`C:/srv/catalog/revi/`, "revi"},
		{`\\host\share\x.bot`, "x.bot"},
		{"/", "/"},
		{"plain", "plain"},
	} {
		if got := Base(tc.in); got != tc.want {
			t.Errorf("Base(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDirPrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/srv/catalog/revi", "/srv/catalog/"},
		{`C:\srv\catalog\revi`, `C:\srv\catalog\`},
		{"relative/x", ""},
		{"", ""},
	} {
		if got := DirPrefix(tc.in); got != tc.want {
			t.Errorf("DirPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStrict_RecognisesEitherSpellingOnEveryOS is the witness of the Windows
// card: a `/srv/catalog/...` path stayed in a message because filepath.IsAbs
// says false for it on Windows, and no test spelled a `C:\...` path at all.
// Strict decides by spelling, so the same table holds on Linux and Windows.
func TestStrict_RecognisesEitherSpellingOnEveryOS(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"bot name \"revi\" is already claimed by /srv/catalog/revi, so nothing routes", "bot name \"revi\" is already claimed by revi, so nothing routes"},
		{`bot name "revi" is already claimed by C:\srv\catalog\revi, so nothing routes`, `bot name "revi" is already claimed by revi, so nothing routes`},
		{`open "C:\srv\catalog\a\manifest.yaml": no such file`, `open "manifest.yaml": no such file`},
		{`open "C:/srv/catalog/a/manifest.yaml": no such file`, `open "manifest.yaml": no such file`},
		{`open C:\srv\catalog\a\manifest.yaml: The system cannot find the file specified.`, `open manifest.yaml: The system cannot find the file specified.`},
		{`lstat (\\fileserver\share\deep) failed.`, `lstat (deep) failed.`},
		{"lstat (/srv/catalog/deep) failed.", "lstat (deep) failed."},
		{"claimed by ../bots/revi, so nothing routes", "claimed by ../bots/revi, so nothing routes"},
		{`claimed by ..\bots\revi, so nothing routes`, `claimed by ..\bots\revi, so nothing routes`},
		{"broken-chat/manifest.yaml: chat: node has no text_field", "broken-chat/manifest.yaml: chat: node has no text_field"},
		{"two paths /a/b/one and C:\\c\\d\\two\nand a newline /e/f/three", "two paths one and two\nand a newline three"},
		{"", ""},
	} {
		if got := Strict(tc.in); got != tc.want {
			t.Errorf("Strict(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHost_ReducesHostPathsAndKeepsRoutes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// A Unix host path under a well-known prefix.
		{"load diagnostic: open /home/zz-nobody/proj/.iterion/runs/r1/run.json: no such file or directory", "load diagnostic: open run.json: no such file or directory"},
		{"stat /var/lib/iterion/store/runs/r1: permission denied", "stat r1: permission denied"},
		{`read "/srv/zz-iterion/run.json" failed`, `read "run.json" failed`},
		// Windows drive paths (a user name no test host has, so the home root
		// does not apply), with and without spaces in a directory name,
		// either slash, and an OS error's colon after the path.
		{`load diagnostic: open C:\Users\zz-nobody\OneDrive\Рабочий стол\p\.iterion\runs\r1\run.json: The system cannot find the file specified.`, `load diagnostic: open run.json: The system cannot find the file specified.`},
		{`open c:/Users/x/run.json: no such file`, `open run.json: no such file`},
		{`share \\fileserver\iterion\store\r1 is gone`, `share r1 is gone`},
		// What an API error legitimately names is left alone.
		{"POST /api/v1/runs/r1/cancel is not allowed", "POST /api/v1/runs/r1/cancel is not allowed"},
		{"see https://example.com/home/docs for details", "see https://example.com/home/docs for details"},
		{"run \"r1\" not found", "run \"r1\" not found"},
		{"file not found: bots/revi/main.bot", "file not found: bots/revi/main.bot"},
		{"", ""},
	} {
		if got := Host(tc.in); got != tc.want {
			t.Errorf("Host(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHost_RegisteredRootBecomesRelativeAndSpacesSurvive: a path under the
// workspace keeps what is relative to it — the useful part of the message —
// and a space in a directory name does not leave the tail of the root behind.
func TestHost_RegisteredRootBecomesRelativeAndSpacesSurvive(t *testing.T) {
	AddRoots(`D:\Work Space\my proj`, "/opt/studio data/ws")
	for _, tc := range []struct{ in, want string }{
		{`open D:\Work Space\my proj\.iterion\runs\r1\run.json: missing`, `open .iterion\runs\r1\run.json: missing`},
		{`open d:/work space/my proj/.iterion/runs/r1/run.json: missing`, `open .iterion/runs/r1/run.json: missing`},
		{`workspace D:\Work Space\my proj is read-only`, `workspace . is read-only`},
		{`workspace "D:\Work Space\my proj": gone`, `workspace ".": gone`},
		{"open /opt/studio data/ws/bots/a.bot: nope", "open bots/a.bot: nope"},
		// A sibling that merely shares the root's prefix is not under it: it
		// is cut as an unregistered host path (to its first segment's end),
		// never read as the root's remainder.
		{"open /opt/studio data/ws2/x: nope", "open studio data/ws2/x: nope"},
	} {
		if got := Host(tc.in); got != tc.want {
			t.Errorf("Host(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAddRoots_IgnoresFilesystemRootsAndRelativePaths(t *testing.T) {
	AddRoots("", "/", `C:\`, "C:", "relative/dir")
	const msg = "GET /api/v1/runs and /x/y stay"
	if got := Host(msg); got != msg {
		t.Errorf("Host(%q) = %q after registering roots that must be ignored", msg, got)
	}
}
