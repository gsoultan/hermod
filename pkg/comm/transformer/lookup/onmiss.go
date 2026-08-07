package lookup

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/user/hermod/pkg/comm/transformer/core"
)

// LookupMisses counts lookups that produced no value, whatever the configured
// policy. A pipeline can be healthy by every other measure while quietly
// enriching nothing; this is the number that says so.
var LookupMisses atomic.Int64

// missPolicy decides what happens when a lookup cannot produce a value.
//
// Every failure used to be silent success: a key path that resolved to nil, an
// absent config, a row that was not found — all returned the message unchanged
// with a nil error, so an unenriched record reached the sink looking exactly
// like an enriched one. For an enrichment step that is the worst available
// default, because nothing downstream can tell the two apart.
type missPolicy int

const (
	// missPassthrough emits the message unchanged. The historical behaviour,
	// kept because it is legitimately what some pipelines want — now chosen
	// rather than assumed.
	missPassthrough missPolicy = iota
	// missDefault writes defaultValue, so the gap is visible in the data.
	missDefault
	// missFail returns an error, so the message follows the normal retry and
	// dead-letter path instead of being quietly degraded.
	missFail
)

func (p missPolicy) String() string {
	switch p {
	case missDefault:
		return "default"
	case missFail:
		return "fail"
	default:
		return "passthrough"
	}
}

// resolveMissPolicy reads the onMiss setting, falling back to behaviour that
// cannot surprise an existing pipeline: a configured defaultValue implies the
// author wanted misses filled in, and anything else stays passthrough.
//
// An unrecognised value falls back rather than erroring, because refusing to
// start a workflow over a typo in an optional field is worse than the typo.
func resolveMissPolicy(config map[string]any, hasDefault bool) missPolicy {
	switch strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "onMiss"))) {
	case "fail":
		return missFail
	case "default":
		return missDefault
	case "passthrough":
		return missPassthrough
	}
	if hasDefault {
		return missDefault
	}
	return missPassthrough
}

// applyMissPolicy carries out the configured decision for a lookup that
// produced no value, and counts the miss either way so silent degradation shows
// up as a number even when the pipeline is configured to continue.
//
// It returns the error to propagate, or nil to continue.
func applyMissPolicy(msg interface{ SetData(string, any) }, p missPolicy, targetField, defaultValue string, cause error) error {
	LookupMisses.Add(1)
	switch p {
	case missFail:
		return cause
	case missDefault:
		if defaultValue != "" && targetField != "" {
			msg.SetData(targetField, defaultValue)
		}
		return nil
	default:
		return nil
	}
}

// missError describes a lookup that produced nothing, naming the table and key
// path so an operator can tell which of several lookups in a workflow failed.
func missError(table, keyField string, keyVal any) error {
	if keyVal == nil {
		return fmt.Errorf("db_lookup: no value for key path %q against table %q (the path resolved to nothing on this message)", keyField, table)
	}
	return fmt.Errorf("db_lookup: no row in table %q for key path %q = %v", table, keyField, keyVal)
}
