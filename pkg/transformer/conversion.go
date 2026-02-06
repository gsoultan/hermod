package transformer

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("data_conversion", &DataConversionTransformer{})
}

type DataConversionTransformer struct{}

func (t *DataConversionTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	targetType, _ := config["targetType"].(string)       // "int", "float", "bool", "string", "date"
	format, _ := config["format"].(string)               // used for date
	errorBehavior, _ := config["errorBehavior"].(string) // "fail", "null", "keep"

	valRaw := evaluator.GetMsgValByPath(msg, field)
	if valRaw == nil {
		return msg, nil
	}

	var converted interface{}
	var err error

	switch strings.ToLower(targetType) {
	case "int", "integer":
		converted, err = t.toInt(valRaw)
	case "float", "decimal", "double":
		converted, err = t.toFloat(valRaw)
	case "bool", "boolean":
		converted, err = t.toBool(valRaw)
	case "string":
		converted = fmt.Sprintf("%v", valRaw)
	case "date", "datetime", "time":
		converted, err = t.toDate(valRaw, format)
	default:
		return msg, fmt.Errorf("unsupported target type: %s", targetType)
	}

	if err != nil {
		switch strings.ToLower(errorBehavior) {
		case "fail":
			return nil, err
		case "null":
			converted = nil
		case "keep":
			converted = valRaw
		default:
			return nil, err
		}
	}

	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = field
	}

	msg.SetData(targetField, converted)
	return msg, nil
}

func (t *DataConversionTransformer) toInt(val interface{}) (interface{}, error) {
	switch v := val.(type) {
	case int, int64, int32:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	default:
		return strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
	}
}

func (t *DataConversionTransformer) toFloat(val interface{}) (interface{}, error) {
	switch v := val.(type) {
	case float64, float32:
		return v, nil
	case int, int64, int32:
		return float64(v.(int64)), nil // Caution: may need better type switch
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
	}
}

func (t *DataConversionTransformer) toBool(val interface{}) (interface{}, error) {
	return evaluator.ToBool(val), nil // Evaluator's ToBool is quite robust
}

func (t *DataConversionTransformer) toDate(val interface{}, format string) (interface{}, error) {
	s := fmt.Sprintf("%v", val)
	if format == "" {
		format = time.RFC3339
	}
	return time.Parse(format, s)
}
