package transformer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/evaluator"
	sourcemongodb "github.com/user/hermod/pkg/source/mongodb"
	"github.com/user/hermod/pkg/sqlutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func init() {
	Register("db_lookup", &DBLookupTransformer{})
}

type DBLookupTransformer struct{}

type RegistryProvider interface {
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
	GetLookupCache() (map[string]any, *sync.RWMutex) // This might need a better way
}

// NOTE: Since we need Registry for storage and DB pool, we'll assume it's passed in context
// or we need a cleaner way to provide these services.
// For now, let's look at how we can access Registry from context as previously hinted in registry.go:893

func (t *DBLookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value("registry").(interface {
		GetSource(ctx context.Context, id string) (storage.Source, error)
		GetOrOpenDB(src storage.Source) (*sql.DB, error)
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})

	if !ok {
		return msg, fmt.Errorf("registry not found in context")
	}

	sourceID := getConfigString(config, "sourceId")
	table := getConfigString(config, "table")
	keyColumn := getConfigString(config, "keyColumn")
	valueColumn := getConfigString(config, "valueColumn")
	keyField := getConfigString(config, "keyField")
	targetField := getConfigString(config, "targetField")
	ttlStr := getConfigString(config, "ttl")
	whereClause := getConfigString(config, "whereClause")
	defaultValue := getConfigString(config, "defaultValue")
	queryTemplate := getConfigString(config, "queryTemplate")
	flattenInto := getConfigString(config, "flattenInto")

	if sourceID == "" || targetField == "" {
		return msg, nil
	}

	keyVal := evaluator.GetMsgValByPath(msg, keyField)
	if keyVal == nil && queryTemplate == "" && whereClause == "" {
		return msg, nil
	}

	cacheKey := fmt.Sprintf("db:%s:%s:%s:%s:%v:%s:%s", sourceID, table, keyColumn, valueColumn, keyVal, whereClause, queryTemplate)
	if cached, found := registry.GetLookupCache(cacheKey); found {
		msg.SetData(targetField, cached)
		return msg, nil
	}

	src, err := registry.GetSource(ctx, sourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get source for lookup: %w", err)
	}

	// Enforce: db_lookup should use non-CDC sources, except for SQL Server (mssql)
	if v, ok := src.Config["use_cdc"]; ok {
		if v != "false" && src.Type != "mssql" {
			return msg, fmt.Errorf("db_lookup requires a non-CDC source; disable CDC on source '%s' or use a non-CDC source (allowed exception: SQL Server)", src.Name)
		}
	}

	var resultVal any
	if src.Type == "mongodb" {
		// queryTemplate not supported for Mongo; use whereClause
		resultVal, err = t.lookupMongoDB(ctx, src, table, keyColumn, keyVal, whereClause, valueColumn, defaultValue, msg.Data())
	} else {
		if queryTemplate != "" {
			resultVal, err = t.lookupSQLWithTemplate(ctx, registry, src, queryTemplate, valueColumn, msg.Data())
		} else {
			resultVal, err = t.lookupSQL(ctx, registry, src, table, keyColumn, keyVal, whereClause, valueColumn, defaultValue, msg.Data())
		}
	}

	if err != nil {
		return msg, err
	}

	if resultVal != nil {
		var ttl time.Duration
		if ttlStr != "" {
			ttl, _ = time.ParseDuration(ttlStr)
		}
		registry.SetLookupCache(cacheKey, resultVal, ttl)
		msg.SetData(targetField, resultVal)
		// Optional flattening of results into top-level or prefixed fields
		if flattenInto != "" {
			if m, ok := resultVal.(map[string]any); ok {
				for k, v := range m {
					if flattenInto == "." {
						msg.SetData(k, v)
					} else {
						msg.SetData(strings.TrimSuffix(flattenInto, ".")+"."+k, v)
					}
				}
			}
		}
	} else if defaultValue != "" {
		msg.SetData(targetField, defaultValue)
	}

	return msg, nil
}

