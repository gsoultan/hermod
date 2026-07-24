package sqlutil

import (
	"encoding/json"
	"strings"
)

// ColumnMapping defines how a source field maps to a sink column.
type ColumnMapping struct {
	SourceField  string `json:"source_field"`
	TargetColumn string `json:"target_column"`
	DataType     string `json:"data_type"`      // Optional, used for auto-creation
	IsPrimaryKey bool   `json:"is_primary_key"` // Optional
	IsNullable   bool   `json:"is_nullable"`    // Optional
	IsIdentity   bool   `json:"is_identity"`    // Optional, auto-increment/sequence
}

// ParseColumnMappings parses a JSON string into a slice of ColumnMapping.
//
// A blank source_field is treated as "use the column's own name": it falls back
// to target_column. Without this normalization an empty source_field resolves to
// a nil value at write time (evaluator.GetMsgValByPath returns nil for an empty
// path), so every such column is bound as NULL and rows fail on NOT NULL / PRIMARY
// KEY constraints. UIs commonly leave source_field blank to mean "same name as
// the target column", so this default makes that intent work safely.
func ParseColumnMappings(s string) ([]ColumnMapping, error) {
	var mappings []ColumnMapping
	if s == "" {
		return mappings, nil
	}
	if err := json.Unmarshal([]byte(s), &mappings); err != nil {
		return nil, err
	}
	for i := range mappings {
		mappings[i].SourceField = strings.TrimSpace(mappings[i].SourceField)
		if mappings[i].SourceField == "" {
			mappings[i].SourceField = mappings[i].TargetColumn
		}
	}
	return mappings, nil
}
