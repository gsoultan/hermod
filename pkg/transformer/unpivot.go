package transformer

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("unpivot", &UnpivotTransformer{})
}

type UnpivotTransformer struct{}

func (t *UnpivotTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	pivotColumns, _ := config["pivotColumns"].([]interface{})
	if len(pivotColumns) == 0 {
		return msg, nil
	}

	attributeField, _ := config["attributeField"].(string)
	if attributeField == "" {
		attributeField = "attribute"
	}

	valueField, _ := config["valueField"].(string)
	if valueField == "" {
		valueField = "value"
	}

	resultField, _ := config["resultField"].(string)
	if resultField == "" {
		resultField = "_fanout"
	}

	data := msg.Data()
	unpivoted := make([]interface{}, 0, len(pivotColumns))

	for _, colRaw := range pivotColumns {
		col := fmt.Sprintf("%v", colRaw)
		val := evaluator.GetValByPath(data, col)

		if val == nil {
			continue
		}

		// Create a new record
		newRecord := make(map[string]interface{})
		for k, v := range data {
			// Skip pivot columns in the new records?
			// Usually yes, we want them removed from the base and added as attribute/value.
			isPivotCol := false
			for _, pc := range pivotColumns {
				if fmt.Sprintf("%v", pc) == k {
					isPivotCol = true
					break
				}
			}
			if !isPivotCol {
				newRecord[k] = v
			}
		}
		newRecord[attributeField] = col
		newRecord[valueField] = val

		unpivoted = append(unpivoted, newRecord)
	}

	msg.SetData(resultField, unpivoted)
	return msg, nil
}