func (t *DBLookupTransformer) lookupMongoDB(ctx context.Context, src storage.Source, table, keyColumn string, keyVal any, whereClause, valueColumn, defaultValue string, data map[string]any) (any, error) {
	uri := src.Config["uri"]
	if uri == "" {
		host := src.Config["host"]
		port := src.Config["port"]
		user := src.Config["user"]
		password := src.Config["password"]
		if user != "" && password != "" {
			uri = fmt.Sprintf("mongodb://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%s", host, port)
		}
	}

	client, err := sourcemongodb.GetClient(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb for lookup: %w", err)
	}

	dbName := src.Config["database"]
	collName := table
	if collName == "" {
		collName = src.Config["collection"]
	}

	coll := client.Database(dbName).Collection(collName)
	filter := bson.M{keyColumn: keyVal}
	if whereClause != "" {
		err = json.Unmarshal([]byte(evaluator.ResolveTemplate(whereClause, data)), &filter)
		if err != nil {
			return nil, fmt.Errorf("failed to parse mongo whereClause: %w", err)
		}
	}

	var result map[string]any
	err = coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("mongo lookup failed: %w", err)
	}

	var finalResult any = result
	if valueColumn != "" && valueColumn != "*" {
		finalResult = result[valueColumn]
	}
	return finalResult, nil
}

