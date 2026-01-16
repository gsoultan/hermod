package transformer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/xeipuuv/gojsonschema"
	"github.com/yuin/gopher-lua"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Chain struct {
	transformers []hermod.Transformer
}

func NewChain(transformers ...hermod.Transformer) *Chain {
	return &Chain{transformers: transformers}
}

func (c *Chain) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	var err error
	currentMsg := msg
	for _, t := range c.transformers {
		currentMsg, err = t.Transform(ctx, currentMsg)
		if err != nil {
			return nil, err
		}
		if currentMsg == nil {
			return nil, nil
		}
	}
	return currentMsg, nil
}

func (c *Chain) Close() error {
	var errs []string
	for _, t := range c.transformers {
		if err := t.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing transformers: %s", strings.Join(errs, "; "))
	}
	return nil
}

type FilterOperationTransformer struct {
	Operations map[hermod.Operation]bool
}

func (t *FilterOperationTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	if t.Operations[msg.Operation()] {
		return msg, nil
	}
	return nil, nil // Returning nil message means it's filtered out
}

func (t *FilterOperationTransformer) Close() error { return nil }

type FilterDataTransformer struct {
	Field      string
	Operator   string
	Value      string
	Operations map[hermod.Operation]bool
	regex      *regexp.Regexp
}

func (t *FilterDataTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	// 1. Operation filtering
	if len(t.Operations) > 0 {
		if !t.Operations[msg.Operation()] {
			return nil, nil
		}
	}

	// 2. Data filtering
	if t.Field != "" {
		data := getMessageData(msg)
		val, exists := getValueAtPath(data, t.Field)
		if !t.evaluate(val, exists) {
			return nil, nil
		}
	}

	return msg, nil
}

func (t *FilterDataTransformer) evaluate(val interface{}, exists bool) bool {
	switch t.Operator {
	case "exists":
		return exists
	case "not_exists":
		return !exists
	case "is_null":
		return exists && val == nil
	case "is_not_null":
		return exists && val != nil
	}

	if !exists {
		// If field doesn't exist, it's not equal to anything
		return t.Operator == "neq" || t.Operator == "!="
	}

	return t.compare(val, t.Operator, t.Value)
}

func (t *FilterDataTransformer) Close() error { return nil }

func (t *FilterDataTransformer) compare(actual interface{}, op string, expected string) bool {
	if op == "regex" && t.regex != nil {
		return t.regex.MatchString(fmt.Sprintf("%v", actual))
	}

	actualStr := fmt.Sprintf("%v", actual)

	switch op {
	case "eq", "=":
		return actualStr == expected
	case "neq", "!=":
		return actualStr != expected
	case "gt", ">":
		a, err1 := strconv.ParseFloat(actualStr, 64)
		e, err2 := strconv.ParseFloat(expected, 64)
		if err1 == nil && err2 == nil {
			return a > e
		}
		return actualStr > expected
	case "lt", "<":
		a, err1 := strconv.ParseFloat(actualStr, 64)
		e, err2 := strconv.ParseFloat(expected, 64)
		if err1 == nil && err2 == nil {
			return a < e
		}
		return actualStr < expected
	case "contains":
		return strings.Contains(actualStr, expected)
	case "starts_with":
		return strings.HasPrefix(actualStr, expected)
	case "ends_with":
		return strings.HasSuffix(actualStr, expected)
	}
	return false
}

type MappingTransformer struct {
	Mapping map[string]string // source_field -> target_field
	Strict  bool
}

func (t *MappingTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	data := getMessageData(msg)
	newData := make(map[string]interface{})

	if !t.Strict {
		for k, v := range data {
			newData[k] = v
		}
	}

	for src, target := range t.Mapping {
		val, ok := getValueAtPath(data, src)
		if ok {
			// If we are renaming a field (src != target) or dropping it (target == ""),
			// we should remove the original if not in strict mode.
			// Actually the old implementation's behavior for non-strict was:
			// 1. Copy all fields
			// 2. For each mapping:
			//    a. Get value at src
			//    b. If target != "", set value at target
			//    c. If target != src, delete value at src (to handle rename)
			//    d. If target == "", delete value at src (to handle drop)

			if !t.Strict {
				// delete original
				setValueAtPath(newData, src, nil)
			}

			if target != "" {
				setValueAtPath(newData, target, val)
			}
		}
	}

	setMessageData(msg, newData, t.Strict)
	return msg, nil
}

