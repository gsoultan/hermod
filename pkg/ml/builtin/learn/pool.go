package learn

import (
	"runtime"
	"sync"
)

// threadCap is how many goroutines a network trains on: the threads asked
// for, or all the CPUs Go may use but one, and never more than workers (the
// engine's cap, 0 for none) or fewer than one.
func threadCap(requested, workers int) int {
	t := requested
	if t <= 0 {
		t = runtime.GOMAXPROCS(0) - 1
	}
	if workers > 0 {
		t = min(t, workers)
	}
	return max(1, t)
}

// minWorkPerThread is the least work, in multiply-adds, worth another
// goroutine: about half a millisecond. Below it, starting and joining one
// costs more CPU than it saves time (measured on ml/bench/gomlp: batches of
// 32 rows trained 5% faster on three goroutines than on one, for 30% more
// CPU).
const minWorkPerThread = 1 << 20

// pool runs a loop's iterations on at most threads goroutines.
type pool struct{ threads int }

func newPool(threads int) *pool { return &pool{threads: max(1, threads)} }

// run calls f over [0, n) in at most p.threads contiguous ranges at once,
// one per minWorkPerThread of work (the multiply-adds the n items take, all
// told), and returns when every range is done. With one range it runs on
// the caller's goroutine.
func (p *pool) run(n, work int, f func(lo, hi int)) {
	t := min(p.threads, n, work/minWorkPerThread)
	if t <= 1 {
		if n > 0 {
			f(0, n)
		}
		return
	}
	var wg sync.WaitGroup
	for k := 1; k < t; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(k*n/t, (k+1)*n/t)
		}()
	}
	f(0, n/t)
	wg.Wait()
}
