package hermod

import (
	"encoding/json"
	"strconv"
)

// StringMap is a map[string]string that tolerates JSON values of other scalar
// types when unmarshalling. UI clients sometimes send configuration values as
// booleans or numbers (e.g. {"use_cdc": true, "batch_size": 100}) rather than
// strings. Decoding such payloads directly into map[string]string fails with
// errors like "json: cannot unmarshal bool into Go struct field ... of type
// string". StringMap coerces those scalar values to their string form so the
// API stays robust regardless of how the client encodes them.
type StringMap map[string]string

// UnmarshalJSON decodes a JSON object into a StringMap, coercing scalar values
// (string, bool, number) to strings. Null becomes an absent/empty map and null
// values become empty strings. Objects and arrays are preserved as their JSON
// encoding.
func (m *StringMap) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(StringMap, len(raw))
	for k, v := range raw {
		result[k] = coerceToString(v)
	}
	*m = result
	return nil
}

// coerceToString converts a decoded JSON scalar value to its string form.
func coerceToString(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		// Use the shortest representation that round-trips and avoids
		// scientific notation / trailing ".0" for integral values.
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	default:
		// Objects/arrays: keep their JSON encoding.
		b, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