func (t *MappingTransformer) Close() error { return nil }

type AdvancedTransformer struct {
	Mapping map[string]string // target_field -> expression
	Strict  bool
}

func (t *AdvancedTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	data := getMessageData(msg)

	var newData map[string]interface{}
	if t.Strict {
		newData = make(map[string]interface{})
	} else {
		newData = make(map[string]interface{})
		for k, v := range data {
			newData[k] = v
		}
	}

	for targetField, expr := range t.Mapping {
		val := t.evaluateExpression(expr, data)
		if val != nil {
			setValueAtPath(newData, targetField, val)
		}
	}

	setMessageData(msg, newData, t.Strict)
	return msg, nil
}

func (t *AdvancedTransformer) Close() error { return nil }

func (t *AdvancedTransformer) evaluateExpression(expr string, source map[string]interface{}) interface{} {
	expr = strings.TrimSpace(expr)

	if strings.HasPrefix(expr, "source.") {
		path := strings.TrimPrefix(expr, "source.")
		val, _ := getValueAtPath(source, path)
		return val
	}

	if strings.HasPrefix(expr, "system.") {
		sysVar := strings.TrimPrefix(expr, "system.")
		switch sysVar {
		case "now":
			return time.Now().Format(time.RFC3339)
		case "uuid":
			return uuid.New().String()
		case "space":
			return " "
		case "tab":
			return "\t"
		case "newline":
			return "\n"
		case "comma":
			return ","
		case "semicolon":
			return ";"
		}
	}

	if strings.HasPrefix(expr, "const.") {
		constVal := strings.TrimPrefix(expr, "const.")
		if n, err := strconv.ParseFloat(constVal, 64); err == nil {
			return n
		}
		if b, err := strconv.ParseBool(constVal); err == nil {
			return b
		}
		return constVal
	}

	// Check for functions: func(arg1, arg2, ...)
	if strings.Contains(expr, "(") && strings.HasSuffix(expr, ")") {
		idx := strings.Index(expr, "(")
		funcName := strings.ToLower(expr[:idx])
		argsStr := expr[idx+1 : len(expr)-1]

		// Split args by comma, but be careful with nested parentheses
		args := t.splitArgs(argsStr)
		evaluatedArgs := make([]interface{}, len(args))
		for i, arg := range args {
			evaluatedArgs[i] = t.evaluateExpression(arg, source)
		}

		return t.callFunction(funcName, evaluatedArgs)
	}

	// Fallback: check if it's a field path without source. prefix
	val, ok := getValueAtPath(source, expr)
	if ok {
		return val
	}

	// Default: treat as literal string
	return expr
}

