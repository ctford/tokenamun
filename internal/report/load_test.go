package report

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

func benchRefs(n int) []model.SessionRef {
	refs := make([]model.SessionRef, n)
	for i := range refs {
		refs[i] = model.SessionRef{
			ID: fmt.Sprintf("s%02d", i),
			// Descending, which is the order discover returns: Readable
			// sorts ascending afterwards, so a test that fed it sorted refs
			// would not notice the sort going away.
			Modified: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).
				Add(time.Duration(n-i) * time.Hour),
		}
	}
	return refs
}

func TestConcurrentLoadingKeepsTheCallersOrder(t *testing.T) {
	// Finishing order is deliberately the reverse of ref order: the first
	// ref is the slowest. Results appended as they complete would come back
	// backwards, and a golden file over them would be a coin toss.
	refs := benchRefs(12)
	got := loadEach(refs, func(ref model.SessionRef) (*model.Session, error) {
		for i, r := range refs {
			if r.ID == ref.ID {
				time.Sleep(time.Duration(len(refs)-i) * time.Millisecond)
			}
		}
		return &model.Session{Ref: ref}, nil
	})
	if len(got) != len(refs) {
		t.Fatalf("got %d results for %d refs", len(got), len(refs))
	}
	for i, r := range got {
		if r.Err != nil {
			t.Fatalf("%s: %v", refs[i].ID, r.Err)
		}
		if r.Session.Ref.ID != refs[i].ID {
			t.Errorf("position %d holds %q, want %q", i, r.Session.Ref.ID, refs[i].ID)
		}
	}
}

func TestAFailureStaysAttachedToTheSessionThatFailed(t *testing.T) {
	// The failure list names sessions, so a result landing at the wrong
	// index would blame a transcript that read perfectly well.
	refs := benchRefs(10)
	_, failed := Readable(refs, func(ref model.SessionRef) (*model.Session, error) {
		if ref.ID == "s03" || ref.ID == "s07" {
			return nil, fmt.Errorf("unreadable")
		}
		return &model.Session{Ref: ref}, nil
	})
	want := []string{"s03: unreadable", "s07: unreadable"}
	if len(failed) != len(want) {
		t.Fatalf("got %v, want %v", failed, want)
	}
	for i := range want {
		if failed[i] != want[i] {
			t.Errorf("failure %d = %q, want %q", i, failed[i], want[i])
		}
	}
}

func TestEverySessionIsLoadedExactlyOnce(t *testing.T) {
	// Under -race this is also the check that the loader shares nothing: the
	// counter is the only shared state and it is the test's, not the
	// loader's.
	refs := benchRefs(50)
	var mu sync.Mutex
	seen := map[string]int{}
	out, failed := Readable(refs, func(ref model.SessionRef) (*model.Session, error) {
		mu.Lock()
		seen[ref.ID]++
		mu.Unlock()
		return &model.Session{Ref: ref}, nil
	})
	if len(failed) != 0 || len(out) != len(refs) {
		t.Fatalf("got %d sessions and %d failures", len(out), len(failed))
	}
	for _, ref := range refs {
		if seen[ref.ID] != 1 {
			t.Errorf("%s loaded %d times, want 1", ref.ID, seen[ref.ID])
		}
	}
	// Readable's contract is oldest first, which several reports rely on.
	for i := 1; i < len(out); i++ {
		if out[i].Ref.Modified.Before(out[i-1].Ref.Modified) {
			t.Fatalf("result %d is older than the one before it", i)
		}
	}
}

func TestLoadingNothingIsNotADeadlock(t *testing.T) {
	if got := loadEach(nil, func(model.SessionRef) (*model.Session, error) {
		t.Error("nothing to load, so nothing should be loaded")
		return nil, nil
	}); len(got) != 0 {
		t.Errorf("got %d results for no refs", len(got))
	}
}
