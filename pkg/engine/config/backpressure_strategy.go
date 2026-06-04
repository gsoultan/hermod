package config

// BackpressureStrategy defines how to handle overflow when a sink is slow.
type BackpressureStrategy string

const (
	// BPBlock blocks the source until there is room in the buffer.
	BPBlock BackpressureStrategy = "block"
	// BPDropOldest drops the oldest message in the buffer to make room.
	BPDropOldest BackpressureStrategy = "drop_oldest"
	// BPDropNewest drops the incoming message if the buffer is full.
	BPDropNewest BackpressureStrategy = "drop_newest"
	// BPSampling drops the incoming message with a configured probability.
	BPSampling BackpressureStrategy = "sampling"
	// BPSpillToDisk writes the incoming message to disk if the buffer is full.
	BPSpillToDisk BackpressureStrategy = "spill_to_disk"
)