func (t *AdvancedTransformer) splitArgs(s string) []string {
	var args []string
	var current strings.Builder
	depth := 0
	for _, r := range s {
		if r == '(' {
			depth++
			current.WriteRune(r)
		} else if r == ')' {
			depth--
			current.WriteRune(r)
		} else if r == ',' && depth == 0 {
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		} else {
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func (t *AdvancedTransformer) callFunction(name string, args []interface{}) interface{} {
	switch name {
	case "upper":
		if len(args) > 0 {
			return strings.ToUpper(fmt.Sprintf("%v", args[0]))
		}
	case "lower":
		if len(args) > 0 {
			return strings.ToLower(fmt.Sprintf("%v", args[0]))
		}
	case "trim":
		if len(args) > 0 {
			return strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		}
	case "concat":
		var res strings.Builder
		for _, arg := range args {
			res.WriteString(fmt.Sprintf("%v", arg))
		}
		return res.String()
	case "substring":
		if len(args) >= 3 {
			s := fmt.Sprintf("%v", args[0])
			start, _ := strconv.Atoi(fmt.Sprintf("%v", args[1]))
			length, _ := strconv.Atoi(fmt.Sprintf("%v", args[2]))
			if start < 0 {
				start = 0
			}
			if start > len(s) {
				return ""
			}
			end := start + length
			if end > len(s) {
				end = len(s)
			}
			return s[start:end]
		}
	case "replace":
		if len(args) >= 3 {
			s := fmt.Sprintf("%v", args[0])
			old := fmt.Sprintf("%v", args[1])
			newS := fmt.Sprintf("%v", args[2])
			return strings.ReplaceAll(s, old, newS)
		}
	case "abs":
		if len(args) > 0 {
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64); err == nil {
				return math.Abs(f)
			}
		}
	case "to_int":
		if len(args) > 0 {
			if i, err := strconv.Atoi(fmt.Sprintf("%v", args[0])); err == nil {
				return i
			}
			// handle float to int
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64); err == nil {
				return int(f)
			}
		}
	case "to_float":
		if len(args) > 0 {
			if f, err := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64); err == nil {
				return f
			}
		}
	case "to_string":
		if len(args) > 0 {
			return fmt.Sprintf("%v", args[0])
		}
	case "default":
		if len(args) >= 2 {
			if args[0] == nil || fmt.Sprintf("%v", args[0]) == "" {
				return args[1]
			}
			return args[0]
		}
	case "coalesce":
		for _, arg := range args {
			if arg != nil && arg != "" {
				return arg
			}
		}
	case "eq":
		if len(args) >= 2 {
			return fmt.Sprintf("%v", args[0]) == fmt.Sprintf("%v", args[1])
		}
	case "neq":
		if len(args) >= 2 {
			return fmt.Sprintf("%v", args[0]) != fmt.Sprintf("%v", args[1])
		}
	case "gt":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return f1 > f2
		}
	case "lt":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return f1 < f2
		}
	case "gte":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return f1 >= f2
		}
	case "lte":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return f1 <= f2
		}
	case "date_format":
		if len(args) >= 2 {
			tStr := fmt.Sprintf("%v", args[0])
			layout := fmt.Sprintf("%v", args[1])
			parsed, err := time.Parse(time.RFC3339, tStr)
			if err != nil {
				if unix, err := strconv.ParseInt(tStr, 10, 64); err == nil {
					parsed = time.Unix(unix, 0)
				} else {
					return tStr
				}
			}
			return parsed.Format(layout)
		}
	case "date_parse":
		if len(args) >= 2 {
			tStr := fmt.Sprintf("%v", args[0])
			layout := fmt.Sprintf("%v", args[1])
			parsed, err := time.Parse(layout, tStr)
			if err != nil {
				return tStr
			}
			return parsed.Format(time.RFC3339)
		}
	case "unix_now":
		return time.Now().Unix()
	case "base64_encode":
		if len(args) > 0 {
			return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", args[0])))
		}
	case "base64_decode":
		if len(args) > 0 {
			decoded, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", args[0]))
			if err != nil {
				return args[0]
			}
			return string(decoded)
		}
	case "url_encode":
		if len(args) > 0 {
			return url.QueryEscape(fmt.Sprintf("%v", args[0]))
		}
	case "url_decode":
		if len(args) > 0 {
			decoded, err := url.QueryUnescape(fmt.Sprintf("%v", args[0]))
			if err != nil {
				return args[0]
			}
			return decoded
		}
	case "and":
		for _, arg := range args {
			condition := false
			if b, ok := arg.(bool); ok {
				condition = b
			} else if arg != nil && arg != "" && arg != 0 && arg != 0.0 && arg != false {
				condition = true
			}
			if !condition {
				return false
			}
		}
		return true
	case "or":
		for _, arg := range args {
			condition := false
			if b, ok := arg.(bool); ok {
				condition = b
			} else if arg != nil && arg != "" && arg != 0 && arg != 0.0 && arg != false {
				condition = true
			}
			if condition {
				return true
			}
		}
		return false
	case "not":
		if len(args) > 0 {
			condition := false
			if b, ok := args[0].(bool); ok {
				condition = b
			} else if args[0] != nil && args[0] != "" && args[0] != 0 && args[0] != 0.0 && args[0] != false {
				condition = true
			}
			return !condition
		}
	case "if":
		if len(args) >= 3 {
			condition := false
			if b, ok := args[0].(bool); ok {
				condition = b
			} else if args[0] != nil && args[0] != "" && args[0] != 0 && args[0] != 0.0 && args[0] != false {
				condition = true
			}
			if condition {
				return args[1]
			}
			return args[2]
		}
	case "add":
		var sum float64
		for _, arg := range args {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", arg), 64)
			sum += f
		}
		return sum
	case "sub":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return f1 - f2
		}
	case "mul":
		if len(args) == 0 {
			return 0.0
		}
		res := 1.0
		for _, arg := range args {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", arg), 64)
			res *= f
		}
		return res
	case "div":
		if len(args) >= 2 {
			f1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			f2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			if f2 == 0 {
				return 0.0
			}
			return f1 / f2
		}
	case "round":
		if len(args) > 0 {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			return math.Round(f)
		}
	case "floor":
		if len(args) > 0 {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			return math.Floor(f)
		}
	case "ceil":
		if len(args) > 0 {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			return math.Ceil(f)
		}
	case "min":
		if len(args) == 0 {
			return nil
		}
		minVal, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
		for i := 1; i < len(args); i++ {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[i]), 64)
			if f < minVal {
				minVal = f
			}
		}
		return minVal
	case "max":
		if len(args) == 0 {
			return nil
		}
		maxVal, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
		for i := 1; i < len(args); i++ {
			f, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[i]), 64)
			if f > maxVal {
				maxVal = f
			}
		}
		return maxVal
	case "sha256":
		if len(args) > 0 {
			h := sha256.Sum256([]byte(fmt.Sprintf("%v", args[0])))
			return hex.EncodeToString(h[:])
		}
	case "md5":
		if len(args) > 0 {
			h := md5.Sum([]byte(fmt.Sprintf("%v", args[0])))
			return hex.EncodeToString(h[:])
		}
	case "split":
		if len(args) >= 2 {
			s := fmt.Sprintf("%v", args[0])
			sep := fmt.Sprintf("%v", args[1])
			return strings.Split(s, sep)
		}
	case "join":
		if len(args) >= 2 {
			sep := fmt.Sprintf("%v", args[1])
			if arr, ok := args[0].([]string); ok {
				return strings.Join(arr, sep)
			}
			if arr, ok := args[0].([]interface{}); ok {
				strs := make([]string, len(arr))
				for i, v := range arr {
					strs[i] = fmt.Sprintf("%v", v)
				}
				return strings.Join(strs, sep)
			}
		}
	}
	return nil
}

