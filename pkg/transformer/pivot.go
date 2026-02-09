package transformer

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

// PivotTransformer rotates attribute/value rows into columns grouped by index keys.
// Config:
// - indexKeys: ["id", "date"]           // keys that identify a group
// - attributeField: "attribute"          // field that contains attribute name
// - valueField: "value"                  // field that contains value
// - strategy: "first" | "concat"       // aggregation when multiple rows share same attribute
// - targetField: "" (optional)          // if set, writes pivoted object under this field; otherwise merges into root
func init() { Register("pivot", &PivotTransformer{}) }

type PivotTransformer struct{}

func (t *PivotTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	idxRaw, _ := config["indexKeys"].([]interface{})
	if len(idxRaw) == 0 {
		return msg, nil
	}
	indexKeys := make([]string, 0, len(idxRaw))
	for _, v := range idxRaw {
		indexKeys = append(indexKeys, fmt.Sprintf("%v", v))
	}

	attrField, _ := config["attributeField"].(string)
	if attrField == "" {
		attrField = "attribute"
	}
	valField, _ := config["valueField"].(string)
	if valField == "" {
		valField = "value"
	}
	strategy, _ := config["strategy"].(string)
	if strategy == "" {
		strategy = "first"
	}
	targetField, _ := config["targetField"].(string)

	data := msg.Data()

	// Build index object and key
	idxVals := make([]interface{}, 0, len(indexKeys))
	idxObj := make(map[string]interface{}, len(indexKeys))
	for _, k := range indexKeys {
		v := evaluator.GetValByPath(data, k)
		idxObj[k] = v
		idxVals = append(idxVals, v)
	}

	// Current attribute/value
	attr := fmt.Sprintf("%v", evaluator.GetValByPath(data, attrField))
	val := evaluator.GetValByPath(data, valField)

	// Existing pivot map on message (supports streaming aggregation across multiple records)
	var pivot map[string]interface{}
	if pf := evaluator.GetValByPath(data, targetField); targetField != "" && pf != nil {
		if m, ok := pf.(map[string]interface{}); ok {
			pivot = m
		}
	}
	if pivot == nil {
		pivot = make(map[string]interface{})
	}

	// Apply aggregation strategy
	if existing, ok := pivot[attr]; ok {
		switch strategy {
		case "concat":
			pivot[attr] = fmt.Sprintf("%v%v", existing, val)
		default: // first
			// keep existing
		}
	} else {
		pivot[attr] = val
	}

	// Write back
	if targetField != "" {
		msg.SetData(targetField, pivot)
	} else {
		// Merge into root (may overwrite keys matching attributes)
		for k, v := range pivot {
			msg.SetData(k, v)
		}
	}

	// Also ensure index keys remain present
	for k, v := range idxObj {
		msg.SetData(k, v)
	}
	return msg, nil
}
