package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/arketov/iterion/pkg/knowledge"
)

func quotaStore(t *testing.T) *FSStore {
	t.Helper()
	t.Setenv("ITERION_HOME", t.TempDir())
	return DefaultFSStore()
}

// botRef builds the SpaceRef a legacy `memory: scope:` block resolves to.
// LegacyBotRef maps that legacy path to PROJECT visibility (true per-bot
// isolation is an explicit visibility: bot space), so the per-space quota
// for these refs is keyed by ITERION_MEMORY_QUOTA_PROJECT.
func botRef(scope string) knowledge.SpaceRef { return LegacyBotRef("/tmp/proj", scope) }

func bytesOf(n int) []byte { return bytes.Repeat([]byte("x"), n) }

func TestFSStore_RejectsOversizeDocument(t *testing.T) {
	t.Setenv("ITERION_MEMORY_MAX_DOC", "5")
	s := quotaStore(t)
	ctx := context.Background()
	ref := botRef("notes")
	_, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "big.md", Content: bytesOf(10)})
	if !errors.Is(err, knowledge.ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if _, rerr := s.ReadDocument(ctx, ref, "big.md"); !errors.Is(rerr, knowledge.ErrDocNotFound) {
		t.Fatalf("oversize doc must not be written, read err=%v", rerr)
	}
}

func TestFSStore_PerSpaceQuota(t *testing.T) {
	t.Setenv("ITERION_MEMORY_QUOTA_PROJECT", "30")
	s := quotaStore(t)
	ctx := context.Background()
	ref := botRef("notes")
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "a.md", Content: bytesOf(20)}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	used, quota, _ := s.UsageBytes(ctx, ref)
	if used != 20 || quota != 30 {
		t.Fatalf("usage=%d quota=%d, want 20/30", used, quota)
	}
	// 20 + 15 = 35 > 30: per-space cap, NOT the org aggregate (default 1GiB).
	_, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "b.md", Content: bytesOf(15)})
	if !errors.Is(err, knowledge.ErrQuotaExceeded) || errors.Is(err, knowledge.ErrOrgQuotaExceeded) {
		t.Fatalf("want per-space ErrQuotaExceeded, got %v", err)
	}
	if used, _, _ := s.UsageBytes(ctx, ref); used != 20 {
		t.Fatalf("rejected write changed usage: %d", used)
	}
}

func TestFSStore_OrgAggregateQuota(t *testing.T) {
	t.Setenv("ITERION_MEMORY_QUOTA_ORG_TOTAL", "30") // per-space stays at the 256MiB project default
	s := quotaStore(t)
	ctx := context.Background()
	refA, refB := botRef("a"), botRef("b")
	if _, err := s.WriteDocument(ctx, refA, knowledge.DocumentInput{Path: "x.md", Content: bytesOf(20)}); err != nil {
		t.Fatalf("write A: %v", err)
	}
	// refB has its own roomy sub-cap, but the org aggregate (20+15>30) trips.
	_, err := s.WriteDocument(ctx, refB, knowledge.DocumentInput{Path: "y.md", Content: bytesOf(15)})
	if !errors.Is(err, knowledge.ErrOrgQuotaExceeded) {
		t.Fatalf("want ErrOrgQuotaExceeded, got %v", err)
	}
}

func TestFSStore_ConcurrentWritersSumCorrectly(t *testing.T) {
	s := quotaStore(t)
	ctx := context.Background()
	ref := botRef("notes")
	const n, sz = 16, 100
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: fmt.Sprintf("doc-%02d.md", i), Content: bytesOf(sz)})
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("writer %d: %v", i, e)
		}
	}
	used, _, _ := s.UsageBytes(ctx, ref)
	if used != int64(n*sz) {
		t.Fatalf("space used=%d want=%d (counter drift under concurrency)", used, n*sz)
	}
}

func TestFSStore_DeleteFreesQuota(t *testing.T) {
	t.Setenv("ITERION_MEMORY_QUOTA_PROJECT", "30")
	s := quotaStore(t)
	ctx := context.Background()
	ref := botRef("notes")
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "a.md", Content: bytesOf(25)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "b.md", Content: bytesOf(10)}); !errors.Is(err, knowledge.ErrQuotaExceeded) {
		t.Fatalf("want quota exceeded with only 5 bytes free, got %v", err)
	}
	if err := s.DeleteDocument(ctx, ref, "a.md"); err != nil {
		t.Fatal(err)
	}
	if used, _, _ := s.UsageBytes(ctx, ref); used != 0 {
		t.Fatalf("after delete used=%d want 0", used)
	}
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "b.md", Content: bytesOf(10)}); err != nil {
		t.Fatalf("write after delete should fit: %v", err)
	}
}

func TestFSStore_OverwriteAdjustsUsage(t *testing.T) {
	s := quotaStore(t)
	ctx := context.Background()
	ref := botRef("notes")
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "a.md", Content: bytesOf(100)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteDocument(ctx, ref, knowledge.DocumentInput{Path: "a.md", Content: bytesOf(40)}); err != nil {
		t.Fatal(err)
	}
	if used, _, _ := s.UsageBytes(ctx, ref); used != 40 {
		t.Fatalf("overwrite usage=%d want 40", used)
	}
}
