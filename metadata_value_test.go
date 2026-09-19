package hermod

// Reading one metadata entry cloned the whole map.
//
// Metadata() copies under the read lock, and it has to: handing out the live
// map would race with a concurrent SetMetadata. But almost every caller wants
// one key — `msg.Metadata()["_outbox_id"]`, `["_source_node_id"]`,
// `[MetaDeliveredInline]` — and pays for a copy of every other entry to get
// it. Measured on a real workflow at 32 columns, those reads were 11% of
// everything the pipeline allocated.
//
// Three packages had already grown their own private version of this helper.
// This is the one definition, next to the interface it reads.

import (
	"maps"
	"strconv"
	"testing"
)

// fullMessage implements the single-key read.
type fullMessage struct {
	Message
	md map[string]string
}

func (m *fullMessage) Metadata() map[string]string { return maps.Clone(m.md) }
func (m *fullMessage) MetadataValue(key string) (string, bool) {
	v, ok := m.md[key]
	return v, ok
}

// plainMessage does not, which is what an out-of-tree implementation looks
// like. It must keep working.
type plainMessage struct {
	Message
	md map[string]string
}

func (m *plainMessage) Metadata() map[string]string { return maps.Clone(m.md) }

func metadataFixture(n int) map[string]string {
	md := make(map[string]string, n)
	for i := range n {
		md["key_"+strconv.Itoa(i)] = "value_" + strconv.Itoa(i)
	}
	md["traceparent"] = "tp"
	return md
}

func TestMetadataValueMatchesTheMapRead(t *testing.T) {
	md := metadataFixture(4)

	for _, tc := range []struct {
		name string
		msg  Message
	}{
		{"implements the single-key read", &fullMessage{md: md}},
		{"does not implement it", &plainMessage{md: md}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"traceparent", "key_0", "key_3", "absent", ""} {
				want, wantOK := tc.msg.Metadata()[key]
				got, gotOK := MetadataValue(tc.msg, key)
				if got != want || gotOK != wantOK {
					t.Errorf("MetadataValue(%q) = %q,%v; Metadata()[%q] = %q,%v",
						key, got, gotOK, key, want, wantOK)
				}
			}
		})
	}

	t.Run("nil message", func(t *testing.T) {
		if v, ok := MetadataValue(nil, "k"); v != "" || ok {
			t.Errorf("MetadataValue(nil) = %q,%v; want \"\",false", v, ok)
		}
	})
}

// The point of the change: reading one key must not get more expensive as the
// message carries more metadata.
func TestMetadataValueDoesNotScaleWithMapSize(t *testing.T) {
	few := &fullMessage{md: metadataFixture(2)}
	many := &fullMessage{md: metadataFixture(64)}

	fewAllocs := testing.AllocsPerRun(200, func() { MetadataValue(few, "traceparent") })
	manyAllocs := testing.AllocsPerRun(200, func() { MetadataValue(many, "traceparent") })

	if manyAllocs > fewAllocs {
		t.Errorf("MetadataValue allocates %v with 3 entries and %v with 65: the map is still being copied",
			fewAllocs, manyAllocs)
	}
	if manyAllocs != 0 {
		t.Errorf("MetadataValue allocated %v times; a map lookup should allocate nothing", manyAllocs)
	}
}
