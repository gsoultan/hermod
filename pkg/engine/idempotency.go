package engine

import (
	"os"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

const idempotencyKeyMeta = "idempotency_key"

// DetermineIdempotencyKey returns a deterministic key for deduplication.
// Precedence:
// 1) metadata[idempotency_key]
// 2) message ID
// 3) empty string if none available (caller may decide behavior)
func DetermineIdempotencyKey(msg hermod.Message) string {
	if msg == nil {
		return ""
	}
	if md := msg.Metadata(); md != nil {
		if k := strings.TrimSpace(md[idempotencyKeyMeta]); k != "" {
			return k
		}
	}
	if id := strings.TrimSpace(msg.ID()); id != "" {
		return id
	}
	return ""
}

// EnsureIdempotencyID ensures the message has a stable ID set for sinks that
// use the message ID as the natural idempotency key (e.g., SQL primary key, etc.).
// Returns the key and whether it was set on the message.
func EnsureIdempotencyID(msg hermod.Message) (string, bool) {
	key := DetermineIdempotencyKey(msg)
	if key == "" {
		return "", false
	}
	if dm, ok := msg.(*message.DefaultMessage); ok {
		if strings.TrimSpace(dm.ID()) == "" {
			dm.SetID(key)
			return key, true
		}
	}
	return key, false
}

// IdempotencyRequired returns true if idempotency must be enforced (and missing
// keys should be reported). Controlled via HERMOD_IDEMPOTENCY_REQUIRED.
func IdempotencyRequired() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_IDEMPOTENCY_REQUIRED")))
	return v == "1" || v == "true" || v == "yes"
}
