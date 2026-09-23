package evaluator

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Evaluator manages expression evaluation.
type Evaluator struct {
	// Custom functions can be added here if needed
}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// ... existing helpers ...

func (e *Evaluator) EvaluateAdvancedExpression(msg hermod.Message, expr any) any {
	valStr, ok := expr.(string)
	if !ok {
		return expr
	}
	return e.ParseAndEvaluate(msg, valStr)
}

func (e *Evaluator) ParseAndEvaluate(msg hermod.Message, expr string) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// Check if it's a source reference: source.path
	if strings.HasPrefix(expr, "source.") {
		return GetMsgValByPath(msg, expr[7:])
	}

	// Try to parse as a number
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f
	}

	// Try to parse as a boolean
	if expr == "true" {
		return true
	}
	if expr == "false" {
		return false
	}

	// Check if it's a string literal: 'string' or "string"
	if ((strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) ||
		(strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\""))) && len(expr) >= 2 {
		return expr[1 : len(expr)-1]
	}

	// Check if it's a function call: func(args...)
	if strings.HasSuffix(expr, ")") {
		openParen := -1
		parenCount := 0
		for i := len(expr) - 1; i >= 0; i-- {
			if expr[i] == ')' {
				parenCount++
			} else if expr[i] == '(' {
				parenCount--
				if parenCount == 0 {
					openParen = i
					break
				}
			}
		}

		if openParen > 0 {
			funcName := strings.TrimSpace(expr[:openParen])
			// Verify it looks like a function name
			isFunc := true
			for _, c := range funcName {
				if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
					isFunc = false
					break
				}
			}

			if isFunc {
				argsStr := expr[openParen+1 : len(expr)-1]
				args := e.parseArgs(argsStr)
				evaluatedArgs := make([]any, len(args))
				for i, arg := range args {
					evaluatedArgs[i] = e.ParseAndEvaluate(msg, arg)
				}
				return e.CallFunction(funcName, evaluatedArgs)
			}
		}
	}

	// Default to returning the expression as a string
	return expr
}

func (e *Evaluator) parseArgs(argsStr string) []string {
	var args []string
	if argsStr == "" {
		return args
	}

	var currentArg strings.Builder
	parenCount := 0
	inQuote := false
	var quoteChar byte

	for i := 0; i < len(argsStr); i++ {
		c := argsStr[i]
		if c == '\'' || c == '"' {
			if !inQuote {
				inQuote = true
				quoteChar = c
			} else if c == quoteChar {
				inQuote = false
			}
			currentArg.WriteByte(c)
		} else if !inQuote && c == '(' {
			parenCount++
			currentArg.WriteByte(c)
		} else if !inQuote && c == ')' {
			parenCount--
			currentArg.WriteByte(c)
		} else if !inQuote && c == ',' && parenCount == 0 {
			args = append(args, strings.TrimSpace(currentArg.String()))
			currentArg.Reset()
		} else {
			currentArg.WriteByte(c)
		}
	}
	args = append(args, strings.TrimSpace(currentArg.String()))
	return args
}