func (t *DBLookupTransformer) lookupSQL(ctx context.Context, registry interface {
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}, src storage.Source, table, keyColumn string, keyVal any, whereClause, valueColumn, defaultValue string, data map[string]any) (any, error) {
	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for lookup: %w", err)
	}
	// Map to driver for quoting/placeholder
	driver := src.Type
	switch src.Type {
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	case "sqlite":
		driver = "sqlite"
	case "mssql":
		driver = "mssql"
	}

	// Quote table and columns safely
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}

	selectList := "*"
	if valueColumn != "" && valueColumn != "*" {
		cols := strings.Split(valueColumn, ",")
		qcols := make([]string, 0, len(cols))
		for _, c := range cols {
			c = strings.TrimSpace(c)
			qc, qerr := sqlutil.QuoteIdent(driver, c)
			if qerr != nil {
				return nil, fmt.Errorf("invalid column in valueColumn: %w", qerr)
			}
			qcols = append(qcols, qc)
		}
		selectList = strings.Join(qcols, ", ")
	}

	var whereParts []string
	var args []any
	nextIdx := 1
	batchMode := false

	if whereClause != "" {
		// Safe subset parser: support AND-separated expressions of the form: column = {{template}} or column = 'literal'
		raw := whereClause
		// Normalize AND
		parts := strings.Split(raw, "AND")
		if len(parts) == 1 {
			parts = strings.Split(raw, "and")
		}
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("unsupported whereClause fragment: %q", p)
			}
			col := strings.TrimSpace(kv[0])
			rhs := strings.TrimSpace(kv[1])
			qcol, qerr := sqlutil.QuoteIdent(driver, col)
			if qerr != nil {
				return nil, fmt.Errorf("invalid column in whereClause: %w", qerr)
			}
			var val any
			if strings.Contains(rhs, "{{") {
				// Evaluate template into a value string and trim any surrounding quotes
				s := evaluator.ResolveTemplate(rhs, data)
				if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
					s = strings.Trim(s, "'")
				} else if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
					s = strings.Trim(s, "\"")
				}
				val = s
			} else if strings.HasPrefix(rhs, "'") && strings.HasSuffix(rhs, "'") {
				val = strings.Trim(rhs, "'")
			} else if strings.HasPrefix(rhs, "\"") && strings.HasSuffix(rhs, "\"") {
				val = strings.Trim(rhs, "\"")
			} else {
				// Treat as raw token (number/bool)
				val = rhs
			}
			ph := sqlutil.Placeholder(driver, nextIdx)
			nextIdx++
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qcol, ph))
			args = append(args, val)
		}
		if len(whereParts) == 0 {
			return nil, fmt.Errorf("invalid whereClause: no conditions parsed")
		}
	} else if keyColumn != "" {
		qkey, qerr := sqlutil.QuoteIdent(driver, keyColumn)
		if qerr != nil {
			return nil, fmt.Errorf("invalid keyColumn: %w", qerr)
		}
		// Support batch lookup for slice/array keyVal -> IN (...)
		if arr, ok := asSlice(keyVal); ok {
			if len(arr) == 0 {
				return nil, nil
			}
			var phs []string
			for range arr {
				phs = append(phs, sqlutil.Placeholder(driver, nextIdx))
				nextIdx++
			}
			whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", qkey, strings.Join(phs, ", ")))
			args = append(args, arr...)
			batchMode = true
		} else {
			ph := sqlutil.Placeholder(driver, nextIdx)
			nextIdx++
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qkey, ph))
			args = append(args, keyVal)
		}
	} else {
		return nil, fmt.Errorf("either whereClause or keyColumn must be provided for db_lookup")
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectList, quotedTable, strings.Join(whereParts, " AND "))
	if !batchMode {
		query += " LIMIT 1"
	}

	var resultVal any
	if valueColumn == "*" || strings.Contains(valueColumn, ",") {
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to execute lookup query: %w", err)
		}
		defer rows.Close()

		// If batch (IN) was used, return all rows as []map
		isBatch := strings.Contains(strings.ToUpper(query), " IN (")
		if isBatch {
			var rowsOut []map[string]any
			for rows.Next() {
				cols, _ := rows.Columns()
				values := make([]any, len(cols))
				valuePtrs := make([]any, len(cols))
				for i := range values {
					valuePtrs[i] = &values[i]
				}
				if err := rows.Scan(valuePtrs...); err != nil {
					return nil, fmt.Errorf("failed to scan lookup results: %w", err)
				}
				rowMap := make(map[string]any)
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						rowMap[col] = string(b)
					} else {
						rowMap[col] = val
					}
				}
				rowsOut = append(rowsOut, rowMap)
			}
			if len(rowsOut) == 0 {
				return nil, nil
			}
			resultVal = rowsOut
		} else {
			if rows.Next() {
				cols, _ := rows.Columns()
				values := make([]any, len(cols))
				valuePtrs := make([]any, len(cols))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				if err := rows.Scan(valuePtrs...); err != nil {
					return nil, fmt.Errorf("failed to scan lookup results: %w", err)
				}

				rowMap := make(map[string]any)
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						rowMap[col] = string(b)
					} else {
						rowMap[col] = val
					}
				}
				resultVal = rowMap
			} else {
				return nil, nil
			}
		}
	} else {
		// If batch (IN) used with single column selection, return []any
		isBatch := strings.Contains(strings.ToUpper(query), " IN (")
		if isBatch {
			rows, err := db.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("failed to execute lookup query: %w", err)
			}
			defer rows.Close()
			var list []any
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					return nil, fmt.Errorf("failed to scan lookup results: %w", err)
				}
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				list = append(list, v)
			}
			if len(list) == 0 {
				return nil, nil
			}
			resultVal = list
		} else {
			err = db.QueryRowContext(ctx, query, args...).Scan(&resultVal)
			if err != nil {
				if err == sql.ErrNoRows {
					return nil, nil
				}
				return nil, fmt.Errorf("failed to execute lookup query: %w", err)
			}
			if b, ok := resultVal.([]byte); ok {
				resultVal = string(b)
			}
		}
	}
	return resultVal, nil
}

