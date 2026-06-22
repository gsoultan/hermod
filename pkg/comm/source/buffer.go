package source

// DefaultSourceBuffer is the capacity used for the per-source message channel
// that connectors place decoded change events onto.
//
// It is intentionally small: a shallow buffer absorbs short bursts while
// applying backpressure to the producer (the upstream database/stream reader)
// as soon as the downstream sink lags, instead of hoarding up to a thousand
// fully-decoded messages in RAM per active pipeline. With multi-KB payloads
// and several concurrent pipelines, deep buffers were a primary driver of the
// resident set; bounding them keeps Hermod within its lightweight footprint.
const DefaultSourceBuffer = 64