func (e *Evaluator) CallFunction(name string, args []any) any {
	switch strings.ToLower(name) {
	case "lower":
		if len(args) > 0 {
			return strings.ToLower(fmt.Sprintf("%v", args[0]))
		}
	case "upper":
		if len(args) > 0 {
			return strings.ToUpper(fmt.Sprintf("%v", args[0]))
		}
	case "trim":
		if len(args) > 0 {
			return strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		}
	case "replace":
		if len(args) >= 3 {
			s := fmt.Sprintf("%v", args[0])
			oldVal := fmt.Sprintf("%v", args[1])
			newVal := fmt.Sprintf("%v", args[2])
			return strings.ReplaceAll(s, oldVal, newVal)
		}
	case "concat":
		var sb strings.Builder
		for _, arg := range args {
			if arg != nil {
				fmt.Fprintf(&sb, "%v", arg)
			}
		}
		return sb.String()
	case "substring":
		if len(args) >= 2 {
			s := fmt.Sprintf("%v", args[0])
			start, _ := strconv.Atoi(fmt.Sprintf("%v", args[1]))
			end := len(s)
			if len(args) >= 3 {
				end, _ = strconv.Atoi(fmt.Sprintf("%v", args[2]))
			}
			if start < 0 {
				start = 0
			}
			if start > len(s) {
				start = len(s)
			}
			if end > len(s) {
				end = len(s)
			}
			if start > end {
				return ""
			}
			return s[start:end]
		}
	case "date_format":
		if len(args) >= 2 {
			dateStr := fmt.Sprintf("%v", args[0])
			toFormat := fmt.Sprintf("%v", args[1])
			var t time.Time
			var err error
			if len(args) >= 3 {
				fromFormat := fmt.Sprintf("%v", args[2])
				t, err = time.Parse(fromFormat, dateStr)
			} else {
				formats := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z}
				for _, f := range formats {
					t, err = time.Parse(f, dateStr)
					if err == nil {
						break
					}
				}
			}
			if err == nil {
				return t.Format(toFormat)
			}
			return dateStr
		}
	case "coalesce":
		for _, arg := range args {
			if arg != nil && fmt.Sprintf("%v", arg) != "<nil>" && fmt.Sprintf("%v", arg) != "" {
				return arg
			}
		}
		return nil
	case "now":
		return time.Now().Format(time.RFC3339)
	case "uuid":
		return uuid.New().String()
	case "timestamp":
		return time.Now().Unix()
	case "env":
		if len(args) > 0 {
			key := fmt.Sprintf("%v", args[0])
			val := os.Getenv(key)
			if val == "" && len(args) > 1 {
				return args[1]
			}
			return val
		}
		return ""
	case "secret":
		if len(args) > 0 {
			key := fmt.Sprintf("%v", args[0])
			// First try direct env match
			val := os.Getenv(key)
			if val == "" {
				// Then try with HERMOD_SECRET_ prefix
				val = os.Getenv("HERMOD_SECRET_" + key)
			}
			if val == "" && len(args) > 1 {
				return args[1]
			}
			return val
		}
		return ""
	case "add":
		if len(args) >= 2 {
			v1, _ := ToFloat64(args[0])
			v2, _ := ToFloat64(args[1])
			return v1 + v2
		}
	case "sub":
		if len(args) >= 2 {
			v1, _ := ToFloat64(args[0])
			v2, _ := ToFloat64(args[1])
			return v1 - v2
		}
	case "mul":
		if len(args) >= 2 {
			v1, _ := ToFloat64(args[0])
			v2, _ := ToFloat64(args[1])
			return v1 * v2
		}
	case "div":
		if len(args) >= 2 {
			v1, _ := ToFloat64(args[0])
			v2, _ := ToFloat64(args[1])
			if v2 == 0 {
				return nil
			}
			return v1 / v2
		}
	case "round":
		if len(args) >= 1 {
			v, _ := ToFloat64(args[0])
			precision := 0.0
			if len(args) >= 2 {
				precision, _ = ToFloat64(args[1])
			}
			ratio := math.Pow(10, precision)
			return math.Round(v*ratio) / ratio
		}
	case "and":
		for _, arg := range args {
			if !ToBool(arg) {
				return false
			}
		}
		return true
	case "or":
		return slices.ContainsFunc(args, ToBool)
	case "not":
		if len(args) > 0 {
			return !ToBool(args[0])
		}
	case "if":
		if len(args) >= 3 {
			if ToBool(args[0]) {
				return args[1]
			}
			return args[2]
		}
	case "eq":
		if len(args) >= 2 {
			return fmt.Sprintf("%v", args[0]) == fmt.Sprintf("%v", args[1])
		}
	case "gt":
		if len(args) >= 2 {
			v1, ok1 := ToFloat64(args[0])
			v2, ok2 := ToFloat64(args[1])
			if ok1 && ok2 {
				return v1 > v2
			}
			return fmt.Sprintf("%v", args[0]) > fmt.Sprintf("%v", args[1])
		}
	case "lt":
		if len(args) >= 2 {
			v1, ok1 := ToFloat64(args[0])
			v2, ok2 := ToFloat64(args[1])
			if ok1 && ok2 {
				return v1 < v2
			}
			return fmt.Sprintf("%v", args[0]) < fmt.Sprintf("%v", args[1])
		}
	case "contains":
		if len(args) >= 2 {
			return strings.Contains(fmt.Sprintf("%v", args[0]), fmt.Sprintf("%v", args[1]))
		}
	case "toint":
		if len(args) > 0 {
			v, ok := ToInt64(args[0])
			if ok {
				return v
			}
			vf, _ := ToFloat64(args[0])
			return int64(vf)
		}
	case "tofloat":
		if len(args) > 0 {
			v, _ := ToFloat64(args[0])
			return v
		}
	case "tostring":
		if len(args) > 0 {
			if args[0] == nil {
				return ""
			}
			return fmt.Sprintf("%v", args[0])
		}
	case "tobool":
		if len(args) > 0 {
			return ToBool(args[0])
		}
	case "todate":
		if len(args) > 0 {
			val := args[0]
			if val == nil {
				return nil
			}
			dateStr := fmt.Sprintf("%v", val)
			var t time.Time
			var err error
			if len(args) >= 2 {
				format := fmt.Sprintf("%v", args[1])
				t, err = time.Parse(format, dateStr)
			} else {
				formats := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z}
				for _, f := range formats {
					t, err = time.Parse(f, dateStr)
					if err == nil {
						break
					}
				}
			}
			if err == nil {
				return t.Format(time.RFC3339)
			}
			return nil
		}
	}
	return nil
}

// Path helpers

func GetValByPath(data map[string]any, path string) any {
	if path == "" {
		return nil
	}

	// Walk the map directly when the path is a plain field reference. The
	// round trip below costs O(row) per read, so a sink mapping with N
	// placeholders marshalled the row N times: 17.9us and 294 allocations for
	// one field of a 128-column row, 106us and 1763 allocations to resolve a
	// six-placeholder template. Both are now independent of row width.
	//
	// The round trip is not only overhead — it is what turns an int into a
	// float64 and a []byte into a base64 string, and every transformation,
	// condition and mapping downstream is written against that shape. So the
	// walk normalises what it finds through the same rules, and bails out to
	// the round trip for anything it cannot reproduce exactly.
	// TestGetValByPathMatchesJSONRoundTrip holds the two together.
	if v, ok := lookupByWalk(data, path); ok {
		return v
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	res := gjson.GetBytes(jsonData, path)
	if !res.Exists() {
		return nil
	}

	return res.Value()
}

// gjsonPathMeta are the characters gjson gives meaning to beyond the "." path
// separator: wildcards, array queries, modifiers and escapes. A path holding
// any of them is handed to gjson rather than walked, because reproducing those
// semantics here is how a fast path silently starts disagreeing with the
// thing it is supposed to be a fast path for.
const gjsonPathMeta = `*?#|@\[]()!<>=~`

// lookupByWalk resolves a plain dotted path against the map directly.
//
// The bool reports whether the answer is authoritative. False means "ask
// gjson" — either the path uses syntax this does not implement, or a value
// along the way has a shape whose JSON form cannot be reproduced here.
func lookupByWalk(data map[string]any, path string) (any, bool) {
	v, ok := walkPath(data, path)
	if !ok {
		return nil, false
	}
	return normalizeToJSONShape(v)
}

// walkPath resolves a plain dotted path against the map and returns the value
// the map actually holds, without normalising it.
//
// The bool means what lookupByWalk's does: false is "ask gjson". A true with a
// nil value means the path is definitively absent.
//
// Split out for the one consumer that must not be normalised -- a value about
// to be bound as a SQL parameter. See GetMsgRawValByPath.
func walkPath(data map[string]any, path string) (any, bool) {
	if strings.ContainsAny(path, gjsonPathMeta) {
		return nil, false
	}

	var cur any = data
	for rest := path; ; {
		seg, more, hasMore := strings.Cut(rest, ".")

		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				// Absent and definitively so: gjson reports the same nil for a
				// missing key, so there is nothing to fall back for.
				return nil, true
			}
			cur = v
		case []any:
			// gjson indexes an array with a bare number. Anything else is not
			// an index, and gjson would not match it either.
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, true
			}
			cur = node[i]
		default:
			// A scalar, or a typed container such as map[string]string or
			// []string. gjson sees those through their marshalled form and can
			// descend into them; this cannot, so let gjson answer.
			return nil, false
		}

		if !hasMore {
			return cur, true
		}
		rest = more
	}
}

