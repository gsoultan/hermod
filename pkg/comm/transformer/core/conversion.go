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
	// The zone database, embedded, so a row's time zone resolves wherever the
	// binary runs, a host with no /usr/share/zoneinfo included. The SMTP sink
	// embeds it too; this package does not rely on that sink being linked.
	_ "time/tzdata"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("data_conversion", &DataConversionTransformer{})
}

type DataConversionTransformer struct{}

// conversionRow is one field's conversion. A node holds a list of them, so a
// single node retypes several fields at once, each to its own target type --
// previously one node meant one field, and a row with five columns to retype
// meant five chained nodes.
type conversionRow struct {
	field         string
	targetType    string // "int", "float", "bool", "string", "date", "uuid", "array", "json"
	format        string // date: the layout a value is read with; ISO-8601 needs none
	outputFormat  string // date: the layout the value is written with; empty keeps a time.Time
	timeZone      string // date: IANA zone a zoneless value is read in and every value is written in
	separator     string // used for array <-> string, default ","
	elementType   string // used for array: coerce each element
	targetField   string // defaults to field
	errorBehavior string // "fail", "null", "keep"; empty inherits the node's

	// location is timeZone, loaded once when the config is parsed:
	// time.LoadLocation reads the zone database on every call. Nil keeps each
	// value in its own zone.
	location *time.Location

	// configErr is a fault in the row itself, found once when the config is
	// parsed rather than on every message.
	configErr error
}

// parseConversions reads the node's row list, falling back to the single-field
// keys that every config stored before the list existed still uses.
//
// Row order is the config's order and is not re-sorted. The rows are persisted
// as a JSON array, which keeps its order, so this node does not have the
// problem the `set` node has: its columns live in a map, whose order is gone by
// the time the config is read back, and had to be sorted by path to stop two
// overlapping writes from landing differently from message to message.
func parseConversions(config map[string]any) []conversionRow {
	nodeBehavior, _ := config["errorBehavior"].(string)

	// Presence of the row list is what makes it authoritative, not whether it
	// has rows in it. An operator who deletes the last row means the node
	// converts nothing; treating an empty list as "fall back to the legacy
	// keys" would have quietly resurrected the conversion they just removed,
	// because the editor leaves the pre-list keys in place.
	if raw, ok := config["conversions"].([]any); ok {
		rows := make([]conversionRow, 0, len(raw))
		for _, entry := range raw {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			row := conversionRow{
				field:         rowString(m, "field"),
				targetType:    rowString(m, "targetType"),
				format:        rowString(m, "format"),
				outputFormat:  rowString(m, "outputFormat"),
				timeZone:      rowString(m, "timeZone"),
				separator:     rowString(m, "separator"),
				elementType:   rowString(m, "elementType"),
				targetField:   rowString(m, "targetField"),
				errorBehavior: rowString(m, "errorBehavior"),
			}
			if row.errorBehavior == "" {
				// The node-level setting is the default every row starts from;
				// a row only carries its own when it disagrees.
				row.errorBehavior = nodeBehavior
			}
			// Only a date row reads these. The editor keeps a row's keys when its
			// target type changes, so another row may still carry them unused.
			if isDateTarget(row.targetType) {
				row.configErr = checkOutputFormat(row.outputFormat)
				if row.configErr == nil {
					row.location, row.configErr = loadTimeZone(row.timeZone)
				}
			}
			rows = append(rows, row)
		}
		return rows
	}

	field, _ := config["field"].(string)
	if field == "" {
		return nil
	}
	return []conversionRow{{
		field:         field,
		targetType:    rowString(config, "targetType"),
		format:        rowString(config, "format"),
		separator:     rowString(config, "separator"),
		elementType:   rowString(config, "elementType"),
		targetField:   rowString(config, "targetField"),
		errorBehavior: nodeBehavior,
	}}
}

func rowString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func (t *DataConversionTransformer) Prepare(config map[string]any) (map[string]any, error) {
	if rows := parseConversions(config); len(rows) > 0 {
		config["_parsed_conversions"] = rows
	}
	return config, nil
}

