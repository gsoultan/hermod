package learn

import "math/rand/v2"

// newRand is the random source of one part of a training: a PCG seeded with
// the training's seed, on a stream of its own, so the same seed draws the
// same numbers every time.
func newRand(seed, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, stream)) //nolint:gosec // G404: seeded on purpose, so a training repeats; nothing relies on it being unpredictable.
}
