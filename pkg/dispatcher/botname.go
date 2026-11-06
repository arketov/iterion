package dispatcher

import (
	"sort"

	"github.com/arketov/iterion/pkg/botregistry"
)

// lookupBotKey finds key in m the way routing resolves a bot name: an exact
// key wins, otherwise the first key whose botregistry.NormalizeName equals the
// normalized lookup name (so `My_Helper`, `my-helper` and `my helper` are one
// bot everywhere — the registry route, the static assignee_workflows map and
// the per-bot dispatch vars).
//
// Several keys can normalise to the same name (`my_helper` and `my-helper`
// side by side); Go's map iteration order would then pick one at random on
// every call. The lexicographically smallest key wins instead, so the answer
// is the same on every dispatch.
func lookupBotKey[V any](m map[string]V, key string) (V, bool) {
	var zero V
	if len(m) == 0 || key == "" {
		return zero, false
	}
	if v, ok := m[key]; ok {
		return v, true
	}
	nk := botregistry.NormalizeName(key)
	if nk == "" {
		return zero, false
	}
	var matches []string
	for k := range m {
		if botregistry.NormalizeName(k) == nk {
			matches = append(matches, k)
		}
	}
	if len(matches) == 0 {
		return zero, false
	}
	sort.Strings(matches)
	return m[matches[0]], true
}
