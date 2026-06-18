package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresSink_ConvertValue(t *testing.T) {
	s := &PostgresSink{}

	tests := []struct {
		name     string
		val      any
		dataType string
		expected any
	}{
		{
			name:     "JSONB from map",
			val:      map[string]any{"foo": "bar"},
			dataType: "JSONB",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "JSONB from string",
			val:      `{"foo":"bar"}`,
			dataType: "JSONB",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "UUID from string",
			val:      "550e8400-e29b-41d4-a716-446655440000",
			dataType: "UUID",
			expected: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		},
		{
			name:     "INT from string",
			val:      "123",
			dataType: "INTEGER",
			expected: int64(123),
		},
		{
			name:     "BOOL from string",
			val:      "true",
			dataType: "BOOLEAN",
			expected: true,
		},
		{
			name:     "FLOAT from string",
			val:      "123.45",
			dataType: "NUMERIC",
			expected: 123.45,
		},
		{
			name:     "TIMESTAMP from string",
			val:      "2023-01-01T12:00:00Z",
			dataType: "TIMESTAMP",
			expected: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "Nil value",
			val:      nil,
			dataType: "TEXT",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.convertValue(tt.val, tt.dataType)

			if tt.dataType == "JSONB" && tt.val != nil {
				// Special check for JSON as map order is non-deterministic
				if _, ok := tt.val.(map[string]any); ok {
					var m1, m2 map[string]any
					json.Unmarshal([]byte(got.(string)), &m1)
					json.Unmarshal([]byte(tt.expected.(string)), &m2)
					// Simple check
					if len(m1) != len(m2) {
						t.Errorf("convertValue() = %v, want %v", got, tt.expected)
					}
					return
				}
			}

			if got != tt.expected {
				// Handle time.Time comparison
				if t1, ok := got.(time.Time); ok {
					if t2, ok := tt.expected.(time.Time); ok {
						if t1.Equal(t2) {
							return
						}
					}
				}
				t.Errorf("convertValue() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPostgresSink_CloseIsIdempotent(t *testing.T) {
	// Close must be safe to call when no pool was ever created (e.g. a failed
	// test connection) and safe to call repeatedly, without panicking or
	// leaking a closed pool reference.
	s := NewPostgresSink("postgres://user:pass@localhost:5432/db", "t", nil, false, "", "", "", "auto", false, false)

	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if s.pool != nil {
		t.Errorf("pool not reset to nil after Close: got %v", s.pool)
	}
}