// normalizeToJSONShape returns v as the JSON round trip would have returned
// it. The bool reports whether it could do so.
func normalizeToJSONShape(v any) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		return t, true
	case bool:
		return t, true
	case float64:
		return finiteFloat(t)
	case float32:
		return finiteFloat(float64(t))
	}

	if f, ok := integerAsFloat(v); ok {
		return f, true
	}

	// A container or a type with its own marshalling. Marshal just this
	// subtree: gjson would have parsed exactly these bytes out of the full
	// row, so the value is the same and the cost is proportional to the field
	// rather than to the row.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return gjson.ParseBytes(b).Value(), true
}

// GetMsgRawValByPath resolves a path to the value the message actually holds,
// skipping the JSON normalisation GetMsgValByPath applies.
//
// That normalisation is deliberate and pinned: an int becomes a float64 and a
// []byte becomes base64, because every transformation, condition and mapping
// downstream is written against the JSON round trip's shape.
//
// It is wrong for exactly one consumer: a value about to be bound as a SQL
// parameter. float64 carries 53 bits of mantissa, so a bigint key above 2^53
// reaches the driver already rounded -- 9007199254740993 binds as
// 9007199254740992 -- and the query matches no row. Measured against PostgreSQL
// 18.4 via pgx: zero rows, no error, straight into db_lookup's miss policy, so
// it reads as "the field is null" rather than as a lost key. Snowflake-style
// and other 64-bit identifiers are squarely above 2^53.
//
// Only paths the direct walk can answer skip normalisation. Anything else --
// the virtual fields, the before-image, the raw-payload fallbacks -- falls
// through to GetMsgValByPath, so this can resolve nothing the full reader
// could not.
//
// This cannot rescue a value that was already a float64 when it reached
// Hermod: a message decoded from JSON lost the digits before any of this ran.
func GetMsgRawValByPath(msg hermod.Message, path string) any {
	if path == "" || msg == nil {
		return nil
	}
	if v, ok := walkPath(msg.DataRef(), strings.TrimPrefix(path, "$.")); ok && v != nil {
		return v
	}
	return GetMsgValByPath(msg, path)
}

// MessageResolver is GetMsgRawValByPath bound to one message, in the shape a
// SQL template's path resolver takes (sqlutil.Resolver). It is what makes a
// {{ }} token in db_lookup, execute_sql and the editor's SQL builder resolve
// the same paths as a condition or a sink mapping: the data map first, then the
// CDC envelope (after., before.), the virtual fields and meta..
//
// Note the asymmetry it inherits, which is deliberate. A path the data map can
// answer keeps its Go type, because a bigint above 2^53 resolved through JSON
// comes back as float64 and then matches no row while reporting no error. A
// path only the envelope can answer goes through gjson and is therefore
// JSON-normalised -- those bytes are already JSON and there is no typed value
// left to preserve.
//
// The return type is the bare func rather than sqlutil.Resolver so that the
// evaluator does not import sqlutil; the two are assignable.
func MessageResolver(msg hermod.Message) func(path string) any {
	return func(path string) any { return GetMsgRawValByPath(msg, path) }
}

