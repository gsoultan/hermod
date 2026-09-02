package registry

import (
	"testing"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
)

// The engine reads the sequential flag from the workflow node
// (internal/engine/registry/nodes/core/sink.go:19), but the only UI control
// that sets it writes to the shared sink entity's config
// (ui/src/hooks/useSinkForm.ts). Nothing copied the value across, so the
// "Sequential Control Flow" feature documented in README.md could not be
// reached from the UI at all.
//
// resolveSinkNodeSequential closes that gap. Node config wins, because
// execution semantics belong to the workflow node rather than to a connection
// that may be shared by many workflows; the entity value is only a fallback so
// existing configurations keep working.
func TestResolveSinkNodeSequential(t *testing.T) {
	tests := []struct {
		name       string
		nodeType   string
		nodeConfig map[string]any
		entity     hermod.StringMap
		want       bool
		wantSet    bool
	}{
		{
			name:       "inherits true from sink entity when node is silent",
			nodeType:   "sink",
			nodeConfig: map[string]any{},
			entity:     hermod.StringMap{"sequential": "true"},
			want:       true,
			wantSet:    true,
		},
		{
			name:       "node config wins over entity",
			nodeType:   "sink",
			nodeConfig: map[string]any{"sequential": false},
			entity:     hermod.StringMap{"sequential": "true"},
			want:       false,
			wantSet:    true,
		},
		{
			name:       "node true is preserved when entity is silent",
			nodeType:   "sink",
			nodeConfig: map[string]any{"sequential": true},
			entity:     hermod.StringMap{},
			want:       true,
			wantSet:    true,
		},
		{
			name:       "both silent leaves the flag unset",
			nodeType:   "sink",
			nodeConfig: map[string]any{},
			entity:     hermod.StringMap{},
			wantSet:    false,
		},
		{
			name:       "entity false does not set the flag",
			nodeType:   "sink",
			nodeConfig: map[string]any{},
			entity:     hermod.StringMap{"sequential": "false"},
			want:       false,
			wantSet:    true,
		},
		{
			name:       "non-sink nodes are untouched",
			nodeType:   "transformation",
			nodeConfig: map[string]any{},
			entity:     hermod.StringMap{"sequential": "true"},
			wantSet:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &storage.WorkflowNode{Type: tc.nodeType, Config: tc.nodeConfig}

			resolveSinkNodeSequential(node, tc.entity)

			got, ok := node.Config["sequential"].(bool)
			if ok != tc.wantSet {
				t.Fatalf("sequential set = %v; want %v (config=%v)", ok, tc.wantSet, node.Config)
			}
			if tc.wantSet && got != tc.want {
				t.Errorf("sequential = %v; want %v", got, tc.want)
			}
		})
	}
}
