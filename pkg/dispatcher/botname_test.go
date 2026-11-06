package dispatcher

import "testing"

func TestLookupBotKey(t *testing.T) {
	m := map[string]string{"feature-dev": "a", "my_helper": "b", "my-helper": "c", "Zed": "d"}

	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{"feature-dev", "a", true}, // exact
		{"Feature_Dev", "a", true}, // case and `_` folded like routing
		{"feature dev", "a", true}, // spaces too
		{"my_helper", "b", true},   // exact wins over the normalized sibling
		{"my-helper", "c", true},   // exact
		{"MY_HELPER", "c", true},   // two normalized matches: the smallest key (my-helper) wins, every time
		{"zed", "d", true},         // normalized against a capitalised key
		{"ghost", "", false},       // no bot
		{"", "", false},            // empty
	}
	for _, tc := range cases {
		// Repeat: a map-order-dependent answer would flip between runs.
		for i := 0; i < 20; i++ {
			got, ok := lookupBotKey(m, tc.key)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("lookupBotKey(%q) = %q,%v; want %q,%v", tc.key, got, ok, tc.want, tc.ok)
			}
		}
	}
	if _, ok := lookupBotKey[string](nil, "x"); ok {
		t.Fatal("a nil map has no key")
	}
}