func (t *DataConversionTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	var rows []conversionRow
	if cached, ok := config["_parsed_conversions"].([]conversionRow); ok {
		rows = cached
	} else {
		// Fallback for non-prepared config -- the editor's preview endpoint
		// passes the node config as stored. Shares parseConversions with
		// Prepare so a node cannot resolve one way in the engine and another in
		// the preview the operator is looking at.
		rows = parseConversions(config)
	}
	if len(rows) == 0 {
		return msg, nil
	}

	// Writes are staged and applied only once every row has resolved.
	//
	// Converting in place would leave a half-converted message behind when a
	// later row fails: applyTransformation forwards the *input* message for a
	// workflow with onError "continue" (internal/engine/registry/registry.go),
	// so some columns would reach the sink retyped and the failed one raw, with
	// nothing downstream able to tell the difference. It also means every row
	// reads the message as it arrived rather than as an earlier row left it, so
	// two rows reading the same field agree wherever they sit in the list.
	type pendingWrite struct {
		field string
		value any
	}
	writes := make([]pendingWrite, 0, len(rows))

	for _, row := range rows {
		if row.field == "" {
			// The editor adds an empty row the moment Add is clicked. A row
			// nobody has filled in yet is not a reason to fail every message.
			continue
		}
		if row.configErr != nil {
			// A fault in the node rather than in this value, so errorBehavior
			// does not apply: "null" would turn a typo into a column of nulls.
			return msg, fmt.Errorf("field %q: %w", row.field, row.configErr)
		}

		valRaw := evaluator.EvaluateField(msg, row.field)

		var converted any
		var err error

		if valRaw == nil {
			// A field that resolves to nothing is a conversion failure, and
			// follows the configured error behaviour like any other.
			//
			// It used to return the message unchanged with no error: a green
			// node, untouched data, and nothing anywhere to say the conversion
			// never ran. A misspelled field name is the common way to get here,
			// and the editor offers field names from the source's stored
			// Sample, which is known to drift from the names a live CDC stream
			// actually carries. The editor also defaults Error Behaviour to
			// "fail", so staying silent contradicted the setting the operator
			// was looking at.
			//
			// Pipelines where the field is genuinely optional set "keep" (leave
			// the message alone) or "null" (write an explicit null).
			err = fmt.Errorf("field %q resolved to nothing on this message", row.field)
		} else {
			switch strings.ToLower(row.targetType) {
			case "array", "list":
				converted, err = t.toArray(valRaw, row.separator, row.elementType)
			default:
				converted, err = t.convertScalar(valRaw, row)
				if err != nil && errors.Is(err, errUnsupportedTargetType) {
					// An unknown target type is a configuration fault, not a
					// value fault, so it is not subject to errorBehavior.
					return msg, err
				}
			}
			if err != nil {
				// Named, because a node now holds several rows and the
				// underlying errors do not say which value they choked on:
				// strconv reports `parsing "abc": invalid syntax` and nothing more.
				err = fmt.Errorf("field %q: %w", row.field, err)
			}
		}

		if err != nil {
			switch strings.ToLower(row.errorBehavior) {
			case "null":
				converted = nil
			case "keep":
				if valRaw == nil {
					// Nothing to keep. Writing the target field as null here
					// would invent a field the message never had.
					continue
				}
				converted = valRaw
			default: // "fail", and unset — the editor's default is "fail"
				return nil, err
			}
		}

		targetField := row.targetField
		if targetField == "" {
			targetField = row.field
		}
		writes = append(writes, pendingWrite{field: targetField, value: converted})
	}

	for _, w := range writes {
		msg.SetData(w.field, w.value)
	}
	return msg, nil
}

// errUnsupportedTargetType marks a configuration fault rather than a value
// that would not convert, so errorBehavior does not swallow it.
var errUnsupportedTargetType = errors.New("unsupported target type")

