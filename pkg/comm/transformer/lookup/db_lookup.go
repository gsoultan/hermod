package lookup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	sourcemongodb "github.com/user/hermod/pkg/comm/source/mongodb"
	"github.com/user/hermod/pkg/comm/transformer/core"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/sqlutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func init() {
	transformer.Register("db_lookup", &DBLookupTransformer{})
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

	registry, ok := ctx.Value(hermod.RegistryKey).(interface {
		GetSource(ctx context.Context, id string) (storage.Source, error)
		GetOrOpenDB(src storage.Source) (*sql.DB, error)
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})

	if !ok {
		return msg, errors.New("registry not found in context")
	}

	sourceID := core.GetConfigString(config, "sourceId")
	table := core.GetConfigString(config, "table")
	keyColumn := core.GetConfigString(config, "keyColumn")
	valueColumn := core.GetConfigString(config, "valueColumn")
	keyField := core.GetConfigString(config, "keyField")
	targetField := core.GetConfigString(config, "targetField")
	ttlStr := core.GetConfigString(config, "ttl")
	whereClause := core.GetConfigString(config, "whereClause")
	defaultValue := core.GetConfigString(config, "defaultValue")
	queryTemplate := core.GetConfigString(config, "queryTemplate")
	flattenInto := core.GetConfigString(config, "flattenInto")
	mode := core.GetConfigString(config, "mode")

	if sourceID == "" || targetField == "" {
		return msg, nil
	}

	keyVal := evaluator.GetMsgValByPath(msg, keyField)
	if keyVal == nil && queryTemplate == "" && whereClause == "" {
		return msg, nil
	}

	cacheKey := fmt.Sprintf("db:%s:%s:%s:%s:%v:%s:%s:%s", sourceID, table, keyColumn, valueColumn, keyVal, whereClause, queryTemplate, mode)
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
		// If mode is explicit, follow it. Otherwise fallback to queryTemplate presence.
		useTemplate := false
		if mode == "query" {
			useTemplate = true
		} else if mode == "table" {
			useTemplate = false
		} else if queryTemplate != "" {
			useTemplate = true
		}

		if useTemplate {
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
		if errors.Is(err, mongo.ErrNoDocuments) {
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
			if strings.HasPrefix(rhs, "{{") && strings.HasSuffix(rhs, "}}") && strings.Count(rhs, "{{") == 1 {
				// Single token template: preserve original type and handle nil correctly for SQL
				token := strings.TrimSpace(rhs[2 : len(rhs)-2])
				token = strings.TrimPrefix(token, ".")
				val = evaluator.GetValByPath(data, token)
			} else if strings.Contains(rhs, "{{") {
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
				// Treat as raw token (number/bool/null)
				if strings.EqualFold(rhs, "NULL") {
					val = nil
				} else if i, err := strconv.ParseInt(rhs, 10, 64); err == nil {
					val = i
				} else if f, err := strconv.ParseFloat(rhs, 64); err == nil {
					val = f
				} else if b, err := strconv.ParseBool(rhs); err == nil {
					val = b
				} else {
					val = rhs
				}
			}
			ph := sqlutil.Placeholder(driver, nextIdx)
			nextIdx++
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qcol, ph))
			args = append(args, val)
		}
		if len(whereParts) == 0 {
			return nil, errors.New("invalid whereClause: no conditions parsed")
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
			whereParts = append(whereParts, fmt.Sprintf("%s = %s", qkey, ph))
			args = append(args, keyVal)
		}
	} else {
		return nil, errors.New("either whereClause or keyColumn must be provided for db_lookup")
	}

	query := buildLookupQuery(driver, selectList, quotedTable, whereParts, batchMode)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lookup query: %w", err)
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var rowsOut []map[string]any
	for rows.Next() {
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

	isBatch := strings.Contains(strings.ToUpper(query), " IN (")

	// Single column requested.
	if valueColumn != "" && valueColumn != "*" && !strings.Contains(valueColumn, ",") {
		findValue := func(row map[string]any) any {
			if val, ok := row[valueColumn]; ok {
				return val
			}
			for k, v := range row {
				if strings.EqualFold(k, valueColumn) {
					return v
				}
			}
			return nil
		}

		if !isBatch && len(rowsOut) == 1 {
			return findValue(rowsOut[0]), nil
		}
		var results []any
		for _, row := range rowsOut {
			if val := findValue(row); val != nil {
				results = append(results, val)
			}
		}
		if len(results) == 0 {
			return nil, nil
		}
		return results, nil
	}

	if !isBatch && len(rowsOut) == 1 {
		return rowsOut[0], nil
	}
	return rowsOut, nil
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

	sqlText, args := core.ParameterizeTemplate(driver, queryTemplate, data)
	if strings.TrimSpace(sqlText) == "" {
		return nil, errors.New("empty queryTemplate after processing")
	}

	// Execute query and fetch results.
	// We always use rows.Scan with dynamic columns because queryTemplate is user-provided.
	rows, err := db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lookup query: %w", err)
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var rowsOut []map[string]any
	for rows.Next() {
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

	// Determine result based on valueColumn setting
	if valueColumn == "*" || valueColumn == "" {
		if len(rowsOut) == 1 {
			return rowsOut[0], nil
		}
		return rowsOut, nil
	}

	// Multiple columns requested via comma-separated list
	if strings.Contains(valueColumn, ",") {
		requestedCols := strings.Split(valueColumn, ",")
		for i := range requestedCols {
			requestedCols[i] = strings.TrimSpace(requestedCols[i])
		}

		filterRow := func(row map[string]any) map[string]any {
			filtered := make(map[string]any)
			for _, rc := range requestedCols {
				if val, ok := row[rc]; ok {
					filtered[rc] = val
				} else {
					// try case-insensitive
					for k, v := range row {
						if strings.EqualFold(k, rc) {
							filtered[rc] = v
							break
						}
					}
				}
			}
			return filtered
		}

		if len(rowsOut) == 1 {
			return filterRow(rowsOut[0]), nil
		}
		var filteredRows []map[string]any
		for _, row := range rowsOut {
			filteredRows = append(filteredRows, filterRow(row))
		}
		return filteredRows, nil
	}

	// Single column requested.
	// We look for it in the scanned row(s). Case-insensitive match for convenience.
	findValue := func(row map[string]any) any {
		if val, ok := row[valueColumn]; ok {
			return val
		}
		for k, v := range row {
			if strings.EqualFold(k, valueColumn) {
				return v
			}
		}
		return nil
	}

	if len(rowsOut) == 1 {
		return findValue(rowsOut[0]), nil
	}

	var results []any
	for _, row := range rowsOut {
		results = append(results, findValue(row))
	}
	return results, nil
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

func buildLookupQuery(driver, selectList, quotedTable string, whereParts []string, batchMode bool) string {
	whereJoined := strings.Join(whereParts, " AND ")
	if !batchMode {
		switch driver {
		case "mssql", "sqlserver":
			return fmt.Sprintf("SELECT TOP 1 %s FROM %s WHERE %s", selectList, quotedTable, whereJoined)
		case "oracle":
			return fmt.Sprintf("SELECT %s FROM %s WHERE %s FETCH FIRST 1 ROWS ONLY", selectList, quotedTable, whereJoined)
		default:
			return fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1", selectList, quotedTable, whereJoined)
		}
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectList, quotedTable, whereJoined)
}
