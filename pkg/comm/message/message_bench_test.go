package message

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
)

func BenchmarkSanitizeValue(b *testing.B) {
	val := "test string"
	b.Run("string", func(b *testing.B) {
		for b.Loop() {
			SanitizeValue(val)
		}
	})

	u := uuid.New()
	b.Run("uuid", func(b *testing.B) {
		for b.Loop() {
			SanitizeValue(u)
		}
	})

	ptr := &val
	b.Run("ptr string", func(b *testing.B) {
		for b.Loop() {
			SanitizeValue(ptr)
		}
	})
}

func BenchmarkMessagePayload(b *testing.B) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetData("field1", "value1")
	m.SetData("field2", 123)
	m.SetData("field3", true)

	b.Run("First call (marshal)", func(b *testing.B) {
		for b.Loop() {
			m.payload = m.payload[:0] // Clear cache
			m.Payload()
		}
	})

	b.Run("Subsequent calls (cached)", func(b *testing.B) {
		m.Payload() // Warm up cache
		for b.Loop() {
			m.Payload()
		}
	})
}

func BenchmarkMessageSetData(b *testing.B) {
	m := AcquireMessage()
	defer ReleaseMessage(m)

	b.Run("simple key", func(b *testing.B) {
		var i int
		for b.Loop() {
			m.SetData("key", i)
			i++
		}
	})

	b.Run("nested key", func(b *testing.B) {
		var i int
		for b.Loop() {
			m.SetData("a.b.c", i)
			i++
		}
	})
}

// BenchmarkMessageClone measures the two shapes Clone actually sees: a flat row
// of scalars (most CDC rows) and a row carrying a nested jsonb document. Clone
// runs once per extra branch on every fan-out, so the flat case is the one that
// must not regress.
func BenchmarkMessageClone(b *testing.B) {
	b.Run("flat scalars", func(b *testing.B) {
		m := AcquireMessage()
		defer ReleaseMessage(m)
		m.SetID("row-1")
		m.SetData("id", 1)
		m.SetData("name", "value")
		m.SetData("active", true)
		m.SetMetadata("_hermod_workflow_id", "wf-1")

		b.ReportAllocs()
		for b.Loop() {
			c := m.Clone()
			c.Release()
		}
	})

	b.Run("nested document", func(b *testing.B) {
		m := AcquireMessage()
		defer ReleaseMessage(m)
		m.SetID("row-2")
		m.SetData("id", 2)
		m.SetData("doc", map[string]any{
			"status": "open",
			"labels": []any{"a", "b", "c"},
			"nested": map[string]any{"k": "v"},
		})
		m.SetMetadata("_hermod_workflow_id", "wf-1")

		b.ReportAllocs()
		for b.Loop() {
			c := m.Clone()
			c.Release()
		}
	})
}

// BenchmarkMessageToMap covers the snapshot path taken per message per node
// whenever tracing or the live viewer is on. ToMap deep-copies so the snapshot
// can outlive the caller safely; the JSON encode every caller then performs is
// the cost this has to stay small against, so it is measured alongside.
func BenchmarkMessageToMap(b *testing.B) {
	shapes := []struct {
		name string
		prep func(m *DefaultMessage)
	}{
		{"flat scalars", func(m *DefaultMessage) {
			m.SetData("id", 1)
			m.SetData("name", "value")
			m.SetData("active", true)
		}},
		{"nested document", func(m *DefaultMessage) {
			m.SetData("id", 2)
			m.SetData("doc", map[string]any{
				"status": "open",
				"labels": []any{"a", "b", "c"},
				"nested": map[string]any{"k": "v"},
			})
		}},
		{"cdc envelope", func(m *DefaultMessage) {
			m.SetOperation(hermod.OpUpdate)
			m.SetTable("orders")
			m.SetBefore([]byte(`{"id":"1","amount":"10.50"}`))
			m.SetAfter([]byte(`{"id":"1","amount":"11.50"}`))
		}},
	}

	for _, s := range shapes {
		m := AcquireMessage()
		s.prep(m)
		m.SetMetadata("_hermod_workflow_id", "wf-1")
		_ = m.DataRef()

		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = m.ToMap()
			}
		})
		b.Run(s.name+"/then encode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = json.Marshal(m.ToMap())
			}
		})
		ReleaseMessage(m)
	}
}