type HttpTransformer struct {
	URL        string
	Method     string
	Headers    map[string]string
	MaxRetries int
}

var defaultHttpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	},
}

func (t *HttpTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	data := getMessageData(msg)

	url := t.URL
	if data != nil {
		// Replace placeholders like {user.id} with values from data
		re := regexp.MustCompile(`\{([a-zA-Z0-9_\.]+)\}`)
		url = re.ReplaceAllStringFunc(t.URL, func(match string) string {
			path := match[1 : len(match)-1]
			val, ok := getValueAtPath(data, path)
			if ok && val != nil {
				return fmt.Sprintf("%v", val)
			}
			return match // Keep original if not found
		})
	}

	maxAttempts := t.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, t.Method, url, nil)
		if err != nil {
			return nil, err
		}

		for k, v := range t.Headers {
			req.Header.Set(k, v)
		}

		resp, err := defaultHttpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// If successful, update the message After with the response
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			dm.ClearPayloads()
			dm.SetAfter(body)
			return dm, nil
		}

		lastErr = fmt.Errorf("http error: status %d, body: %s", resp.StatusCode, string(body))
		if resp.StatusCode < 500 {
			// Don't retry client errors (4xx)
			break
		}
	}

	return nil, fmt.Errorf("http transformation failed after %d attempts: %w", maxAttempts, lastErr)
}

func (t *HttpTransformer) Close() error { return nil }

type SqlTransformer struct {
	Driver string
	Conn   string
	Query  string
	db     *sql.DB
	mu     sync.Mutex
}

var sqlParamRegex = regexp.MustCompile(`:([a-zA-Z0-9_\.]+)`)