// convertScalar converts a single value. It is shared by the node's own
// targetType and by the per-element coercion an "array" conversion applies.
func (t *DataConversionTransformer) convertScalar(val any, row conversionRow) (any, error) {
	if isDateTarget(row.targetType) {
		return t.convertDate(val, row)
	}
	switch strings.ToLower(row.targetType) {
	case "int", "integer":
		return t.toInt(val)
	case "float", "decimal", "double":
		return t.toFloat(val)
	case "bool", "boolean":
		return t.toBool(val)
	case "string":
		return t.toString(val, row.separator), nil
	case "uuid":
		return t.toUUID(val)
	case "json", "jsonb":
		return t.toJSON(val)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedTargetType, row.targetType)
	}
}

func isDateTarget(targetType string) bool {
	switch strings.ToLower(targetType) {
	case "date", "datetime", "time":
		return true
	}
	return false
}

// convertDate reads a value as a time and, when the row names an output format,
// writes it as text in that layout.
//
// With no output format the row hands over a time.Time, which is what a date or
// timestamp column binds; anything that serialises the message renders it as
// RFC3339. That was the only behaviour until the output format existed, and the
// row's one format field -- which only ever described how to *read* -- was
// labelled "Date Format". Setting it to "02 January 2006" returned
// 2026-09-18T04:30:57.333046Z unchanged, because the layout did not match the
// value, the ISO sweep read it instead, and nothing was ever written with it.
//
// Without a time zone the value is written in its own zone -- an offset in it is
// kept, never converted to UTC or to the server's zone. With one, the instant is
// written in that zone: 2026-09-18T20:00:00Z is the 19th in Asia/Jakarta, which
// a date-only format written in UTC gets wrong for seven hours of every day.
func (t *DataConversionTransformer) convertDate(val any, row conversionRow) (any, error) {
	parsed, err := t.toDate(val, row.format, row.location)
	if err != nil {
		return nil, err
	}
	if row.location != nil {
		parsed = parsed.In(row.location)
	}
	if row.outputFormat == "" {
		return parsed, nil
	}
	return parsed.Format(row.outputFormat), nil
}

// layoutProbeA and layoutProbeB differ in every element a Go layout can print
// -- year, month, day, day of year, weekday, hour, half of day, minute, second,
// fraction, zone name and offset -- so a layout that renders both to the same
// text prints none of them.
var (
	layoutProbeA = time.Date(2001, 2, 3, 4, 5, 6, 100000000, time.UTC)
	layoutProbeB = time.Date(2012, 11, 25, 17, 48, 59, 900000000, time.FixedZone("WIB", 7*60*60))
)

// checkOutputFormat refuses an output format that prints no part of a date.
//
// Go reads a layout by example rather than by letter codes, so the "DD MMMM
// YYYY" or "dd/MM/yyyy" that every other date library would accept holds no
// element Go recognises. time.Format never fails; it prints such a layout back
// verbatim, and every message would reach the sink carrying the same literal
// text in place of its date.
func checkOutputFormat(layout string) error {
	if layout == "" {
		return nil
	}
	if layoutProbeA.Format(layout) == layoutProbeB.Format(layout) {
		return fmt.Errorf("output format %q prints no part of a date: Go layouts write each part "+
			"with the reference date Mon Jan 2 15:04:05 MST 2006, so day/month/year is 02/01/2006", layout)
	}
	return nil
}

