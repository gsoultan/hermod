package evaluator

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/user/hermod"
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
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
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
			old := fmt.Sprintf("%v", args[1])
			new := fmt.Sprintf("%v", args[2])
			return strings.ReplaceAll(s, old, new)
		}
	case "concat":
		var sb strings.Builder
		for _, arg := range args {
			if arg != nil {
				sb.WriteString(fmt.Sprintf("%v", arg))
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
		for _, arg := range args {
			if ToBool(arg) {
				return true
			}
		}
		return false
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
			v, _ := ToFloat64(args[0])
			return int64(v)
		}
	case "tofloat":
		if len(args) > 0 {
			v, _ := ToFloat64(args[0])
			return v
		}
	}
	return nil
}

// Path helpers

func GetValByPath(data map[string]any, path string) any {
	if path == "" {
		return nil
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

func GetMsgValByPath(msg hermod.Message, path string) any {
	if path == "" || msg == nil {
		return nil
	}

	// Expose CDC/meta virtual fields for filtering/templates
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
		if md := msg.Metadata(); md != nil {
			if v, ok := md[key]; ok {
				return v
			}
		}
	}

	// 1) Try the path as-is
	if v := GetValByPath(msg.Data(), path); v != nil {
		return v
	}

	// 2) Try raw payloads if data doesn't have it
	// This handles cases where Data() only contains "after" or is empty (like in deletes)
	if strings.HasPrefix(lower, "before.") {
		base := path[7:]
		if v := getValueFromRaw(msg.Before(), base); v != nil {
			return v
		}
		// Fallback to Data() if field not found in raw (could happen if manually structured)
		if v := GetValByPath(msg.Data(), base); v != nil {
			return v
		}
	}
	if strings.HasPrefix(lower, "after.") {
		base := path[6:]
		if v := getValueFromRaw(msg.Payload(), base); v != nil {
			return v
		}
		// Fallback to Data() if payload is empty or field not found in raw
		if v := GetValByPath(msg.Data(), base); v != nil {
			return v
		}
	}

	// Try direct in before/after if not prefixed
	if v := getValueFromRaw(msg.Payload(), path); v != nil {
		return v
	}
	if v := getValueFromRaw(msg.Before(), path); v != nil {
		return v
	}

	return nil
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
		for k, v := range newData {
			data[k] = v
		}
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
	case string:
		// Align with UI simulator: be lenient about surrounding whitespace
		s := strings.TrimSpace(v)
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			f, err := strconv.ParseFloat(s, 64)
			return int64(f), err == nil
		}
		return i, true
	}
	return 0, false
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

// Template resolver

func ResolveTemplate(temp string, data map[string]any) string {
	result := temp
	for {
		start := strings.Index(result, "{{")
		if start == -1 {
			break
		}

		end := strings.Index(result[start:], "}}")
		if end == -1 {
			break
		}

		fullTag := result[start : start+end+2]
		path := strings.TrimSpace(result[start+2 : start+end])

		var val any
		if strings.HasPrefix(path, "env.") {
			envVar := strings.TrimPrefix(path, "env.")
			val = os.Getenv(envVar)
		} else if strings.Contains(path, "(") && strings.HasSuffix(path, ")") {
			e := NewEvaluator()
			// Create a mock message if data is provided, to allow accessing it via source.path
			var msg hermod.Message
			if data != nil {
				msg = &mockMessage{data: data}
			}
			val = e.ParseAndEvaluate(msg, path)
		} else {
			// Support ".field" style (UI templates commonly use a leading dot)
			p := path
			if strings.HasPrefix(p, ".") {
				p = strings.TrimPrefix(p, ".")
			}
			val = GetValByPath(data, p)
		}

		valStr := ""
		if val != nil {
			switch v := val.(type) {
			case string:
				valStr = v
			default:
				valStr = fmt.Sprintf("%v", v)
			}
		}

		result = strings.Replace(result, fullTag, valStr, 1)
	}
	return result
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

		fieldValRaw := GetMsgValByPath(msg, field)
		// Treat missing values consistently as empty string (UI simulator behavior)
		fieldVal := ""
		if fieldValRaw != nil {
			fieldVal = fmt.Sprintf("%v", fieldValRaw)
		}

		// Resolve templates/expressions in the value if present
		var valResolved any = val
		if vs, ok := val.(string); ok {
			if strings.Contains(vs, "{{") && strings.Contains(vs, "}}") {
				valResolved = ResolveTemplate(vs, msg.Data())
			} else {
				valResolved = vs
			}
		}
		valStr := ""
		if valResolved != nil {
			valStr = fmt.Sprintf("%v", valResolved)
		}

		switch op {
		case "=":
			match = fieldVal == valStr
		case "!=":
			match = fieldVal != valStr
		case ">", ">=", "<", "<=":
			v1, ok1 := ToFloat64(fieldValRaw)
			v2, ok2 := ToFloat64(valResolved)
			if ok1 && ok2 {
				switch op {
				case ">":
					match = v1 > v2
				case ">=":
					match = v1 >= v2
				case "<":
					match = v1 < v2
				case "<=":
					match = v1 <= v2
				}
			} else {
				// Fallback to string comparison if not numbers
				switch op {
				case ">":
					match = fieldVal > valStr
				case ">=":
					match = fieldVal >= valStr
				case "<":
					match = fieldVal < valStr
				case "<=":
					match = fieldVal <= valStr
				}
			}
		case "contains":
			match = strings.Contains(fieldVal, valStr)
		case "not_contains":
			match = !strings.Contains(fieldVal, valStr)
		case "regex":
			re, err := regexp.Compile(valStr)
			if err == nil {
				match = re.MatchString(fieldVal)
			}
		case "not_regex":
			re, err := regexp.Compile(valStr)
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

func (m *mockMessage) ID() string                  { return m.id }
func (m *mockMessage) Operation() hermod.Operation { return m.op }
func (m *mockMessage) Table() string               { return m.table }
func (m *mockMessage) Schema() string              { return m.schema }
func (m *mockMessage) Data() map[string]any        { return m.data }
func (m *mockMessage) Metadata() map[string]string { return m.metadata }
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
func (m *mockMessage) ClearPayloads()          {}
