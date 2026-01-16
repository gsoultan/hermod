package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestAdvancedTransformerProfessional(t *testing.T) {
	tests := []struct {
		name     string
		mapping  map[string]string
		source   map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "Conditional Logic - IF",
			mapping: map[string]string{
				"status": "if(gt(source.price, const.100), expensive, cheap)",
			},
			source: map[string]interface{}{"price": 150},
			expected: map[string]interface{}{
				"status": "expensive",
			},
		},
		{
			name: "Math - Add and Mul",
			mapping: map[string]string{
				"total": "mul(add(source.p1, source.p2), const.1.1)",
			},
			source: map[string]interface{}{"p1": 10, "p2": 20},
			expected: map[string]interface{}{
				"total": 33.0,
			},
		},
		{
			name: "Logical - And Or",
			mapping: map[string]string{
				"is_valid": "and(gt(source.age, const.18), or(eq(source.role, admin), eq(source.role, editor)))",
			},
			source: map[string]interface{}{"age": 25, "role": "admin"},
			expected: map[string]interface{}{
				"is_valid": true,
			},
		},
		{
			name: "Hashing - SHA256",
			mapping: map[string]string{
				"email_hash": "sha256(source.email)",
			},
			source: map[string]interface{}{"email": "test@example.com"},
			expected: map[string]interface{}{
				"email_hash": "973dfe463ec85785f5f95af5ba3906eedb2d931c24e69824a89ea65dba4e813b",
			},
		},
		{
			name: "String - Split and Join",
			mapping: map[string]string{
				"joined": "join(split(source.tags, system.comma), const.|)",
			},
			source: map[string]interface{}{"tags": "a,b,c"},
			expected: map[string]interface{}{
				"joined": "a|b|c",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformer := &AdvancedTransformer{
				Mapping: tt.mapping,
				Strict:  true,
			}

			sourceData, _ := json.Marshal(tt.source)
			msg := message.AcquireMessage()
			msg.SetAfter(sourceData)

			res, err := transformer.Transform(context.Background(), msg)
			if err != nil {
				t.Fatalf("Transform failed: %v", err)
			}

			var result map[string]interface{}
			json.Unmarshal(res.After(), &result)

			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("Expected %s = %v, got %v", k, v, result[k])
				}
			}
		})
	}
}

func TestValidatorTransformerProfessional(t *testing.T) {
	tests := []struct {
		name    string
		rules   []ValidationRule
		data    map[string]interface{}
		wantErr bool
	}{
		{
			name: "Min/Max Validation - Pass",
			rules: []ValidationRule{
				{Field: "age", Type: "min", Config: "18", Severity: "fail"},
				{Field: "age", Type: "max", Config: "100", Severity: "fail"},
			},
			data:    map[string]interface{}{"age": 25},
			wantErr: false,
		},
		{
			name: "Min Validation - Fail",
			rules: []ValidationRule{
				{Field: "age", Type: "min", Config: "18", Severity: "fail"},
			},
			data:    map[string]interface{}{"age": 15},
			wantErr: true,
		},
		{
			name: "In List Validation - Pass",
			rules: []ValidationRule{
				{Field: "status", Type: "in", Config: "active,pending", Severity: "fail"},
			},
			data:    map[string]interface{}{"status": "active"},
			wantErr: false,
		},
		{
			name: "In List Validation - Fail",
			rules: []ValidationRule{
				{Field: "status", Type: "in", Config: "active,pending", Severity: "fail"},
			},
			data:    map[string]interface{}{"status": "deleted"},
			wantErr: true,
		},
		{
			name: "Length Validation - Pass",
			rules: []ValidationRule{
				{Field: "code", Type: "min_len", Config: "3", Severity: "fail"},
				{Field: "code", Type: "max_len", Config: "5", Severity: "fail"},
			},
			data:    map[string]interface{}{"code": "ABC"},
			wantErr: false,
		},
		{
			name: "Length Validation - Fail",
			rules: []ValidationRule{
				{Field: "code", Type: "max_len", Config: "2", Severity: "fail"},
			},
			data:    map[string]interface{}{"code": "ABC"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &ValidatorTransformer{Rules: tt.rules}
			payload, _ := json.Marshal(tt.data)
			msg := message.AcquireMessage()
			msg.SetAfter(payload)

			_, err := v.Transform(context.Background(), msg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Transform() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
