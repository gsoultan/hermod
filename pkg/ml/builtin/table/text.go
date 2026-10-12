package table

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
)

// ToText renders a present value as the worker's to_text does: a boolean as
// true or false, a whole number below 2^53 without a decimal point, any other
// number as Python's repr, a string as itself, and an object or array as
// compact JSON.
func ToText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return pyFloat(x)
	case float32:
		return pyFloat(float64(x))
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return pyFloat(f)
		}
		return x.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// pyFloat is Python's rendering of a float through to_text.
func pyFloat(f float64) string {
	a := math.Abs(f)
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == math.Trunc(f) && a < 1<<53:
		return strconv.FormatInt(int64(f), 10)
	case a >= 1e-4 && a < 1e16:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

func sortStrings(s []string) { slices.Sort(s) }

// FromJSON types a JSON array of row objects into a table, keeping the
// columns in the order the rows first name them. Numbers outside float64's
// range, and anything that is not an array of objects, are refused.
func FromJSON(raw []byte) (*Table, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errors.New("'rows' must be an array of objects")
	}
	var (
		rows  []map[string]any
		order []string
	)
	seen := map[string]bool{}
	for dec.More() {
		keys, row, err := object(dec)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", len(rows), err)
		}
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
		rows = append(rows, row)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("'rows' is not valid JSON: %w", err)
	}
	return fromOrdered(rows, order)
}

// object reads one JSON object, its keys in order.
func object(dec *json.Decoder) ([]string, map[string]any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if tok != json.Delim('{') {
		return nil, nil, errors.New("it is not a JSON object")
	}
	var keys []string
	row := map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, _ := tok.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		if _, dup := row[key]; !dup {
			keys = append(keys, key)
		}
		row[key] = v
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	return keys, row, nil
}
