package engine

// DefaultLagWarnBytes is how much un-acknowledged WAL a source may retain before
// it is reported. A replication slot that stops advancing pins WAL on the
// *source* database, so an unnoticed stall does not degrade Hermod — it fills
// the customer's primary and takes it down. 256 MiB is small enough to be a
// warning rather than an incident and large enough not to fire on a normal
// backlog.
const DefaultLagWarnBytes uint64 = 256 << 20

// lagState reports crossings of the retention threshold rather than the level,
// so a slot that sits above the line for an hour costs two log lines instead of
// one per health check.
type lagState struct {
	breached bool
}

// observe records a lag sample and reports the threshold crossings it causes.
// A zero threshold disables the check.
func (l *lagState) observe(lag, threshold uint64) (breached, cleared bool) {
	if threshold == 0 {
		return false, false
	}
	switch {
	case lag >= threshold && !l.breached:
		l.breached = true
		return true, false
	// Clear at half the threshold so a slot hovering on the line does not
	// alternate between states on every sample.
	case l.breached && lag < threshold/2:
		l.breached = false
		return false, true
	}
	return false, false
}