func (t *SqlTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	t.mu.Lock()
	if t.db == nil {
		db, err := sql.Open(t.Driver, t.Conn)
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		t.db = db
	}
	t.mu.Unlock()

	data := getMessageData(msg)

	query := t.Query
	var args []interface{}

	if data != nil {
		i := 0
		query = sqlParamRegex.ReplaceAllStringFunc(t.Query, func(match string) string {
			path := match[1:]
			val, _ := getValueAtPath(data, path)
			args = append(args, val)
			placeholder := "?"
			if t.Driver == "postgres" || t.Driver == "pgx" {
				placeholder = fmt.Sprintf("$%d", i+1)
			}
			i++
			return placeholder
		})
	}

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	if rows.Next() {
		columns := make([]interface{}, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, err
		}

		rowMap := make(map[string]interface{})
		for i, colName := range cols {
			val := columns[i]
			if b, ok := val.([]byte); ok {
				rowMap[colName] = string(b)
			} else {
				rowMap[colName] = val
			}
		}

		setMessageData(msg, rowMap, true)
	}

	return msg, nil
}

func (t *SqlTransformer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.db != nil {
		return t.db.Close()
	}
	return nil
}

type RecoveryTransformer struct {
	Inner     hermod.Transformer
	OnFailure string // "fail", "skip"
}

func NewRecoveryTransformer(inner hermod.Transformer, onFailure string) *RecoveryTransformer {
	return &RecoveryTransformer{Inner: inner, OnFailure: onFailure}
}

func (t *RecoveryTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	newMsg, err := t.Inner.Transform(ctx, msg)
	if err != nil {
		if t.OnFailure == "skip" {
			return nil, nil // Filter out on failure
		}
		return nil, err
	}
	return newMsg, nil
}

func (t *RecoveryTransformer) Close() error {
	return t.Inner.Close()
}

type LuaTransformer struct {
	Script string
}

func (t *LuaTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	L := lua.NewState()
	defer L.Close()

	// Create message table in Lua
	luaMsg := L.NewTable()
	L.SetTable(luaMsg, lua.LString("id"), lua.LString(msg.ID()))
	L.SetTable(luaMsg, lua.LString("table"), lua.LString(msg.Table()))
	L.SetTable(luaMsg, lua.LString("operation"), lua.LString(string(msg.Operation())))
	L.SetTable(luaMsg, lua.LString("before"), lua.LString(string(msg.Before())))
	L.SetTable(luaMsg, lua.LString("after"), lua.LString(string(msg.After())))

	L.SetGlobal("msg", luaMsg)

	if err := L.DoString(t.Script); err != nil {
		return nil, fmt.Errorf("lua error: %w", err)
	}

	// Read back from Lua
	res := L.GetGlobal("msg")
	if tbl, ok := res.(*lua.LTable); ok {
		dm, ok := msg.(*message.DefaultMessage)
		if !ok {
			return msg, nil
		}

		after := L.GetTable(tbl, lua.LString("after"))
		if afterStr, ok := after.(lua.LString); ok {
			dm.SetAfter([]byte(afterStr))
		}

		before := L.GetTable(tbl, lua.LString("before"))
		if beforeStr, ok := before.(lua.LString); ok {
			dm.SetBefore([]byte(beforeStr))
		}

		tableName := L.GetTable(tbl, lua.LString("table"))
		if tableNameStr, ok := tableName.(lua.LString); ok {
			dm.SetTable(string(tableNameStr))
		}

		return dm, nil
	}

	return msg, nil
}

func (t *LuaTransformer) Close() error { return nil }

type SchemaTransformer struct {
	Schema string
}

func (t *SchemaTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	data := getMessageData(msg)
	if len(data) == 0 {
		return msg, nil
	}

	schemaLoader := gojsonschema.NewStringLoader(t.Schema)
	documentLoader := gojsonschema.NewGoLoader(data)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return nil, err
	}

	if result.Valid() {
		return msg, nil
	}

	var errs []string
	for _, desc := range result.Errors() {
		errs = append(errs, desc.String())
	}
	return nil, fmt.Errorf("schema validation failed: %s", strings.Join(errs, "; "))
}

func (t *SchemaTransformer) Close() error { return nil }

func getValueAtPath(m map[string]interface{}, path string) (interface{}, bool) {
	if val, ok := m[path]; ok {
		return val, true
	}
	parts := strings.Split(path, ".")
	var current interface{} = m
	for _, part := range parts {
		if curMap, ok := current.(map[string]interface{}); ok {
			if val, ok := curMap[part]; ok {
				current = val
			} else {
				return nil, false
			}
		} else {
			return nil, false
		}
	}
	return current, true
}

