package message

// The engine needs a payload's size far more often than it needs the payload:
// the batch-by-bytes accounting adds it up for every message, and the
// per-write debug log reports it. Both went through Payload(), which clones.
// At 50k messages that was 1.86 GB and 0.94 GB of garbage respectively —
// 57% of everything the engine allocated — to produce two integers.
//
// PayloadLen has to be exactly len(Payload()) or the batching threshold moves,
// including on a message whose payload has not been materialised from its data
// map yet.

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

func TestPayloadLenMatchesPayload(t *testing.T) {
	cases := []struct {
		name  string
		build func(*DefaultMessage)
	}{
		{"empty", func(*DefaultMessage) {}},
		{"payload set", func(m *DefaultMessage) { m.SetPayload([]byte(`{"a":1}`)) }},
		{"non-json payload", func(m *DefaultMessage) { m.SetPayload([]byte("plain text body")) }},
		{"large payload", func(m *DefaultMessage) { m.SetPayload([]byte(strings.Repeat("x", 64<<10))) }},
		{"data only", func(m *DefaultMessage) { m.SetData("a", 1); m.SetData("b", "two") }},
		{"cdc after image", func(m *DefaultMessage) {
			m.SetOperation(hermod.OpCreate)
			m.SetData("after", map[string]any{"id": 7})
		}},
		{"payload then data", func(m *DefaultMessage) {
			m.SetPayload([]byte(`{"a":1}`))
			m.SetData("b", 2)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Two messages built identically: reading one with Payload() caches
			// the marshalled bytes, so asking the same message both ways would
			// hide a PayloadLen that only works on an already-materialised
			// message.
			a, b := AcquireMessage(), AcquireMessage()
			defer ReleaseMessage(a)
			defer ReleaseMessage(b)
			tc.build(a)
			tc.build(b)

			if got, want := b.PayloadLen(), len(a.Payload()); got != want {
				t.Errorf("PayloadLen() = %d, len(Payload()) = %d", got, want)
			}
		})
	}
}

func TestPayloadLenDoesNotCopyPayload(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetPayload([]byte(strings.Repeat("x", 64<<10)))

	// Materialise first so neither call has marshalling to do; what is left is
	// the copy Payload() makes and PayloadLen() must not.
	_ = m.Payload()

	if allocs := testing.AllocsPerRun(100, func() { _ = m.PayloadLen() }); allocs != 0 {
		t.Errorf("PayloadLen() allocated %v times; it must not copy the payload", allocs)
	}
}
