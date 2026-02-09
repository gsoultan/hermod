package sqlutil

import "encoding/json"

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
func ParseColumnMappings(s string) ([]ColumnMapping, error) {
	var mappings []ColumnMapping
	if s == "" {
		return mappings, nil
	}
	err := json.Unmarshal([]byte(s), &mappings)
	return mappings, err
}