func setValueAtPath(m map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := m
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, ok := current[part]
		if !ok || next == nil {
			if value == nil {
				return
			}
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		} else if nextMap, ok := next.(map[string]interface{}); ok {
			current = nextMap
		} else {
			if value == nil {
				return
			}
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		}
	}

	lastPart := parts[len(parts)-1]
	if value == nil {
		delete(current, lastPart)
	} else {
		current[lastPart] = value
	}
}

type ValidatorTransformer struct {
	Rules []ValidationRule
}

type ValidationRule struct {
	Field    string
	Type     string // not_null, type, range, regex
	Config   string
	Severity string // fail, warn, skip
}

func (t *ValidatorTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	data := getMessageData(msg)

	for _, rule := range t.Rules {
		val, exists := getValueAtPath(data, rule.Field)
		valid := true
		errorMsg := ""

		switch rule.Type {
		case "not_null":
			if !exists || val == nil {
				valid = false
				errorMsg = fmt.Sprintf("field %s is null or missing", rule.Field)
			}
		case "type":
			switch rule.Config {
			case "string":
				_, ok := val.(string)
				if !ok {
					valid = false
				}
			case "number":
				_, ok1 := val.(float64)
				_, ok2 := val.(int)
				if !ok1 && !ok2 {
					valid = false
				}
			case "boolean":
				_, ok := val.(bool)
				if !ok {
					valid = false
				}
			}
			if !valid {
				errorMsg = fmt.Sprintf("field %s is not of type %s", rule.Field, rule.Config)
			}
		case "regex":
			s := fmt.Sprintf("%v", val)
			matched, _ := regexp.MatchString(rule.Config, s)
			if !matched {
				valid = false
				errorMsg = fmt.Sprintf("field %s does not match regex %s", rule.Field, rule.Config)
			}
		case "min":
			fVal, _ := strconv.ParseFloat(fmt.Sprintf("%v", val), 64)
			limit, _ := strconv.ParseFloat(rule.Config, 64)
			if fVal < limit {
				valid = false
				errorMsg = fmt.Sprintf("field %s value %v is less than %v", rule.Field, val, limit)
			}
		case "max":
			fVal, _ := strconv.ParseFloat(fmt.Sprintf("%v", val), 64)
			limit, _ := strconv.ParseFloat(rule.Config, 64)
			if fVal > limit {
				valid = false
				errorMsg = fmt.Sprintf("field %s value %v is greater than %v", rule.Field, val, limit)
			}
		case "min_len":
			s := fmt.Sprintf("%v", val)
			limit, _ := strconv.Atoi(rule.Config)
			if len(s) < limit {
				valid = false
				errorMsg = fmt.Sprintf("field %s length %d is less than %d", rule.Field, len(s), limit)
			}
		case "max_len":
			s := fmt.Sprintf("%v", val)
			limit, _ := strconv.Atoi(rule.Config)
			if len(s) > limit {
				valid = false
				errorMsg = fmt.Sprintf("field %s length %d is greater than %d", rule.Field, len(s), limit)
			}
		case "in":
			allowed := strings.Split(rule.Config, ",")
			sVal := fmt.Sprintf("%v", val)
			found := false
			for _, a := range allowed {
				if strings.TrimSpace(a) == sVal {
					found = true
					break
				}
			}
			if !found {
				valid = false
				errorMsg = fmt.Sprintf("field %s value %v is not in allowed list: %s", rule.Field, val, rule.Config)
			}
		}

		if !valid {
			if rule.Severity == "fail" {
				return nil, fmt.Errorf("validation failed: %s", errorMsg)
			} else if rule.Severity == "skip" {
				return nil, nil // Drop message
			}
			// warn: continue
		}
	}

	return msg, nil
}

func (t *ValidatorTransformer) Close() error { return nil }

type ConditionalTransformer struct {
	Condition string
	Inner     hermod.Transformer
}