// lookupSQLWithTemplate executes a full custom SELECT template while safely parameterizing any {{ ... }} tokens.
func (t *DBLookupTransformer) lookupSQLWithTemplate(ctx context.Context, registry interface {
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}, src storage.Source, queryTemplate string, valueColumn string, data map[string]any) (any, error) {
	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for lookup: %w", err)
	}

	// Map to driver for placeholder style
	driver := src.Type
	switch src.Type {
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	case "sqlite":
		driver = "sqlite"
	case "mssql":
		driver = "mssql"
	}

	sqlText, args := parameterizeTemplate(driver, queryTemplate, data)
	if strings.TrimSpace(sqlText) == "" {
		return nil, fmt.Errorf("empty queryTemplate after processing")
	}

	// Decide scanning mode by valueColumn
	var resultVal any
	if valueColumn == "*" || valueColumn == "" || strings.Contains(valueColumn, ",") {
		rows, err := db.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to execute lookup query: %w", err)
		}
		defer rows.Close()
		var rowsOut []map[string]any
		for rows.Next() {
			cols, _ := rows.Columns()
			values := make([]any, len(cols))
			valuePtrs := make([]any, len(cols))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			if err := rows.Scan(valuePtrs...); err != nil {
				return nil, fmt.Errorf("failed to scan lookup results: %w", err)
			}
			rowMap := make(map[string]any)
			for i, col := range cols {
				val := values[i]
				if b, ok := val.([]byte); ok {
					rowMap[col] = string(b)
				} else {
					rowMap[col] = val
				}
			}
			rowsOut = append(rowsOut, rowMap)
		}
		if len(rowsOut) == 0 {
			return nil, nil
		}
		// If only one row, return object; else return array of objects
		if len(rowsOut) == 1 {
			resultVal = rowsOut[0]
		} else {
			resultVal = rowsOut
		}
	} else {
		// Single value expected
		var v any
		err = db.QueryRowContext(ctx, sqlText, args...).Scan(&v)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, fmt.Errorf("failed to execute lookup query: %w", err)
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		resultVal = v
	}
	return resultVal, nil
}

// parameterizeTemplate replaces all {{ ... }} tokens in the SQL template with driver-specific placeholders
// and returns the parameterized SQL text and a corresponding args slice.
// Token content should be either a path like `source.foo` or a quoted literal. Paths are resolved against `data`.
func parameterizeTemplate(driver, tpl string, data map[string]any) (string, []any) {
	var out strings.Builder
	var args []any
	i := 0
	nextIdx := 1
	for i < len(tpl) {
		if i+1 < len(tpl) && tpl[i] == '{' && tpl[i+1] == '{' {
			// find closing }}
			j := i + 2
			for j+1 < len(tpl) {
				if tpl[j] == '}' && tpl[j+1] == '}' {
					break
				}
				j++
			}
			if j+1 >= len(tpl) {
				// no closing, write rest and break
				out.WriteString(tpl[i:])
				break
			}
			token := strings.TrimSpace(tpl[i+2 : j])
			// Resolve token value
			var val any
			switch {
			case strings.HasPrefix(token, "'") && strings.HasSuffix(token, "'"):
				val = strings.Trim(token, "'")
			case strings.HasPrefix(token, "\"") && strings.HasSuffix(token, "\""):
				val = strings.Trim(token, "\"")
			default:
				// allow optional source. prefix
				if strings.HasPrefix(token, "source.") {
					token = strings.TrimPrefix(token, "source.")
				}
				// Use evaluator to get message value by path semantics
				// We only have a map here, so mimic evaluator.GetMsgValByPath on the map
				val = getFromMapPath(data, token)
			}
			out.WriteString(sqlutil.Placeholder(driver, nextIdx))
			nextIdx++
			args = append(args, val)
			i = j + 2
		} else {
			out.WriteByte(tpl[i])
			i++
		}
	}
	return out.String(), args
}

// getFromMapPath resolves a dotted path in a nested map[string]any.
func getFromMapPath(m map[string]any, path string) any {
	if m == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		if mm, ok := cur.(map[string]any); ok {
			cur = mm[p]
		} else {
			return nil
		}
	}
	return cur
}

// asSlice tries to coerce v into a slice of any for batch IN processing.
func asSlice(v any) ([]any, bool) {
	switch arr := v.(type) {
	case []any:
		return arr, true
	case []string:
		out := make([]any, len(arr))
		for i, s := range arr {
			out[i] = s
		}
		return out, true
	case []int:
		out := make([]any, len(arr))
		for i, s := range arr {
			out[i] = s
		}
		return out, true
	case []int64:
		out := make([]any, len(arr))
		for i, s := range arr {
			out[i] = s
		}
		return out, true
	case []float64:
		out := make([]any, len(arr))
		for i, s := range arr {
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}
