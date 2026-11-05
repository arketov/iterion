package server

import (
	"testing"

	"github.com/arketov/iterion/pkg/runview"
)

func TestAggregateRepos(t *testing.T) {
	runs := []runview.RunSummary{
		{ID: "1", ProjectPath: "acme/widgets"},
		{ID: "2", ProjectPath: "acme/widgets"},
		{ID: "3", ProjectPath: "acme/gadgets"},
		{ID: "4", ProjectPath: ""}, // local/manual run — skipped
		{ID: "5", ProjectPath: "acme/gadgets"},
		{ID: "6", ProjectPath: "acme/gadgets"},
	}

	got := aggregateRepos(runs).Repos

	// Empty ProjectPath is skipped → two distinct repos.
	if len(got) != 2 {
		t.Fatalf("want 2 repos, got %d: %+v", len(got), got)
	}
	// Sorted by count desc: gadgets (3) before widgets (2).
	if got[0].ProjectPath != "acme/gadgets" || got[0].Count != 3 {
		t.Fatalf("repo[0] = %+v, want {acme/gadgets, 3}", got[0])
	}
	if got[1].ProjectPath != "acme/widgets" || got[1].Count != 2 {
		t.Fatalf("repo[1] = %+v, want {acme/widgets, 2}", got[1])
	}
}

func TestAggregateReposTieBreaksByPath(t *testing.T) {
	// Equal counts must sort by slug ascending so the chip order is stable.
	runs := []runview.RunSummary{
		{ID: "1", ProjectPath: "zeta/two"},
		{ID: "2", ProjectPath: "alpha/one"},
	}
	got := aggregateRepos(runs).Repos
	if len(got) != 2 || got[0].ProjectPath != "alpha/one" || got[1].ProjectPath != "zeta/two" {
		t.Fatalf("tie-break order wrong: %+v", got)
	}
}

func TestAggregateReposEmpty(t *testing.T) {
	got := aggregateRepos(nil).Repos
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
