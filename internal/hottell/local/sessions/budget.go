package sessions

import (
	"context"
	"sync"
)

// readBudget is how many bytes of transcript content the reads in flight in one hottell-local
// may hold together. go-sdk runs tool calls in parallel without a bound, and a read holds its
// session in memory: without a shared budget a few parallel reads of large sessions take
// gigabytes of the Mac's memory (HT-381). Two reads at the bound of one fit.
const readBudget = 2 * maxDecoded

// reads is the budget the transcript reads of this process share.
//
//nolint:gochecknoglobals // one budget per process, as the memory it guards
var reads = newBudget(readBudget)

// budget is a weighted semaphore whose acquire gives up when its context ends.
type budget struct {
	mu      sync.Mutex
	size    int64
	used    int64
	changed chan struct{} // closed and replaced on every release
}

func newBudget(size int64) *budget {
	return &budget{size: size, changed: make(chan struct{})}
}

// acquire takes w bytes of the budget, waiting while the reads in flight hold too much; w above
// the whole budget waits for all of it. A cancelled ctx stops the wait with its error.
func (b *budget) acquire(ctx context.Context, w int64) error {
	w = min(max(w, 1), b.size)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		if b.used+w <= b.size {
			b.used += w
			b.mu.Unlock()
			return nil
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// release returns w bytes taken by acquire.
func (b *budget) release(w int64) {
	w = min(max(w, 1), b.size)
	b.mu.Lock()
	b.used -= w
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}

// inUse is how many bytes the reads in flight hold.
func (b *budget) inUse() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// readWeight is what a read of si takes of the budget: a plain file its size, a compressed
// rollout the whole bound of one read — how far it unpacks is unknown until it has been read,
// and an estimate from the packed size can be off a thousandfold — and never more than limit
// when the read stops earlier.
func readWeight(si Info, limit int64) int64 {
	bound := si.limits.or().decoded
	if si.Compressed {
		return min(bound, limit)
	}
	return min(si.Size, bound, limit)
}
