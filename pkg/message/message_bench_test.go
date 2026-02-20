package message

import (
	"github.com/google/uuid"
	"testing"
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
