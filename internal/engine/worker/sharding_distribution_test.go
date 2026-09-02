package worker

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

type storageWorker = storage.Worker

// TestRendezvousDistributesUnderUnequalLoad is the load-balancing contract that
// the existing sharding tests do not reach: they compare workers at *equal*
// load, where any hash difference decides the winner and the split looks fine.
//
// Rendezvous hashing is supposed to be proportional — with weights w1 and w2 a
// node should win roughly w1/(w1+w2) of the keys. If instead the weight term
// dominates the hash term completely, assignment becomes all-or-nothing: the
// slightly-less-loaded worker takes *every* workflow and the other takes none.
// That is not load balancing, it is a step function, and combined with the
// incumbent's hysteresis bonus it means an overloaded worker never sheds work
// to an idle peer — failback by load can never happen.
func TestRendezvousDistributesUnderUnequalLoad(t *testing.T) {
	const n = 200

	store := newHardeningStorage(0)
	w, _ := newHardenedWorker(store, "worker-2")

	online := newOnlineSet(0.05, 0.95) // worker-1 idle, worker-2 saturated

	wins := map[string]int{}
	for i := range n {
		best := w.calculateBestWorker(online, fmt.Sprintf("wf-%03d", i), "")
		wins[best]++
	}
	t.Logf("no incumbent, loads 0.05 vs 0.95: %v", wins)

	// The idle worker must be favoured, but a 1.2x weight ratio must not
	// produce a 100/0 split.
	if wins["worker-1"] == n || wins["worker-2"] == n {
		t.Errorf("assignment is all-or-nothing (%v): the weight term is swamping the "+
			"rendezvous hash, so load has become a hard switch instead of a bias", wins)
	}
	if wins["worker-1"] <= wins["worker-2"] {
		t.Errorf("the idle worker did not get the larger share: %v", wins)
	}
}

// TestRendezvousHysteresisStillAllowsFailback pins the other half of the
// contract. The 2x bonus for the current owner exists to stop workflows
// flapping between workers; it must not make the incumbent immovable, or a
// recovered worker never gets any load back.
func TestRendezvousHysteresisStillAllowsFailback(t *testing.T) {
	const n = 200

	store := newHardeningStorage(0)
	w, _ := newHardenedWorker(store, "worker-2")

	online := newOnlineSet(0.05, 0.95)

	wins := map[string]int{}
	for i := range n {
		best := w.calculateBestWorker(online, fmt.Sprintf("wf-%03d", i), "worker-2")
		wins[best]++
	}
	t.Logf("incumbent worker-2 (saturated) with hysteresis: %v", wins)

	if wins["worker-2"] == n {
		t.Errorf("a saturated incumbent kept every workflow (%v); an idle peer can never "+
			"reclaim load, so failback by load is impossible", wins)
	}
	// Hysteresis must still bias towards the incumbent, or workflows flap.
	if wins["worker-2"] == 0 {
		t.Errorf("hysteresis had no effect: the incumbent kept nothing (%v)", wins)
	}
}

// TestRendezvousIsStableForEqualLoad guards against over-correcting: with equal
// load the mapping must stay deterministic and evenly spread.
func TestRendezvousIsStableForEqualLoad(t *testing.T) {
	const n = 300

	store := newHardeningStorage(0)
	w, _ := newHardenedWorker(store, "worker-1")

	online := newOnlineSet(0.1, 0.1)

	first := map[string]string{}
	wins := map[string]int{}
	for i := range n {
		id := fmt.Sprintf("wf-%03d", i)
		best := w.calculateBestWorker(online, id, "")
		first[id] = best
		wins[best]++
	}
	t.Logf("equal load: %v", wins)

	// Deterministic: the same inputs must always produce the same owner.
	for i := range n {
		id := fmt.Sprintf("wf-%03d", i)
		if got := w.calculateBestWorker(online, id, ""); got != first[id] {
			t.Fatalf("assignment for %s is not deterministic: %s then %s", id, first[id], got)
		}
	}

	// Evenly spread: neither worker may take more than 70% at equal load.
	for id, c := range wins {
		if c > n*7/10 {
			t.Errorf("worker %s took %d/%d workflows at equal load; distribution is skewed", id, c, n)
		}
	}
}

// newOnlineSet builds two live worker entries with the given CPU/memory load.
func newOnlineSet(cpu1, cpu2 float64) []storageWorker {
	n1, n2 := time.Now(), time.Now()
	return []storageWorker{
		{ID: "worker-1", CPUUsage: cpu1, MemoryUsage: cpu1, LastSeen: &n1},
		{ID: "worker-2", CPUUsage: cpu2, MemoryUsage: cpu2, LastSeen: &n2},
	}
}

// TestLeaseHysteresisIsConfigurable proves the knob works in both directions.
// The default keeps workflows stable at the cost of very slow load rebalancing;
// an operator who would rather rebalance quickly needs a way to say so, and
// lowering the factor must measurably move work to the idle worker.
func TestLeaseHysteresisIsConfigurable(t *testing.T) {
	const n = 200

	store := newHardeningStorage(0)
	w, _ := newHardenedWorker(store, "worker-2")
	online := newOnlineSet(0.05, 0.95) // worker-1 idle, worker-2 saturated

	orig := leaseHysteresisFactor
	t.Cleanup(func() { leaseHysteresisFactor = orig })

	share := func(factor float64) int {
		leaseHysteresisFactor = factor
		reclaimed := 0
		for i := range n {
			if w.calculateBestWorker(online, fmt.Sprintf("wf-%03d", i), "worker-2") == "worker-1" {
				reclaimed++
			}
		}
		return reclaimed
	}

	atDefault := share(defaultLeaseHysteresis)
	atDisabled := share(1.0)

	t.Logf("idle worker reclaims %d/%d keys at hysteresis %.1f, %d/%d with it disabled",
		atDefault, n, defaultLeaseHysteresis, atDisabled, n)

	if atDisabled <= atDefault {
		t.Errorf("disabling hysteresis did not increase the idle worker's share: %d -> %d", atDefault, atDisabled)
	}
	// With no incumbent bonus the idle worker should take the clear majority.
	if atDisabled <= n/2 {
		t.Errorf("with hysteresis disabled the idle worker took only %d/%d keys; load is not being followed", atDisabled, n)
	}
	// And the default must still favour stability, or it is not doing its job.
	if atDefault > n/4 {
		t.Errorf("the default hysteresis let %d/%d keys move; workflows will flap", atDefault, n)
	}
}