func GetMsgValByPath(msg hermod.Message, path string) any {
	if path == "" || msg == nil {
		return nil
	}

	// "$" is the JSONPath document root: a syntax marker, not a different kind
	// of lookup. Strip it and resolve exactly as the bare form does. This branch
	// used to have its own two-step resolution (payload first, data second, then
	// give up), which made "$.x" strictly weaker than "x" — it never reached the
	// virtual fields, the before-image, or the raw-payload fallbacks below, and
	// it returned the pre-transform payload value where the bare form returned
	// the current one. A db_lookup keyField of "$.x" therefore resolved to nil
	// and silently skipped enrichment on messages where "x" worked.
	path = strings.TrimPrefix(path, "$.")

	// 1) Try the path as-is in the data map first.
	// This ensures that real data columns (like "id", "table") are not shadowed by virtual fields.
	if v := GetValByPath(msg.DataRef(), path); v != nil {
		return v
	}

	// 2) Expose CDC/meta virtual fields for filtering/templates
	// Supported aliases:
	//  - operation/op  → msg.Operation()
	//  - table         → msg.Table()
	//  - schema        → msg.Schema()
	//  - meta.<key> or metadata.<key> → msg.Metadata()[key]
	lower := strings.ToLower(path)
	switch lower {
	case "operation", "op":
		op := msg.Operation()
		if op != "" {
			return string(op)
		}
	case "id":
		// A row's own id column wins over the message's synthetic id, the same
		// way the data map above wins over every virtual field.
		//
		// The data map covers that for an insert or an update, because it
		// hydrates from the after-image. A delete has no after-image
		// (pkg/comm/source/postgres/postgres.go:1259-1284) -- the row's columns
		// exist only in the before-image, which is consulted further down, after
		// this block. So a delete resolved "id" to msg.ID(), and for a CDC
		// message that is the LSN. A sink mapping keyed on id therefore issued
		// DELETE ... WHERE key = '<LSN>' and removed nothing: the row stayed in
		// the destination for ever, and the write reported success.
		//
		// Only the before-image is checked here. The after-image already reaches
		// the data map, and the other virtual fields are left alone: table,
		// schema and operation are unlikely column names whose virtual values
		// are the useful answer, whereas "id" collides with one of the most
		// common columns there is and its virtual value is never the row's
		// identity.
		if v := getValueFromRaw(msg.Before(), "id"); v != nil {
			return v
		}
		if id := msg.ID(); id != "" {
			return id
		}
	case "table":
		if t := msg.Table(); t != "" {
			return t
		}
	case "schema":
		if s := msg.Schema(); s != "" {
			return s
		}
	case "after":
		if a := msg.After(); len(a) > 0 {
			var val any
			if err := json.Unmarshal(a, &val); err == nil {
				return val
			}
		}
	case "before":
		if b := msg.Before(); len(b) > 0 {
			var val any
			if err := json.Unmarshal(b, &val); err == nil {
				return val
			}
		}
	}
	if strings.HasPrefix(lower, "meta.") || strings.HasPrefix(lower, "metadata.") {
		key := path[strings.Index(path, ".")+1:]
		// MetadataValue, not MetadataRef: a transformation resolving a meta.
		// path runs while the message may be held by other goroutines, and
		// indexing the live map raced SetMetadata. This is still a single-key
		// read with no clone.
		if v, ok := hermod.MetadataValue(msg, key); ok {
			return v
		}
	}

	// 3) Try raw payloads if data doesn't have it
	// This handles cases where Data() only contains "after" or is empty (like in deletes)
	if strings.HasPrefix(lower, "before.") {
		base := path[7:]
		// 1. Try msg.Before() as the 'before' object
		if v := getValueFromRaw(msg.Before(), base); v != nil {
			return v
		}
		// 2. Try full path in DataRef (if 'before' is an explicit key in data)
		if v := GetValByPath(msg.DataRef(), path); v != nil {
			return v
		}
	}
	if strings.HasPrefix(lower, "after.") {
		base := path[6:]
		// 1. Try payload as the 'after' object (CDC style)
		if v := getValueFromRaw(msg.Payload(), base); v != nil {
			return v
		}
		// 2. Try full path in DataRef (if 'after' is an explicit key in data)
		if v := GetValByPath(msg.DataRef(), path); v != nil {
			return v
		}
	}

	// Try direct in before/after if not prefixed
	if v := getValueFromRaw(msg.Payload(), path); v != nil {
		return v
	}
	return getValueFromRaw(msg.Before(), path)
}

func getValueFromRaw(raw []byte, path string) any {
	if len(raw) == 0 {
		return nil
	}
	res := gjson.GetBytes(raw, path)
	if !res.Exists() {
		return nil
	}
	return res.Value()
}

func SetValByPath(data map[string]any, path string, val any) {
	if path == "" {
		return
	}

	// Writing one field marshalled the whole map to JSON, sjson-set the field,
	// unmarshalled it all back and then cleared the map and refilled it: 44us
	// and 694 allocations for one field of a 128-column row.
	//
	// It cannot simply be replaced by a targeted write, because the round trip
	// has a second effect — it JSON-normalises every *untouched* value too, so
	// an int elsewhere in the map comes back a float64 and a []byte comes back
	// base64. The fast path is therefore taken only when the map is already
	// all-JSON-native, which is precisely the case where the round trip would
	// have left the other fields alone anyway. That covers a message hydrated
	// from a payload, which is how most data arrives.
	// TestSetValByPathMatchesJSONRoundTrip holds the two together over both
	// kinds of map.
	//
	// One difference worth knowing: the round trip replaces every nested
	// container with a freshly decoded one, while the fast path mutates them in
	// place. A caller holding a reference to a sub-map across the call sees the
	// update under the fast path and does not under the round trip.
	if isJSONNativeMap(data) && setByWalk(data, path, val) {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	newJSON, err := sjson.SetBytes(jsonData, path, val)
	if err != nil {
		return
	}

	var newData map[string]any
	if err := json.Unmarshal(newJSON, &newData); err == nil {
		for k := range data {
			delete(data, k)
		}
		maps.Copy(data, newData)
	}
}

// Type conversion helpers

func ToFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		// An exact decimal column (sqlutil.DecodeColumn) arrives as one of
		// these. json.Number is a *named* string type, so it matches no
		// `case string` in Go -- without this branch a numeric column compared
		// to a threshold read as 0, and every such condition silently changed
		// its answer.
		f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		return f, err == nil
	case string:
		// Align with UI simulator: be lenient about surrounding whitespace
		s := strings.TrimSpace(v)
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}

func ToInt64(val any) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case json.Number:
		// ParseInt first, and that ordering is the point: it is what carries an
		// identifier past float64's exact range through intact. Going via
		// ParseFloat would read 9007199254740993 back as ...992, which is the
		// rounding this type exists to avoid.
		return parseInt64Text(string(v))
	case string:
		// Align with UI simulator: be lenient about surrounding whitespace
		return parseInt64Text(v)
	}
	return 0, false
}

// parseInt64Text reads an integer, falling back to a float for text like
// "1200.50" that ParseInt cannot take. Shared so the json.Number and string
// branches of ToInt64 cannot drift apart.
func parseInt64Text(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		f, err := strconv.ParseFloat(s, 64)
		return int64(f), err == nil
	}
	return i, true
}

func ToBool(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(v)
		if s == "true" || s == "1" || s == "yes" || s == "on" {
			return true
		}
		if s == "false" || s == "0" || s == "no" || s == "off" {
			return false
		}
		b, _ := strconv.ParseBool(s)
		return b
	case int, int32, int64, float32, float64:
		f, _ := ToFloat64(v)
		return f != 0
	}
	return false
}

func isNumeric(val any) bool {
	switch val.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	}
	return false
}