func (t *ConditionalTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	if t.Condition == "" {
		return t.Inner.Transform(ctx, msg)
	}

	data := getMessageData(msg)

	// Add system/metadata to data for evaluation if needed
	// For now, let's use AdvancedTransformer's engine
	evaluator := &AdvancedTransformer{}
	result := evaluator.evaluateExpression(t.Condition, data)

	shouldExecute := false
	if b, ok := result.(bool); ok {
		shouldExecute = b
	} else if result != nil && result != "" && result != 0 && result != 0.0 && result != false {
		shouldExecute = true
	}

	if shouldExecute {
		return t.Inner.Transform(ctx, msg)
	}

	return msg, nil // Skip this transformer but keep the message
}

func (t *ConditionalTransformer) Close() error {
	return t.Inner.Close()
}

func NewTransformer(transType string, config map[string]string) (hermod.Transformer, error) {
	switch transType {
	case "filter_operation":
		ops := make(map[hermod.Operation]bool)
		if config["create"] == "true" {
			ops[hermod.OpCreate] = true
		}
		if config["update"] == "true" {
			ops[hermod.OpUpdate] = true
		}
		if config["delete"] == "true" {
			ops[hermod.OpDelete] = true
		}
		if config["snapshot"] == "true" {
			ops[hermod.OpSnapshot] = true
		}
		return &FilterOperationTransformer{Operations: ops}, nil
	case "filter_data", "filter":
		var re *regexp.Regexp
		var err error
		if config["operator"] == "regex" {
			re, err = regexp.Compile(config["value"])
			if err != nil {
				return nil, fmt.Errorf("invalid regex '%s': %w", config["value"], err)
			}
		}

		ops := make(map[hermod.Operation]bool)
		if config["create"] == "true" {
			ops[hermod.OpCreate] = true
		}
		if config["update"] == "true" {
			ops[hermod.OpUpdate] = true
		}
		if config["delete"] == "true" {
			ops[hermod.OpDelete] = true
		}
		if config["snapshot"] == "true" {
			ops[hermod.OpSnapshot] = true
		}

		return &FilterDataTransformer{
			Field:      config["field"],
			Operator:   config["operator"],
			Value:      config["value"],
			Operations: ops,
			regex:      re,
		}, nil
	case "mapping":
		mapping := make(map[string]string)
		strict := true
		if config["strict"] == "false" {
			strict = false
		}
		for k, v := range config {
			if strings.HasPrefix(k, "map.") {
				mapping[strings.TrimPrefix(k, "map.")] = v
			} else if k != "strict" {
				mapping[k] = v
			}
		}
		return &MappingTransformer{Mapping: mapping, Strict: strict}, nil
	case "schema":
		return &SchemaTransformer{
			Schema: config["schema"],
		}, nil
	case "lua":
		return &LuaTransformer{
			Script: config["script"],
		}, nil
	case "advanced":
		mapping := make(map[string]string)
		strict := true
		if config["strict"] == "false" {
			strict = false
		}
		for k, v := range config {
			if strings.HasPrefix(k, "column.") {
				mapping[strings.TrimPrefix(k, "column.")] = v
			} else if k != "strict" {
				mapping[k] = v
			}
		}
		return &AdvancedTransformer{Mapping: mapping, Strict: strict}, nil
	case "http":
		headers := make(map[string]string)
		for k, v := range config {
			if strings.HasPrefix(k, "header.") {
				headers[strings.TrimPrefix(k, "header.")] = v
			}
		}
		maxRetries, _ := strconv.Atoi(config["max_retries"])
		return &HttpTransformer{
			URL:        config["url"],
			Method:     config["method"],
			Headers:    headers,
			MaxRetries: maxRetries,
		}, nil
	case "sql":
		return &SqlTransformer{
			Driver: config["driver"],
			Conn:   config["conn"],
			Query:  config["query"],
		}, nil
	case "validator":
		var rules []ValidationRule
		// Rules are passed as rule.N.field, rule.N.type, etc.
		// First find all unique Ns
		indices := make(map[int]bool)
		for k := range config {
			if strings.HasPrefix(k, "rule.") {
				parts := strings.Split(k, ".")
				if len(parts) >= 2 {
					if idx, err := strconv.Atoi(parts[1]); err == nil {
						indices[idx] = true
					}
				}
			}
		}
		for i := 0; i < 100; i++ { // Max 100 rules for safety
			if indices[i] {
				idx := strconv.Itoa(i)
				rules = append(rules, ValidationRule{
					Field:    config["rule."+idx+".field"],
					Type:     config["rule."+idx+".type"],
					Config:   config["rule."+idx+".config"],
					Severity: config["rule."+idx+".severity"],
				})
			}
		}
		return &ValidatorTransformer{Rules: rules}, nil
	default:
		return nil, fmt.Errorf("unknown transformer type: %s", transType)
	}
}

