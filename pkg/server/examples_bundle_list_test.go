package server

import (
	"os"
	"path/filepath"
	"testing"
)

// A bundle whose manifest declares no display_name is still listed by
// /api/examples (the Bots page lists it, so the Home panel must too); a bare
// .bot file and a main.bot without a manifest are not.
func TestFirstClassBotAcceptsABundleWithoutAPersona(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	plain := write("foo/main.bot", "workflow w {}")
	write("foo/manifest.yaml", "name: foo\ndescription: A plain bot.\n")
	persona := write("bar/main.bot", "workflow w {}")
	write("bar/manifest.yaml", "name: bar\ndisplay_name: Barry\n")
	noManifest := write("baz/main.bot", "workflow w {}")

	if p, d, ok := firstClassBot(plain); !ok || p != "" || d == "" {
		t.Fatalf("persona-less bundle: persona=%q desc=%q ok=%v", p, d, ok)
	}
	if p, _, ok := firstClassBot(persona); !ok || p != "Barry" {
		t.Fatalf("personified bundle: persona=%q ok=%v", p, ok)
	}
	if _, _, ok := firstClassBot(noManifest); ok {
		t.Fatal("a main.bot without a manifest is not a bundle bot")
	}
}
