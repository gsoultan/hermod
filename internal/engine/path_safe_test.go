package engine

import (
	"reflect"
	"testing"
)

func TestPathSafeImplementation(t *testing.T) {
	t.Run("getValByPath", func(t *testing.T) {
		data := map[string]any{
			"user": map[string]any{
				"profile": map[string]any{
					"name": "John",
				},
				"tags": []any{"a", "b", "c"},
			},
		}

		tests := []struct {
			path     string
			expected any
		}{
			{"user.profile.name", "John"},
			{"user.tags.1", "b"},
			{"user.tags.#", float64(3)},
			{"nonexistent", nil},
			{"user.profile.age", nil},
		}

		for _, tt := range tests {
			got := getValByPath(data, tt.path)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getValByPath(data, %q) = %v; want %v", tt.path, got, tt.expected)
			}
		}
	})

	t.Run("setValByPath", func(t *testing.T) {
		data := map[string]any{
			"user": map[string]any{
				"name": "John",
			},
		}

		// Simple set
		setValByPath(data, "user.age", 30)
		if getValByPath(data, "user.age") != float64(30) {
			t.Errorf("Expected age 30, got %v", getValByPath(data, "user.age"))
		}

		// Deep set with creation
		setValByPath(data, "meta.info.source", "web")
		if getValByPath(data, "meta.info.source") != "web" {
			t.Errorf("Expected meta.info.source 'web', got %v", getValByPath(data, "meta.info.source"))
		}

		// Array append
		setValByPath(data, "user.tags", []any{"tag1"})
		setValByPath(data, "user.tags.-1", "tag2")

		tags := getValByPath(data, "user.tags").([]any)
		if len(tags) != 2 || tags[1] != "tag2" {
			t.Errorf("Expected tags append, got %v", tags)
		}
	})
}
