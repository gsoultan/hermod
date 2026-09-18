package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

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

	targetType, _ := config["targetType"].(string)       // "int", "float", "bool", "string", "date", "uuid", "array"
	format, _ := config["format"].(string)               // used for date
	errorBehavior, _ := config["errorBehavior"].(string) // "fail", "null", "keep"
	separator, _ := config["separator"].(string)         // used for array <-> string, default ","
	elementType, _ := config["elementType"].(string)     // used for array: coerce each element

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
		case "array", "list":
			converted, err = t.toArray(valRaw, separator, elementType)
		default:
			converted, err = t.convertScalar(valRaw, targetType, format, separator)
			if err != nil && errors.Is(err, errUnsupportedTargetType) {
				// An unknown target type is a configuration fault, not a value
				// fault, so it is not subject to errorBehavior.
				return msg, err
			}
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

// errUnsupportedTargetType marks a configuration fault rather than a value
// that would not convert, so errorBehavior does not swallow it.
var errUnsupportedTargetType = errors.New("unsupported target type")

// convertScalar converts a single value. It is shared by the node's own
// targetType and by the per-element coercion an "array" conversion applies.
func (t *DataConversionTransformer) convertScalar(val any, targetType, format, separator string) (any, error) {
	switch strings.ToLower(targetType) {
	case "int", "integer":
		return t.toInt(val)
	case "float", "decimal", "double":
		return t.toFloat(val)
	case "bool", "boolean":
		return t.toBool(val)
	case "string":
		return t.toString(val, separator), nil
	case "uuid":
		return t.toUUID(val)
	case "date", "datetime", "time":
		return t.toDate(val, format)
	case "json", "jsonb":
		return t.toJSON(val)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedTargetType, targetType)
	}
}

// toJSON renders a value as JSON text, which is what a json or jsonb column
// needs and the only shape every SQL driver can bind -- database/sql rejects a
// map[string]any outright. The PostgreSQL sink already does this
// (marshalJSONValue) on the paths where it knows the column type; a node lets a
// pipeline reach the same shape for the sinks that do not, and for an object
// built in a `set` node or read from a document source.
//
// Text that is already a JSON object or array passes through untouched. Encoding
// it again would quote it into a JSON string -- the double-encoded payload this
// exists to avoid -- and re-parsing it would push its numbers through float64
// and lose the precision of any integer past 2^53. Anything else is encoded, so
// a string becomes a JSON string: "123" is text that reads as a number, not a
// number, and converting it to one is what the int target type is for.
//
// Text that *opens* like an object or array must parse. toArray, a few lines
// up, asks for a matching closing delimiter too, because when the guess is
// wrong it still has somewhere sensible to go -- it splits on the separator.
// Here the only other branch is "quote it", so a truncated payload would be
// stored as "{\"a\":1" and reported as a success. Truncation is a real failure
// mode (a TOASTed column, a byte limit, a bad substring); demanding only the
// opening delimiter is what turns it into an error the operator can see.
func (t *DataConversionTransformer) toJSON(val any) (any, error) {
	switch v := val.(type) {
	case string:
		return jsonTextFromString(v)
	case []byte:
		// A driver hands JSON text over as bytes. json.Marshal would
		// base64-encode it, which is how a jsonb column ends up holding
		// "eyJhIjoxfQ==".
		return jsonTextFromString(string(v))
	case json.RawMessage:
		return jsonTextFromString(string(v))
	}

	b, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("cannot convert %T to json: %w", val, err)
	}
	return string(b), nil
}

func jsonTextFromString(s string) (any, error) {
	trimmed := strings.TrimSpace(s)
	if looksLikeJSONComposite(trimmed) {
		if !json.Valid([]byte(trimmed)) {
			// Text that opens like an object or array but does not parse is a
			// broken payload, not a value to quote. Quoting it would store the
			// mangled text and report success; an error puts it under
			// errorBehavior, where the operator's choice already lives.
			return nil, errors.New("value looks like JSON but does not parse")
		}
		return trimmed, nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("cannot convert to json: %w", err)
	}
	return string(b), nil
}

func looksLikeJSONComposite(trimmed string) bool {
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// toArray turns a value into a list so it can be used where a list is needed --
// most directly as `IN ({{.field}})` in a SQL template, which expands a list
// into one placeholder per element.
//
// A string is read as JSON when it looks like JSON, because that is the shape a
// jsonb or document column arrives in, and split on separator otherwise. A
// scalar becomes a one-element list rather than an error: a lookup keyed on one
// id is the degenerate case of a lookup keyed on several.
func (t *DataConversionTransformer) toArray(val any, separator, elementType string) (any, error) {
	if separator == "" {
		separator = ","
	}

	var raw []any
	switch v := val.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		switch {
		case trimmed == "":
			raw = []any{}
		case strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"):
			var parsed []any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				raw = parsed
				break
			}
			raw = splitAndTrim(v, separator)
		case strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}"):
			// A JSON object is one value, not a list of its fields.
			var obj map[string]any
			if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
				raw = []any{obj}
				break
			}
			raw = splitAndTrim(v, separator)
		default:
			raw = splitAndTrim(v, separator)
		}
	default:
		if arr, ok := AsSlice(val); ok {
			raw = arr
		} else {
			raw = []any{val}
		}
	}

	if elementType == "" {
		return raw, nil
	}
	out := make([]any, len(raw))
	for i, el := range raw {
		conv, err := t.convertScalar(el, elementType, "", separator)
		if err != nil {
			return nil, fmt.Errorf("element %d (%v): %w", i, el, err)
		}
		out[i] = conv
	}
	return out, nil
}

func splitAndTrim(s, separator string) []any {
	parts := strings.Split(s, separator)
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// toString renders a value as text. A list joins on separator instead of
// rendering Go's %v form, which produced "[a b c]" -- a value no database or
// downstream system accepts, and the reason an array could not be converted
// back to a scalar at all.
func (t *DataConversionTransformer) toString(val any, separator string) string {
	if separator == "" {
		separator = ","
	}
	if arr, ok := AsSlice(val); ok {
		parts := make([]string, len(arr))
		for i, el := range arr {
			parts[i] = scalarToString(el)
		}
		return strings.Join(parts, separator)
	}
	return scalarToString(val)
}

func scalarToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	}
	// Composite values render as JSON; everything else keeps the %v form it has
	// always had, so dates and numbers are unaffected.
	switch reflect.ValueOf(v).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}

// toUUID validates and canonicalizes a UUID: lower case, hyphenated. It accepts
// the forms a database driver or an API can hand over -- hyphenated, bare hex,
// braced, urn-prefixed, and the raw 16 bytes a uuid column decodes to.
func (t *DataConversionTransformer) toUUID(val any) (any, error) {
	switch v := val.(type) {
	case uuid.UUID:
		return v.String(), nil
	case [16]byte:
		return uuid.UUID(v).String(), nil
	case []byte:
		if len(v) == 16 {
			u, err := uuid.FromBytes(v)
			if err != nil {
				return nil, fmt.Errorf("not a uuid: %w", err)
			}
			return u.String(), nil
		}
		u, err := uuid.Parse(strings.TrimSpace(string(v)))
		if err != nil {
			return nil, fmt.Errorf("not a uuid: %w", err)
		}
		return u.String(), nil
	case string:
		u, err := uuid.Parse(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("not a uuid: %w", err)
		}
		return u.String(), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to uuid", val)
	}
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