func getMessageData(msg hermod.Message) map[string]interface{} {
	data := make(map[string]interface{})

	// 1. Data() map (dynamic fields)
	for k, v := range msg.Data() {
		data[k] = v
	}

	// 2. Payload() JSON (generic data)
	if len(msg.Payload()) > 0 {
		var payload map[string]interface{}
		if err := json.Unmarshal(msg.Payload(), &payload); err == nil {
			for k, v := range payload {
				if _, exists := data[k]; !exists {
					data[k] = v
				}
			}
		}
	}

	// 3. Before() JSON (CDC previous state)
	if len(msg.Before()) > 0 {
		var before map[string]interface{}
		if err := json.Unmarshal(msg.Before(), &before); err == nil {
			data["before"] = before
		}
	}

	// 4. Metadata fields (system)
	data["system.table"] = msg.Table()
	data["system.schema"] = msg.Schema()
	data["system.operation"] = string(msg.Operation())
	data["system.id"] = msg.ID()

	return data
}

func setMessageData(msg hermod.Message, data map[string]interface{}, replace bool) {
	if replace {
		msg.ClearPayloads()
	}

	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return
	}

	// Sync System Metadata fields
	// We check for system. prefixed fields first, then __ prefixed (legacy), then for plain fields
	if val, ok := getValueAtPath(data, "system.id"); ok {
		dm.SetID(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "__id"); ok {
		dm.SetID(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "id"); ok {
		dm.SetID(fmt.Sprintf("%v", val))
	} else if replace {
		dm.SetID("")
	}

	if val, ok := getValueAtPath(data, "system.operation"); ok {
		dm.SetOperation(hermod.Operation(fmt.Sprintf("%v", val)))
	} else if val, ok := getValueAtPath(data, "__operation"); ok {
		dm.SetOperation(hermod.Operation(fmt.Sprintf("%v", val)))
	} else if val, ok := getValueAtPath(data, "operation"); ok {
		dm.SetOperation(hermod.Operation(fmt.Sprintf("%v", val)))
	} else if replace {
		dm.SetOperation("")
	}

	if val, ok := getValueAtPath(data, "system.table"); ok {
		dm.SetTable(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "__table"); ok {
		dm.SetTable(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "table"); ok {
		dm.SetTable(fmt.Sprintf("%v", val))
	} else if replace {
		dm.SetTable("")
	}

	if val, ok := getValueAtPath(data, "system.schema"); ok {
		dm.SetSchema(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "__schema"); ok {
		dm.SetSchema(fmt.Sprintf("%v", val))
	} else if val, ok := getValueAtPath(data, "schema"); ok {
		dm.SetSchema(fmt.Sprintf("%v", val))
	} else if replace {
		dm.SetSchema("")
	}

	// Strip system fields before saving to payload/data map
	cleanData := make(map[string]interface{})
	for k, v := range data {
		if !strings.HasPrefix(k, "__") && !strings.HasPrefix(k, "system.") {
			// Avoid re-adding before/payload/id/operation/table/schema if they were added as data
			// Actually, if user explicitly mapped to 'id', we might want to keep it in the data too
			// or rely on MarshalJSON to add it from the metadata.
			// To avoid redundancy and respect the "New Object" principle,
			// if it's in metadata, we can skip it in data map if it matches.

			// For now, let's just avoid the internal ones.
			if k != "before" && k != "payload" {
				cleanData[k] = v
			}
		}
	}

	// Decide whether to store in Data map or Payload JSON
	useData := !replace && len(dm.Data()) > 0

	if useData {
		for k, v := range cleanData {
			dm.SetData(k, v)
		}
	} else {
		newBody, _ := json.Marshal(cleanData)
		dm.SetPayload(newBody)
	}
}
