package runview

import (
	"testing"

	"github.com/arketov/iterion/pkg/store"
)

func TestMatchesFilterRepo(t *testing.T) {
	mk := func(slug string) *store.Run {
		return &store.Run{ID: "r", ProjectPath: slug, Status: store.RunStatusFinished}
	}

	cases := []struct {
		name   string
		run    *store.Run
		filter ListFilter
		want   bool
	}{
		{"empty filter matches any repo", mk("acme/widgets"), ListFilter{}, true},
		{"empty filter matches no-repo run", mk(""), ListFilter{}, true},
		{"exact repo matches", mk("acme/widgets"), ListFilter{Repo: "acme/widgets"}, true},
		{"different repo rejected", mk("acme/gadgets"), ListFilter{Repo: "acme/widgets"}, false},
		{"repo filter rejects no-repo run", mk(""), ListFilter{Repo: "acme/widgets"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesFilter(tc.run, tc.filter); got != tc.want {
				t.Fatalf("matchesFilter(%q, repo=%q) = %v, want %v",
					tc.run.ProjectPath, tc.filter.Repo, got, tc.want)
			}
		})
	}
}