func ToTime(val any) (time.Time, bool) {
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		s := strings.TrimSpace(v)
		formats := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z}
		for _, f := range formats {
			t, err := time.Parse(f, s)
			if err == nil {
				return t, true
			}
		}
	case int64:
		return time.Unix(v, 0), true
	case int:
		return time.Unix(int64(v), 0), true
	case float64:
		return time.Unix(int64(v), 0), true
	case json.Number:
		// A decimal column carrying a unix timestamp. Routed through ToInt64
		// so it reads the same as the int64 branch above rather than going via
		// a float64 and rounding on the way.
		if i, ok := ToInt64(v); ok {
			return time.Unix(i, 0), true
		}
	}
	return time.Time{}, false
}

// Template resolver

// ResolveTemplate replaces every {{ ... }} token in temp with the value
// resolved from data (or env/expressions). It performs a single forward pass:
// resolved values are written to the output and never re-scanned. This keeps
// resolution bounded and prevents both template injection and the infinite
// loop that occurs when a resolved value itself contains a self-referential
// {{ ... }} token (e.g. data field "a" whose value is literally "{{a}}").
func ResolveTemplate(temp string, data map[string]any) string {
	var out strings.Builder
	i := 0
	for i < len(temp) {
		rel := strings.Index(temp[i:], "{{")
		if rel == -1 {
			out.WriteString(temp[i:])
			break
		}
		start := i + rel
		out.WriteString(temp[i:start])

		closeRel := strings.Index(temp[start+2:], "}}")
		if closeRel == -1 {
			// Unterminated tag: emit the remainder verbatim and stop.
			out.WriteString(temp[start:])
			break
		}
		end := start + 2 + closeRel
		path := strings.TrimSpace(temp[start+2 : end])
		out.WriteString(resolveTemplatePath(path, data))

		// Advance past the closing "}}" so the substituted value is not
		// processed again, guaranteeing termination.
		i = end + 2
	}
	return out.String()
}

// resolveTemplatePath resolves a single template token (the text between
// {{ and }}) to its string value.
func resolveTemplatePath(path string, data map[string]any) string {
	var val any
	switch {
	case strings.HasPrefix(path, "env."):
		// Environment variable access is disabled for security reasons
		return ""
	case strings.Contains(path, "(") && strings.HasSuffix(path, ")"):
		e := NewEvaluator()
		// Create a mock message if data is provided, to allow accessing it via source.path
		var msg hermod.Message
		if data != nil {
			msg = &mockMessage{data: data}
		}
		val = e.ParseAndEvaluate(msg, path)
	default:
		// Support ".field" style (UI templates commonly use a leading dot)
		val = GetValByPath(data, strings.TrimPrefix(path, "."))
	}

	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", val)
}

func EvaluateField(msg hermod.Message, field string) any {
	if (strings.Contains(field, "(") && strings.HasSuffix(field, ")")) || strings.HasPrefix(field, "source.") {
		e := NewEvaluator()
		return e.ParseAndEvaluate(msg, field)
	}
	return GetMsgValByPath(msg, field)
}

// Condition evaluator

func EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	if len(conditions) == 0 {
		return true
	}

	for _, cond := range conditions {
		field, _ := cond["field"].(string)
		op, _ := cond["operator"].(string)
		val := cond["value"]
		match := false

		fieldValRaw := EvaluateField(msg, field)
		// Treat missing values consistently as empty string (UI simulator behavior)
		fieldVal := stringify(fieldValRaw)

		// Resolve templates/expressions in the value if present
		valResolved := val
		if vs, ok := val.(string); ok {
			if strings.Contains(vs, "{{") && strings.Contains(vs, "}}") {
				var data map[string]any
				if msg != nil {
					data = msg.Data()
				}
				valResolved = ResolveTemplate(vs, data)
			} else {
				valResolved = vs
			}
		}
		valStr := stringify(valResolved)

		switch op {
		case "=", "eq":
			match = fieldVal == valStr || numericallyEqual(fieldValRaw, valResolved)
		case "!=", "neq":
			match = fieldVal != valStr && !numericallyEqual(fieldValRaw, valResolved)
		case ">", "gt", ">=", "gte", "<", "lt", "<=", "lte":
			t1, isT1 := ToTime(fieldValRaw)
			t2, isT2 := ToTime(valResolved)
			if isT1 && isT2 && !isNumeric(fieldValRaw) && !isNumeric(valResolved) {
				// Only use time comparison if they look like dates and are NOT simple numbers
				// (to avoid treating small integers as unix timestamps when not intended)
				switch op {
				case ">", "gt":
					match = t1.After(t2)
				case ">=", "gte":
					match = !t1.Before(t2)
				case "<", "lt":
					match = t1.Before(t2)
				case "<=", "lte":
					match = !t1.After(t2)
				}
			} else {
				v1, ok1 := ToFloat64(fieldValRaw)
				v2, ok2 := ToFloat64(valResolved)
				if ok1 && ok2 {
					switch op {
					case ">", "gt":
						match = v1 > v2
					case ">=", "gte":
						match = v1 >= v2
					case "<", "lt":
						match = v1 < v2
					case "<=", "lte":
						match = v1 <= v2
					}
				} else {
					// Fallback to string comparison if not numbers
					switch op {
					case ">", "gt":
						match = fieldVal > valStr
					case ">=", "gte":
						match = fieldVal >= valStr
					case "<", "lt":
						match = fieldVal < valStr
					case "<=", "lte":
						match = fieldVal <= valStr
					}
				}
			}
		case "contains":
			match = strings.Contains(fieldVal, valStr)
		case "not_contains":
			match = !strings.Contains(fieldVal, valStr)
		case "regex":
			re, err := compilePattern(valStr)
			if err == nil {
				match = re.MatchString(fieldVal)
			}
		case "not_regex":
			re, err := compilePattern(valStr)
			if err == nil {
				match = !re.MatchString(fieldVal)
			}
		}

		if !match {
			return false
		}
	}

	return true
}