// loadTimeZone resolves a row's time zone. Empty means none: each value stays in
// its own zone, as every row stored before the option existed does.
//
// "Local" is refused although it loads. It names whatever zone the server runs
// in, so one workflow would write different dates on different machines --
// and a container's Local is usually UTC, which is the mistake the option is
// there to fix.
func loadTimeZone(name string) (*time.Location, error) {
	if name == "" {
		return nil, nil
	}
	if name == "Local" {
		return nil, errors.New(`time zone "Local" is whatever zone the server runs in; name the zone instead, e.g. "Asia/Jakarta" or "UTC"`)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("time zone %q is not an IANA zone name such as \"Asia/Jakarta\" or \"UTC\": %w", name, err)
	}
	return loc, nil
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
		conv, err := t.convertScalar(el, conversionRow{targetType: elementType, separator: separator})
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

// dateLayouts are the shapes a timestamp arrives in, most specific first. Every
// one of them opens with a YYYY-MM-DD date, and that is what makes sweeping
// them safe to do unasked: none of these can read "03/01/2026" as either the
// 3rd of January or the 1st of March, so text that would have to be guessed at
// is still refused rather than converted into a plausible wrong date.
var dateLayouts = []string{
	time.RFC3339Nano,                          // 2026-09-22T07:26:07.173529602Z07:00
	"2006-01-02T15:04:05.999999999",           // the same with no zone
	"2006-01-02 15:04:05.999999999Z07:00",     // PostgreSQL timestamptz
	"2006-01-02 15:04:05.999999999Z07",        // PostgreSQL's short offset, +07
	"2006-01-02 15:04:05.999999999 -0700 MST", // Go's own time.Time.String()
	"2006-01-02 15:04:05.999999999",           // PostgreSQL timestamp, MySQL DATETIME
	"2006-01-02",                              // a date column
}

// looksLikeISODate screens text before the layout sweep, so a value that is not
// a timestamp costs one length check instead of a *time.ParseError per layout.
// A pipeline running errorBehavior "null" over a column that never parses pays
// that on every message, not just on an exceptional one.
func looksLikeISODate(s string) bool {
	const isoDate = len("2006-01-02")
	// The longest layout above renders to 39 characters.
	if len(s) < isoDate || len(s) > 40 {
		return false
	}
	if s[4] != '-' || s[7] != '-' {
		return false
	}
	for _, i := range [...]int{0, 1, 2, 3, 5, 6, 8, 9} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// toDate reads a value as a time.
//
// The configured layout is a hint, not the only shape accepted. It used to be
// the only one, and the editor's Date Format field placeholds "2006-01-02", so
// a date-only layout over a timestamp column is the configuration an operator
// most easily lands on -- and it failed every message with
// `parsing time "2026-09-22T07:26:07.173529602Z": extra text: "T07:26:07.173529602Z"`
// over a value that was never ambiguous. The layout is still tried first, so a
// node that converts today converts identically, to the same instant and zone.
//
// What it will not do is truncate to the layout's precision. A deadline at
// 07:26 silently becoming midnight is a worse outcome than the error this
// replaces. Reading is not rendering: a date column truncates on write, and a
// row that wants text says so with an output format (see convertDate).
//
// A value with no zone of its own is read in loc when one is given -- a MySQL
// DATETIME or a day-first date is that zone's wall clock, and reading it as UTC
// first would move it by the zone's offset. A nil loc keeps time.Parse exactly
// as it was, UTC included.
func (t *DataConversionTransformer) toDate(val any, format string, loc *time.Location) (time.Time, error) {
	// A query, sample or polling path hands a timestamp column over as a
	// time.Time -- and so does the evaluator's fast path. Rendering that with
	// %v produced Go's String() form, which no configured layout describes, so
	// the one shape needing no conversion at all was the one that failed.
	switch v := val.(type) {
	case time.Time:
		return v, nil
	case *time.Time:
		if v == nil {
			return time.Time{}, errors.New("cannot read a date from a nil value")
		}
		return *v, nil
	}

	// scalarToString rather than %v: a driver hands text over as []byte, which
	// %v renders as the decimal bytes.
	s := strings.TrimSpace(scalarToString(val))
	if s == "" {
		return time.Time{}, errors.New("cannot read a date from an empty value")
	}

	parse := time.Parse
	if loc != nil {
		parse = func(layout, value string) (time.Time, error) { return time.ParseInLocation(layout, value, loc) }
	}

	if format != "" {
		if parsed, err := parse(format, s); err == nil {
			return parsed, nil
		}
	}

	if looksLikeISODate(s) {
		for _, layout := range dateLayouts {
			if parsed, err := parse(layout, s); err == nil {
				return parsed, nil
			}
		}
	}

	if format != "" {
		return time.Time{}, fmt.Errorf("cannot read %q as a date: it does not match the configured layout %q, and is not an ISO-8601 date or timestamp", s, format)
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a date: it is not an ISO-8601 date or timestamp", s)
}
