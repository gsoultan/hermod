package core

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Mapping and Data Conversion both say "Field or expression", and both wrote
// the result to a field named after the expression when no target was given.

func mapStatus(t *testing.T, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("status", "PAID")
	cfg["mapping"] = `{"paid": "Paid in full", "PAID": "shouted"}`
	out, err := (&MappingTransformer{}).Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestMapping_ACallWritesToItsTargetField(t *testing.T) {
	data, err := mapStatus(t, map[string]any{"field": "lower(source.status)", "targetField": "status_label"})
	if err != nil {
		t.Fatal(err)
	}
	if data["status_label"] != "Paid in full" {
		t.Errorf("status_label = %v, want the mapped value of the lowercased status", data["status_label"])
	}
	if len(data) != 2 {
		t.Errorf("wrote something besides status_label: %v", data)
	}
}

func TestMapping_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	data, err := mapStatus(t, map[string]any{"field": "lower(source.status)"})
	if err == nil {
		t.Fatalf("no error; the message now holds %v", data)
	}
	if !strings.Contains(err.Error(), "target field") {
		t.Errorf("error %q should say to set a target field", err)
	}
}

func TestMapping_SourcePathIsMappedInPlace(t *testing.T) {
	data, err := mapStatus(t, map[string]any{"field": "source.status"})
	if err != nil {
		t.Fatal(err)
	}
	if data["status"] != "shouted" || len(data) != 1 {
		t.Errorf("got %v, want status mapped in place and nothing else", data)
	}
}

func TestDataConversion_ACallWritesToItsTargetField(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"amount": " 12 "}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "trim(source.amount)", "targetType": "int", "targetField": "amount_int"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if data["amount_int"] != int64(12) || len(data) != 2 {
		t.Errorf("got %v, want amount_int=12 beside the untouched amount", data)
	}
}

// A fault in the node, so it is not subject to the row's error behaviour:
// "null" would write the null to the same invented field.
func TestDataConversion_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	for _, behavior := range []string{"fail", "null", "keep"} {
		t.Run(behavior, func(t *testing.T) {
			data, err := convertMulti(t, map[string]any{"amount": " 12 "}, map[string]any{
				"conversions": []any{
					map[string]any{"field": "trim(source.amount)", "targetType": "int", "errorBehavior": behavior},
				},
			})
			if err == nil {
				t.Fatalf("no error; the message now holds %v", data)
			}
			if !strings.Contains(err.Error(), "target field") {
				t.Errorf("error %q should say to set a target field", err)
			}
		})
	}
}

func TestDataConversion_SourcePathIsConvertedInPlace(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"amount": "12"}, map[string]any{
		"conversions": []any{map[string]any{"field": "source.amount", "targetType": "int"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if data["amount"] != int64(12) || len(data) != 1 {
		t.Errorf("got %v, want amount converted in place and nothing else", data)
	}
}