type mockMessage struct {
	hermod.Message
	id       string
	op       hermod.Operation
	table    string
	schema   string
	data     map[string]any
	metadata map[string]string
}

func (m *mockMessage) ID() string                     { return m.id }
func (m *mockMessage) Operation() hermod.Operation    { return m.op }
func (m *mockMessage) Table() string                  { return m.table }
func (m *mockMessage) Schema() string                 { return m.schema }
func (m *mockMessage) Data() map[string]any           { return m.data }
func (m *mockMessage) Metadata() map[string]string    { return m.metadata }
func (m *mockMessage) DataRef() map[string]any        { return m.data }
func (m *mockMessage) MetadataRef() map[string]string { return m.metadata }
func (m *mockMessage) Before() []byte {
	if b, ok := m.data["before"]; ok {
		by, _ := json.Marshal(b)
		return by
	}
	return nil
}
func (m *mockMessage) After() []byte {
	if a, ok := m.data["after"]; ok {
		by, _ := json.Marshal(a)
		return by
	}
	return nil
}
func (m *mockMessage) Payload() []byte {
	if a, ok := m.data["after"]; ok {
		by, _ := json.Marshal(a)
		return by
	}
	// Fallback to marshaling the whole data map if no "after"
	by, _ := json.Marshal(m.data)
	return by
}
func (m *mockMessage) SetMetadata(k, v string) {}
func (m *mockMessage) SetData(k string, v any) {}
func (m *mockMessage) Clone() hermod.Message   { return nil }
func (m *mockMessage) ToMap() map[string]any   { return nil }
func (m *mockMessage) ClearPayloads()          {}
func (m *mockMessage) Retain()                 {}
func (m *mockMessage) Release()                {}

// finiteFloat passes a float through unless it has no JSON form.
//
// NaN and +/-Inf make json.Marshal fail on the whole row, which resolves every
// path in it to nil. Falling back preserves that rather than quietly making
// one row's reads start working.
func finiteFloat(f float64) (any, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	return f, true
}

// integerAsFloat reports v as the float64 a JSON round trip turns it into.
func integerAsFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	}
	return 0, false
}

// maxCachedPatterns bounds the compiled-pattern cache.
//
// A bound is not optional here. EvaluateConditions resolves a condition value
// containing {{ }} against the message's own data before using it, so the
// pattern reaching compilePattern can be derived from message content — and a
// map keyed by that, without a cap and an eviction, is a memory leak an
// upstream system can drive.
const maxCachedPatterns = 512

// patternEvictionBatch is how many entries are dropped when the cache is full.
// Evicting one per insert would put an eviction on every call once full;
// evicting a slice of them amortises it.
const patternEvictionBatch = maxCachedPatterns / 8

// compiledPattern holds a compile's outcome, failures included. A pattern that
// does not compile is cached too: it is the cheapest way to stop a stream of
// messages carrying the same malformed pattern from paying for the same failed
// compile over and over.
type compiledPattern struct {
	re  *regexp.Regexp
	err error
}

var compiledPatterns = struct {
	mu sync.RWMutex
	m  map[string]compiledPattern
}{m: make(map[string]compiledPattern, maxCachedPatterns)}

func cachedPatternCount() int {
	compiledPatterns.mu.RLock()
	defer compiledPatterns.mu.RUnlock()
	return len(compiledPatterns.m)
}

// compilePattern returns the compiled form of expr, reusing an earlier compile.
//
// A router condition compiled its pattern once per message: 1931ns and 69
// allocations against 208ns and 1 for a match against an already-compiled
// pattern, which at 100k msgs/s is roughly 0.2 of a core and 600 MB/s of
// garbage for a single filter.
func compilePattern(expr string) (*regexp.Regexp, error) {
	compiledPatterns.mu.RLock()
	hit, ok := compiledPatterns.m[expr]
	compiledPatterns.mu.RUnlock()
	if ok {
		return hit.re, hit.err
	}

	// Compiled outside the lock: an expensive pattern must not block every
	// other condition being evaluated. Two goroutines racing on the same new
	// pattern both compile it and the second overwrites the first, which costs
	// one redundant compile and keeps the lock hold short.
	re, err := regexp.Compile(expr)

	compiledPatterns.mu.Lock()
	defer compiledPatterns.mu.Unlock()
	if len(compiledPatterns.m) >= maxCachedPatterns {
		// Go randomises map iteration order, so taking the first entries the
		// range hands back is an arbitrary eviction. That is deliberate: no
		// ordering means nothing for a caller to steer, where an LRU would let
		// a message stream decide what stays resident.
		evicted := 0
		for k := range compiledPatterns.m {
			delete(compiledPatterns.m, k)
			if evicted++; evicted >= patternEvictionBatch {
				break
			}
		}
	}
	compiledPatterns.m[expr] = compiledPattern{re: re, err: err}
	return re, err
}

// ValidateConditions reports the first condition whose regex cannot compile.
//
// It exists because a pattern that does not compile is not a condition that
// matches nothing — it is a condition that *rejects everything*, silently:
// EvaluateConditions starts `match` at false and swallows the compile error,
// so a typo in a filter drops 100% of a workflow's traffic while the workflow
// stays green. Call this where a human can still act on the answer — at save
// time — and again at run time, where the failure must at least be loud.
//
// A pattern containing a template token is left alone. It is resolved per
// message against that message's own data, so there is nothing to judge here
// and rejecting it would refuse a legitimate workflow.
func ValidateConditions(conditions []map[string]any) error {
	for _, cond := range conditions {
		op, _ := cond["operator"].(string)
		if op != "regex" && op != "not_regex" {
			continue
		}
		pattern, ok := cond["value"].(string)
		if !ok {
			continue
		}
		if strings.Contains(pattern, "{{") && strings.Contains(pattern, "}}") {
			continue
		}
		if _, err := compilePattern(pattern); err != nil {
			field, _ := cond["field"].(string)
			return fmt.Errorf("condition on field %q has an invalid %s pattern %q: %w", field, op, pattern, err)
		}
	}
	return nil
}

