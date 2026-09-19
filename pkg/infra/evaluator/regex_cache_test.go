package evaluator

// A `regex` router condition called regexp.Compile once per message. Measured
// on Apple M5 Pro: 1931ns and 69 allocations per message against 208ns and 1
// with the pattern compiled once — so a single regex filter costs roughly
// 0.2 of a core and 600 MB/s of garbage at 100k msgs/s.
//
// The pattern is not necessarily static. EvaluateConditions resolves a value
// containing {{ }} against the message's own data before using it, so the
// pattern can be derived from message content — which is why the cache is
// bounded and evicts, rather than being a plain map that grows.

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func regexCondMsg(t *testing.T, field, value string) *message.DefaultMessage {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	m.SetData(field, value)
	return m
}

// TestRegexConditionsBehaviourUnchanged pins the answers, including the two
// that are easy to get wrong when a compile result starts being reused: an
// invalid pattern, and a pattern that compiled fine but does not match.
func TestRegexConditionsBehaviourUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		op      string
		pattern string
		subject string
		want    bool
	}{
		{"regex matches", "regex", `^a.c$`, "abc", true},
		{"regex does not match", "regex", `^a.c$`, "xbc", false},
		{"regex anchored partial", "regex", `b`, "abc", true},
		{"not_regex matches", "not_regex", `^a.c$`, "xbc", true},
		{"not_regex does not match", "not_regex", `^a.c$`, "abc", false},
		// An invalid pattern leaves match false, so the condition rejects
		// everything. That is the existing behaviour and this change must not
		// alter it -- but see the review note: it is silent, and a typo in a
		// filter drops 100% of traffic with nothing logged.
		{"invalid pattern rejects", "regex", `[unclosed`, "abc", false},
		{"invalid not_regex rejects", "not_regex", `[unclosed`, "abc", false},
		{"empty pattern matches anything", "regex", ``, "abc", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := regexCondMsg(t, "f", tc.subject)
			conds := []map[string]any{{"field": "f", "operator": tc.op, "value": tc.pattern}}
			if got := EvaluateConditions(msg, conds); got != tc.want {
				t.Errorf("EvaluateConditions(%s %q against %q) = %v, want %v",
					tc.op, tc.pattern, tc.subject, got, tc.want)
			}
			// Twice, because the second evaluation is the one that takes the
			// cached path.
			if got := EvaluateConditions(msg, conds); got != tc.want {
				t.Errorf("second evaluation disagreed with the first: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRegexConditionCompilesPatternOnce is the point of the change.
func TestRegexConditionCompilesPatternOnce(t *testing.T) {
	msg := regexCondMsg(t, "email", "someone@example.com")
	conds := []map[string]any{{
		"field":    "email",
		"operator": "regex",
		"value":    `^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`,
	}}

	// Warm, so the measurement is of the steady state rather than of the first
	// compile.
	EvaluateConditions(msg, conds)

	allocs := testing.AllocsPerRun(200, func() { EvaluateConditions(msg, conds) })
	if allocs > 10 {
		t.Errorf("a regex condition allocates %v times per message; a compiled pattern should be reused (it was 69)", allocs)
	}
}

// TestRegexCacheIsBounded: the pattern can come from message data, so an
// unbounded cache is a map keyed by attacker-supplied input.
func TestRegexCacheIsBounded(t *testing.T) {
	msg := regexCondMsg(t, "f", "abc")

	for i := range maxCachedPatterns * 3 {
		conds := []map[string]any{{
			"field":    "f",
			"operator": "regex",
			"value":    fmt.Sprintf("^abc%d?$", i),
		}}
		EvaluateConditions(msg, conds)
	}

	if n := cachedPatternCount(); n > maxCachedPatterns {
		t.Errorf("pattern cache holds %d entries with a cap of %d: a pattern derived from message data can grow it without bound",
			n, maxCachedPatterns)
	}
}

// A pattern is compiled with a size limit in mind; an absurdly long one should
// not be cached forever either. This mostly documents that a huge pattern is
// still handled without blowing up.
func TestRegexConditionHandlesHugePattern(t *testing.T) {
	msg := regexCondMsg(t, "f", "abc")
	huge := "^(" + strings.Repeat("a|", 5000) + "b)$"
	conds := []map[string]any{{"field": "f", "operator": "regex", "value": huge}}

	// Whatever it answers, it must answer, and twice.
	first := EvaluateConditions(msg, conds)
	if second := EvaluateConditions(msg, conds); first != second {
		t.Errorf("a huge pattern evaluated to %v then %v", first, second)
	}
}

// The cache is shared mutable state on the per-message path of every workflow
// in the process, so it is exercised concurrently, including across the
// eviction that a bounded cache has to perform.
func TestRegexCacheUnderConcurrency(t *testing.T) {
	const goroutines = 16
	const perGoroutine = 400

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetData("f", "abc")

			for i := range perGoroutine {
				// A mix of a hot shared pattern, per-goroutine patterns, and
				// enough distinct ones to force eviction while others read.
				var pattern string
				var want bool
				switch i % 3 {
				case 0:
					pattern, want = `^abc$`, true
				case 1:
					pattern, want = fmt.Sprintf(`^abc(%d)?$`, g), true
				default:
					pattern, want = fmt.Sprintf(`^zzz%d$`, i), false
				}
				conds := []map[string]any{{"field": "f", "operator": "regex", "value": pattern}}
				if got := EvaluateConditions(msg, conds); got != want {
					t.Errorf("pattern %q against \"abc\" = %v, want %v", pattern, got, want)
					return
				}
			}
		})
	}
	wg.Wait()

	if n := cachedPatternCount(); n > maxCachedPatterns {
		t.Errorf("cache holds %d entries after concurrent use, cap is %d", n, maxCachedPatterns)
	}
}
