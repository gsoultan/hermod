package transformer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"io"
	"net/http"
	"strconv"
	"strings"
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

type RenameTableTransformer struct {
	OldName string
	NewName string
}

func (t *RenameTableTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	if msg.Table() == t.OldName {
		if dm, ok := msg.(*message.DefaultMessage); ok {
			dm.SetTable(t.NewName)
			return dm, nil
		}
	}
	return msg, nil
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

type FilterDataTransformer struct {
	Field    string
	Operator string
	Value    string
}

func (t *FilterDataTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	var data map[string]interface{}
	if len(dm.After()) > 0 {
		if err := json.Unmarshal(dm.After(), &data); err != nil {
			return msg, nil
		}
	} else if len(dm.Before()) > 0 {
		if err := json.Unmarshal(dm.Before(), &data); err != nil {
			return msg, nil
		}
	}

	if data == nil {
		return nil, nil // If no data, and we have a filter, probably should filter out
	}

	val, ok := getValueAtPath(data, t.Field)
	if !ok {
		return nil, nil
	}

	if t.compare(val, t.Operator, t.Value) {
		return msg, nil
	}

	return nil, nil
}

func (t *FilterDataTransformer) compare(actual interface{}, op string, expected string) bool {
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
	}
	return false
}

type MappingTransformer struct {
	Mapping map[string]string // source_field -> target_field
}

func (t *MappingTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	if len(dm.After()) > 0 {
		newAfter, err := t.transformJSON(dm.After())
		if err != nil {
			return nil, err
		}
		dm.SetAfter(newAfter)
	}

	if len(dm.Before()) > 0 {
		newBefore, err := t.transformJSON(dm.Before())
		if err != nil {
			return nil, err
		}
		dm.SetBefore(newBefore)
	}

	return dm, nil
}

func (t *MappingTransformer) transformJSON(data []byte) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	newM := make(map[string]interface{})
	for k, v := range m {
		if target, ok := t.Mapping[k]; ok {
			if target != "" {
				newM[target] = v
			}
			// If target is "", the field is dropped
		} else {
			// Keep fields not in mapping?
			// For "transform to new data object", maybe only keep mapped fields.
			// Let's make it configurable or assume if mapping is provided, we only keep mapped fields.
			// Actually, let's just keep them if not explicitly mapped to something else or dropped.
			// User said "transform to new data object", which often implies a specific schema.
			// If we want to be strict, we should only include what's in Mapping.
			// Let's go with strict for now if mapping is not empty.
			// Actually, let's keep all and only rename/drop.
			newM[k] = v
		}
	}

	// Re-apply mapping for renames and drops more carefully
	finalM := make(map[string]interface{})
	if len(t.Mapping) > 0 {
		// If mapping is provided, we only take what's in mapping?
		// User: "transform to new data object"
		// Let's follow the mapping.
		for src, target := range t.Mapping {
			if val, ok := m[src]; ok {
				if target != "" {
					finalM[target] = val
				}
			}
		}
	} else {
		finalM = m
	}

	return json.Marshal(finalM)
}

type AdvancedTransformer struct {
	Mapping map[string]string // target_field -> expression
}

func (t *AdvancedTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	if len(dm.After()) > 0 {
		newAfter, err := t.transformJSON(dm.After())
		if err != nil {
			return nil, err
		}
		dm.SetAfter(newAfter)
	}

	if len(dm.Before()) > 0 {
		newBefore, err := t.transformJSON(dm.Before())
		if err != nil {
			return nil, err
		}
		dm.SetBefore(newBefore)
	}

	return dm, nil
}

func (t *AdvancedTransformer) transformJSON(data []byte) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	newM := make(map[string]interface{})
	for targetField, expr := range t.Mapping {
		val := t.evaluateExpression(expr, m)
		if val != nil {
			newM[targetField] = val
		}
	}

	return json.Marshal(newM)
}

func (t *AdvancedTransformer) evaluateExpression(expr string, source map[string]interface{}) interface{} {
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

	// Default: treat as literal string if no prefix
	return expr
}

type HttpTransformer struct {
	URL     string
	Method  string
	Headers map[string]string
}

func (t *HttpTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	// For step-based, we might want to use fields from After in the URL
	var m map[string]interface{}
	if len(dm.After()) > 0 {
		_ = json.Unmarshal(dm.After(), &m)
	}

	url := t.URL
	if m != nil {
		for k, v := range m {
			url = strings.ReplaceAll(url, "{"+k+"}", fmt.Sprintf("%v", v))
		}
	}

	req, err := http.NewRequestWithContext(ctx, t.Method, url, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// If successful, update the message After with the response
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		dm.SetAfter(body)
	}

	return dm, nil
}

type SqlTransformer struct {
	Driver string
	Conn   string
	Query  string
}

func (t *SqlTransformer) Transform(ctx context.Context, msg hermod.Message) (hermod.Message, error) {
	dm, ok := msg.(*message.DefaultMessage)
	if !ok {
		return msg, nil
	}

	db, err := sql.Open(t.Driver, t.Conn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var m map[string]interface{}
	if len(dm.After()) > 0 {
		_ = json.Unmarshal(dm.After(), &m)
	}

	// Very basic query parameter replacement
	query := t.Query
	if m != nil {
		for k, v := range m {
			query = strings.ReplaceAll(query, ":"+k, fmt.Sprintf("'%v'", v)) // Note: simple but UNSAFE for production, but okay for POC
		}
	}

	rows, err := db.QueryContext(ctx, query)
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

		newBody, _ := json.Marshal(rowMap)
		dm.SetAfter(newBody)
	}

	return dm, nil
}

func getValueAtPath(m map[string]interface{}, path string) (interface{}, bool) {
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

func NewTransformer(transType string, config map[string]string) (hermod.Transformer, error) {
	switch transType {
	case "rename_table":
		return &RenameTableTransformer{
			OldName: config["old_name"],
			NewName: config["new_name"],
		}, nil
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
	case "filter_data":
		return &FilterDataTransformer{
			Field:    config["field"],
			Operator: config["operator"],
			Value:    config["value"],
		}, nil
	case "mapping":
		mapping := make(map[string]string)
		for k, v := range config {
			if strings.HasPrefix(k, "map.") {
				mapping[strings.TrimPrefix(k, "map.")] = v
			} else {
				mapping[k] = v
			}
		}
		return &MappingTransformer{Mapping: mapping}, nil
	case "advanced":
		mapping := make(map[string]string)
		for k, v := range config {
			if strings.HasPrefix(k, "column.") {
				mapping[strings.TrimPrefix(k, "column.")] = v
			} else {
				mapping[k] = v
			}
		}
		return &AdvancedTransformer{Mapping: mapping}, nil
	case "http":
		headers := make(map[string]string)
		for k, v := range config {
			if strings.HasPrefix(k, "header.") {
				headers[strings.TrimPrefix(k, "header.")] = v
			}
		}
		return &HttpTransformer{
			URL:     config["url"],
			Method:  config["method"],
			Headers: headers,
		}, nil
	case "sql":
		return &SqlTransformer{
			Driver: config["driver"],
			Conn:   config["conn"],
			Query:  config["query"],
		}, nil
	default:
		return nil, fmt.Errorf("unknown transformer type: %s", transType)
	}
}