// ParseConditions reads the condition list out of a node's configuration.
//
// The editor writes either a JSON `conditions` array or the single
// field/operator/value triple, and both shapes are in saved workflows. This is
// the one definition of how to read them: it is needed by the node that
// evaluates them at run time and by the validator that checks them at save
// time, and a second hand-written copy in the validator would drift from this
// one the first time the shape changed — the way the wizard's requirement list
// and the sink form map both have.
func ParseConditions(config map[string]any) []map[string]any {
	conditions := ParseObjectList(config["conditions"])

	if len(conditions) == 0 {
		field, _ := config["field"].(string)
		op, _ := config["operator"].(string)
		val, _ := config["value"].(string)
		if field != "" {
			conditions = append(conditions, map[string]any{
				"field":    field,
				"operator": op,
				"value":    val,
			})
		}
	}
	return conditions
}

// ParseObjectList reads a list of objects out of a node config value, whatever
// shape the editor happened to save it in.
//
// The editors disagree, and the disagreement is invisible until a message runs
// through: RouterEditor.tsx and FilterDataConfig.tsx JSON.stringify their list
// before calling updateNodeConfig, while SwitchConfig.tsx and ConditionConfig.tsx
// hand it over as a plain array. updateNodeConfig merges its argument straight
// into node.data, so the array shape survives all the way to the engine as
// []any. Reading only the string form made the type assertion fail and produced
// an empty list, and an empty list is not an error anywhere downstream -- it is
// "no cases matched" (switch falls to default) and "no conditions to check"
// (EvaluateConditions returns true). Both nodes ignored a configuration the user
// could see on screen, and neither logged anything.
//
// Accepting both shapes here is what keeps the two editors from having to agree.
func ParseObjectList(raw any) []map[string]any {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		// Parsed once per distinct config rather than once per message. Nodes
		// call this before evaluating anything, so the same bytes were
		// unmarshalled for the life of the workflow to produce the same answer
		// every time: 24 allocations a message.
		return cloneConditions(parseConditionList(v))
	case []map[string]any:
		return cloneConditions(v)
	case []any:
		// What a JSONB round trip leaves behind for an array the editor saved.
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// isJSONNativeMap reports whether every value in m, recursively, is already
// the type a JSON decode would have produced. For such a map, marshalling and
// unmarshalling it is the identity, which is what lets SetValByPath skip it.
func isJSONNativeMap(m map[string]any) bool {
	for _, v := range m {
		if !isJSONNativeValue(v) {
			return false
		}
	}
	return true
}

func isJSONNativeValue(v any) bool {
	switch t := v.(type) {
	case nil, string, bool:
		return true
	case float64:
		// NaN and +/-Inf make json.Marshal fail, which turns the whole write
		// into a no-op. The fast path must not quietly start succeeding.
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	case map[string]any:
		return isJSONNativeMap(t)
	case []any:
		for _, e := range t {
			if !isJSONNativeValue(e) {
				return false
			}
		}
		return true
	}
	return false
}

// setByWalk writes val at path by walking the map, and reports whether it
// could. False means the path uses syntax it does not implement, or leads
// somewhere it will not go — ask sjson instead.
//
// It deliberately refuses anything but plain object-field writes: an array
// index, an sjson append (`-1`), a metacharacter, or an intermediate segment
// occupied by something that is not an object. Those are rare and sjson
// already gets them right.
func setByWalk(data map[string]any, path string, val any) bool {
	if strings.ContainsAny(path, gjsonPathMeta) {
		return false
	}

	segs := strings.Split(path, ".")
	for _, s := range segs {
		if s == "" || isIndexLikeSegment(s) {
			return false
		}
	}

	// sjson writes val into the JSON and the result is decoded back out, so
	// what lands in the map is val's JSON form, not val.
	norm, ok := normalizeForWrite(val)
	if !ok {
		return false
	}

	// Probe before mutating: a path that turns out to be unwalkable halfway
	// down must not leave intermediate objects behind for the round trip to
	// then disagree with.
	cur := data
	for _, s := range segs[:len(segs)-1] {
		next, present := cur[s]
		if !present {
			break // everything below here will be created
		}
		m, isMap := next.(map[string]any)
		if !isMap {
			return false // sjson would replace it; let sjson do that
		}
		cur = m
	}

	cur = data
	for _, s := range segs[:len(segs)-1] {
		m, isMap := cur[s].(map[string]any)
		if !isMap {
			m = make(map[string]any)
			cur[s] = m
		}
		cur = m
	}
	cur[segs[len(segs)-1]] = norm
	return true
}

// isIndexLikeSegment reports whether a path segment addresses an array rather
// than an object field — a bare index, or sjson's `-1` append.
func isIndexLikeSegment(s string) bool {
	if s == "-1" {
		return true
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// normalizeForWrite returns val as sjson-plus-decode would have left it, and
// reports whether it is a type the fast path has been proved equivalent for.
//
// It is an allowlist rather than a mirror of sjson's type switch, because the
// two encoders do not agree everywhere: sjson writes a []byte as the literal
// string where json.Marshal writes base64, which
// TestSetValByPathMatchesJSONRoundTrip caught. Anything not listed here goes to
// sjson, which is the only thing that definitionally matches sjson.
func normalizeForWrite(val any) (any, bool) {
	switch val.(type) {
	case nil, string, bool,
		float32, float64,
		int, int8, int16, int32, int64,
		uint, uint16, uint32, uint64,
		map[string]any, []any:
		return normalizeToJSONShape(val)
	}
	return nil, false
}

// maxCachedConditionLists bounds the parsed-conditions cache. A condition list
// comes from node configuration rather than from message data, so its
// cardinality is the number of distinct condition configs in the process — but
// it is bounded and evicting anyway, for the same reason compiledPatterns is.
const maxCachedConditionLists = 512

var parsedConditionLists = struct {
	mu sync.RWMutex
	m  map[string][]map[string]any
}{m: make(map[string][]map[string]any, maxCachedConditionLists)}

// parseConditionList returns the parse of a conditions JSON string, reusing an
// earlier one. The result is shared and must not be handed to a caller
// directly — see cloneConditions.
//
// A string that does not parse caches its nil, so a malformed config costs one
// failed unmarshal rather than one per message.
func parseConditionList(raw string) []map[string]any {
	parsedConditionLists.mu.RLock()
	hit, ok := parsedConditionLists.m[raw]
	parsedConditionLists.mu.RUnlock()
	if ok {
		return hit
	}

	var parsed []map[string]any
	_ = json.Unmarshal([]byte(raw), &parsed)

	parsedConditionLists.mu.Lock()
	defer parsedConditionLists.mu.Unlock()
	if len(parsedConditionLists.m) >= maxCachedConditionLists {
		evicted := 0
		for k := range parsedConditionLists.m {
			delete(parsedConditionLists.m, k)
			if evicted++; evicted >= maxCachedConditionLists/8 {
				break
			}
		}
	}
	parsedConditionLists.m[raw] = parsed
	return parsed
}

// cloneConditions copies a cached condition list so a caller cannot reach into
// the cache.
//
// Nothing mutates the result today — EvaluateConditions and ValidateConditions
// only read it — but handing out shared mutable state is how that stops being
// true, and the failure would be one workflow's filter silently rewriting
// another's. The copy is shallow: the values inside a condition are the
// scalars the editor writes, and a caller that replaces one replaces its own
// map entry, not the cache's.
func cloneConditions(src []map[string]any) []map[string]any {
	if src == nil {
		return nil
	}
	out := make([]map[string]any, len(src))
	for i, c := range src {
		out[i] = maps.Clone(c)
	}
	return out
}

// stringify renders v as the text a condition compares, without the reflection
// for the types a decoded message actually holds.
//
// EvaluateConditions formats both sides of every condition before it knows
// which operator it is applying, so a numeric comparison paid for two strings
// it never read — per condition, per message. The short-circuits here cover
// what a JSON-decoded row contains (string, bool, float64) plus the integer
// kinds a Go-built message can carry.
//
// A number is rendered the way JSON renders it, not the way %v does, and the
// difference is not cosmetic. GetValByPath deliberately normalises every field
// to what a JSON round trip produces; the API then hands the browser that same
// number, and the sample panel shows the user what JSON.parse gives back. The
// text a condition matches has to be the text the user was shown. Under %v it
// was not: %v is %g, which switches to an exponent above 1e6, so a field
// holding 1704207845 compared as "1.704207845e+09" while the wire, the browser
// and the editor's own simulator all said 1704207845. Every id, timestamp and
// amount wide enough to matter failed `=`, `contains` and `regex` — and
// nothing under a million did, so test data looked fine.
//
// Outside the numbers it still agrees with %v exactly, including for the
// non-finite floats that have no JSON form at all. A condition is a filter, so
// a formatting difference is a data difference: TestStringifyMatchesSprintf and
// TestStringifyNumberMatchesTheWire hold both halves.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return formatJSONFloat(t, 64)
	case float32:
		return formatJSONFloat(float64(t), 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		return marshalForCondition(t)
	case map[string]any:
		return marshalForCondition(t)
	}
	return fmt.Sprintf("%v", v)
}

// marshalForCondition renders a composite field as the JSON the user was
// shown.
//
// %v spells a decoded object `map[a:1 b:2]` and an array `[1 2]` — Go syntax
// that appears nowhere else in the product. A `contains` against a jsonb
// column, which is how you filter one without a path, could therefore never be
// written: the text being searched was in a notation the sample panel never
// displays. JSON is what the field already is, what the API sends, and what the
// editor's simulator compares, and Go sorts map keys when it marshals, so the
// rendering is stable across messages.
func marshalForCondition(v any) string {
	// NaN and the infinities make Marshal fail, and a condition still has to
	// answer, so the old spelling remains the fallback.
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// formatJSONFloat renders f exactly as encoding/json would, which is also what
// JavaScript's Number#toString produces — the two agree by construction, and
// that agreement is the point: it is what puts the engine and the editor's
// client-side simulator on the same string.
//
// NaN and the infinities have no JSON representation (json.Marshal fails on
// them), so they keep the %v spelling they have always had rather than
// acquiring a new one here.
func formatJSONFloat(f float64, bits int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'g', -1, bits)
	}

	// The thresholds are encoding/json's, and ECMAScript's before it: fixed
	// notation over the range a reader would recognise, an exponent only
	// outside it.
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}

	b := strconv.AppendFloat(make([]byte, 0, 24), f, format, -1, bits)
	if format == 'e' {
		// encoding/json trims the exponent's leading zero: e-09 becomes e-9.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return string(b)
}

// numericallyEqual reports whether a numeric field and a configured value are
// the same number written differently.
//
// `>` has always compared a numeric field as a number while `=` compared it as
// text, so one case list ran under two type regimes: a money field holding 100
// was greater than "99.99" and simultaneously not equal to "100.00". This
// closes that, narrowly. Exact text equality is still checked first, so nothing
// that matched before stops matching — this can only add a match between two
// spellings of one number. And it only applies when the *field* is genuinely
// numeric, so a string identifier keeps string semantics and "007" does not
// start equalling "7".
//
// The comparison is at float64 resolution, which is the resolution the field
// already has: GetValByPath normalised it through JSON long before this.
func numericallyEqual(fieldRaw, val any) bool {
	if !isNumeric(fieldRaw) {
		return false
	}
	v1, ok1 := ToFloat64(fieldRaw)
	v2, ok2 := ToFloat64(val)
	return ok1 && ok2 && v1 == v2
}
