package registry

import (
	"sync"

	"github.com/user/hermod"
)

// lastDeliveredSamples caches the most recent message that each source
// successfully delivered downstream, keyed by source ID.
//
// Streaming/consuming sources (Kafka, MQTT, NATS, RabbitMQ, Redis, gRPC,
// WebSocket, ...) are drained by the live workflow consumer: every available
// record is held in that consumer's in-flight/unacked buffer, so a fresh,
// passive Sample connection finds nothing and returns an empty sample. By
// recording the latest record that was actually forwarded to the next node,
// SampleTable can surface real, non-empty sample data whenever a source is
// successfully delivering data, regardless of the source type.
//
// The cache is process-local and only ever keeps the single latest message per
// source ID, so its memory footprint is bounded by the number of distinct
// configured sources.
var lastDeliveredSamples sync.Map // map[string]hermod.Message

// recordDeliveredSample stores a clone of the latest message delivered by the
// given source. It is safe for concurrent use.
func recordDeliveredSample(sourceID string, msg hermod.Message) {
	if sourceID == "" || msg == nil {
		return
	}
	old, swapped := lastDeliveredSamples.Swap(sourceID, msg.Clone())
	if swapped {
		if oldMsg, ok := old.(hermod.Message); ok && oldMsg != nil {
			oldMsg.Release()
		}
	}
}

// loadDeliveredSample returns a clone of the latest delivered message for the
// given source, if one has been observed.
func loadDeliveredSample(sourceID string) (hermod.Message, bool) {
	if sourceID == "" {
		return nil, false
	}
	v, ok := lastDeliveredSamples.Load(sourceID)
	if !ok {
		return nil, false
	}
	msg, ok := v.(hermod.Message)
	if !ok || msg == nil {
		return nil, false
	}
	return msg.Clone(), true
}
