package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("data_conversion", &DataConversionTransformer{})
}

type DataConversionTransformer struct{}

func (t *DataConversionTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
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

	valRaw := evaluator.EvaluateField(msg, field)

	var converted any
	var err error

	if valRaw == nil {
		// A field that resolves to nothing is a conversion failure, and follows
		// the configured error behaviour like any other.
		//
		// It used to return the message unchanged with no error: a green node,
		// untouched data, and nothing anywhere to say the conversion never ran.
		// A misspelled field name is the common way to get here, and the editor
		// offers field names from the source's stored Sample, which is known to
		// drift from the names a live CDC stream actually carries. The editor
		// also defaults Error Behaviour to "fail", so staying silent contradicted
		// the setting the operator was looking at.
		//
		// Pipelines where the field is genuinely optional set "keep" (leave the
		// message alone) or "null" (write an explicit null).
		err = fmt.Errorf("field %q resolved to nothing on this message", field)
	} else {
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
	}

	if err != nil {
		switch strings.ToLower(errorBehavior) {
		case "null":
			converted = nil
		case "keep":
			if valRaw == nil {
				// Nothing to keep. Writing the target field as null here would
				// invent a field the message never had.
				return msg, nil
			}
			converted = valRaw
		default: // "fail", and unset — the editor's default is "fail"
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

func (t *DataConversionTransformer) toInt(val any) (any, error) {
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

func (t *DataConversionTransformer) toFloat(val any) (any, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
	}
}

func (t *DataConversionTransformer) toBool(val any) (any, error) {
	return evaluator.ToBool(val), nil // Evaluator's ToBool is quite robust
}

func (t *DataConversionTransformer) toDate(val any, format string) (any, error) {
	s := fmt.Sprintf("%v", val)
	if format == "" {
		format = time.RFC3339
	}
	return time.Parse(format, s)
}
