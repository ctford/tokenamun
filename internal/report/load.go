package report

import (
	"runtime"
	"sync"

	"github.com/ctford/tokenamun/internal/model"
)

// loaded is one ref's parse, or the error that stopped it.
type loaded struct {
	Session *model.Session
	Err     error
}

// loadEach parses every ref and returns one result per ref, in ref order.
//
// Concurrent, because parsing is where the time goes and each transcript is
// independent: a week is tens of megabytes of JSON and the work is one
// decode per file with nothing shared between them. On a synthetic 60
// session, 46 MB corpus it is 2.5x on a contended eight-core machine:
// median of eight runs, 666ms to 266ms. Less than the core count because
// the work allocates heavily and the collector is shared; more than that on
// a cold page cache, where the first run of a week's report also has 46 MB
// to read off the disk.
//
// The parse cache in front of this is not an alternative to it. That cache
// is keyed on the tool's own executable, so every rebuild empties it, and
// the cold path is what somebody working on this tool has every time. It is
// also what the first run of a week's report is, which is the run anybody
// notices.
//
// Bounded at GOMAXPROCS. The work is CPU-bound once the page cache is warm,
// and an unbounded fan-out over two hundred transcripts would hold two
// hundred decoders' buffers at once for no more throughput.
//
// Results are returned positionally rather than appended as they finish, so
// the order is the caller's and not a race. Every caller here sorts or bins
// afterwards, and both of those are stable: an order that varied run to run
// would make a golden file a coin toss.
//
// load must be safe to call from several goroutines. Both production
// implementations are -- ingest.Load opens its own file and shares nothing,
// and parsecache.Cache holds only a directory name and writes through a
// temporary file and a rename.
func loadEach(refs []model.SessionRef,
	load func(model.SessionRef) (*model.Session, error)) []loaded {
	out := make([]loaded, len(refs))
	limit := runtime.GOMAXPROCS(0)
	if limit > len(refs) {
		limit = len(refs)
	}
	if limit < 1 {
		return out
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := load(ref)
			out[i] = loaded{Session: s, Err: err}
		}()
	}
	wg.Wait()
	return out
}
