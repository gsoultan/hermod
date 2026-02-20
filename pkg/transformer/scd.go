package transformer

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

func init() {
	Register("scd", &SCDTransformer{})
}

type SCDTransformer struct{}

type SCDRegistry interface {
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}

func (t *SCDTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	scdType := 1 // Default to Type 1
	if v, ok := config["scdType"]; ok {
		if i, ok := evaluator.ToInt64(v); ok {
			scdType = int(i)
		}
	} else if v, ok := config["type"]; ok { // Support "type" key from UI
		if i, ok := evaluator.ToInt64(v); ok {
			scdType = int(i)
		}
	}

	registry, ok := ctx.Value("registry").(SCDRegistry)
	if !ok {
		return msg, fmt.Errorf("registry not found in context")
	}

	targetSourceID := getConfigString(config, "targetSourceId")
	targetTable := getConfigString(config, "targetTable")

	// Support both comma-separated string and []any for keys/columns
	businessKeys := getConfigStringSlice(config, "businessKeys")
	if len(businessKeys) == 0 {
		businessKeys = splitComma(getConfigString(config, "keys"))
	}

	compareFields := getConfigStringSlice(config, "compareFields")
	if len(compareFields) == 0 {
		compareFields = splitComma(getConfigString(config, "columns"))
	}

	if targetSourceID == "" || targetTable == "" || len(businessKeys) == 0 {
		return msg, nil
	}

	src, err := registry.GetSource(ctx, targetSourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get source for SCD: %w", err)
	}

	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return msg, fmt.Errorf("failed to get database for SCD: %w", err)
	}

	driver := src.Type
	switch driver {
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	case "sqlite":
		driver = "sqlite"
	case "mssql":
		driver = "mssql"
	}

	switch scdType {
	case 0:
		return t.handleType0(ctx, db, driver, targetTable, businessKeys, msg)
	case 1:
		return t.handleType1(ctx, db, driver, targetTable, businessKeys, compareFields, msg)
	case 2:
		return t.handleType2(ctx, db, driver, targetTable, businessKeys, compareFields, config, msg)
	case 3:
		return t.handleType3(ctx, db, driver, targetTable, businessKeys, config, msg)
	case 4:
		return t.handleType4(ctx, db, driver, targetTable, businessKeys, compareFields, config, msg)
	case 6:
		return t.handleType6(ctx, db, driver, targetTable, businessKeys, config, msg)
	default:
		return msg, fmt.Errorf("unsupported SCD type: %d", scdType)
	}
}

func (t *SCDTransformer) handleType0(ctx context.Context, db *sql.DB, driver, table string, businessKeys []string, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	// 1. Check if record exists
	whereParts := make([]string, 0, len(businessKeys))
	args := make([]any, 0, len(businessKeys))
	for i, key := range businessKeys {
		quotedKey, err := sqlutil.QuoteIdent(driver, key)
		if err != nil {
			return msg, err
		}
		val := evaluator.GetMsgValByPath(msg, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", quotedKey, sqlutil.Placeholder(driver, i+1)))
		args = append(args, val)
	}

	query := fmt.Sprintf("SELECT 1 FROM %s WHERE %s", quotedTable, strings.Join(whereParts, " AND "))
	var dummy int
	err = db.QueryRowContext(ctx, query, args...).Scan(&dummy)
	if err == sql.ErrNoRows {
		// Not found, INSERT
		return t.performInsert(ctx, db, driver, table, msg)
	} else if err != nil {
		return msg, fmt.Errorf("lookup failed: %w", err)
	}

	// Found, do nothing for Type 0
	msg.SetMetadata("scd_action", "none")
	return msg, nil
}

func (t *SCDTransformer) handleType3(ctx context.Context, db *sql.DB, driver, table string, businessKeys []string, config map[string]any, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	// columnMappings: current_col -> previous_col
	mappings := make(map[string]string)
	if v, ok := config["columnMappings"].(map[string]any); ok {
		for k, val := range v {
			if s, ok := val.(string); ok {
				mappings[k] = s
			}
		}
	} else if v, ok := config["mappings"].(string); ok && v != "" {
		// Try parsing comma separated: current:prev,current2:prev2
		parts := strings.Split(v, ",")
		for _, p := range parts {
			kv := strings.Split(p, ":")
			if len(kv) == 2 {
				mappings[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	if len(mappings) == 0 {
		return t.handleType1(ctx, db, driver, table, businessKeys, nil, msg)
	}

	// 1. Lookup existing record
	whereParts := make([]string, 0, len(businessKeys))
	args := make([]any, 0, len(businessKeys))
	for i, key := range businessKeys {
		quotedKey, _ := sqlutil.QuoteIdent(driver, key)
		val := evaluator.GetMsgValByPath(msg, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", quotedKey, sqlutil.Placeholder(driver, i+1)))
		args = append(args, val)
	}

	selectCols := make([]string, 0)
	for curr := range mappings {
		q, _ := sqlutil.QuoteIdent(driver, curr)
		selectCols = append(selectCols, q)
	}
	if len(selectCols) == 0 {
		selectCols = append(selectCols, "*")
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(selectCols, ", "), quotedTable, strings.Join(whereParts, " AND "))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return msg, err
	}
	defer rows.Close()

	if rows.Next() {
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return msg, err
		}

		existingData := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				existingData[col] = string(b)
			} else {
				existingData[col] = val
			}
		}

		changed := false
		updateParts := make([]string, 0)
		updateArgs := make([]any, 0)
		idx := 1

		for curr, prev := range mappings {
			newVal := evaluator.GetMsgValByPath(msg, curr)
			oldVal := existingData[curr]

			if !reflect.DeepEqual(newVal, oldVal) {
				changed = true
				// Move current to previous
				qPrev, _ := sqlutil.QuoteIdent(driver, prev)
				updateParts = append(updateParts, fmt.Sprintf("%s = %s", qPrev, sqlutil.Placeholder(driver, idx)))
				updateArgs = append(updateArgs, oldVal)
				idx++

				// Update current
				qCurr, _ := sqlutil.QuoteIdent(driver, curr)
				updateParts = append(updateParts, fmt.Sprintf("%s = %s", qCurr, sqlutil.Placeholder(driver, idx)))
				updateArgs = append(updateArgs, newVal)
				idx++
			}
		}

		if changed {
			// Add any other fields from msg that are not in mappings or business keys
			for field, val := range msg.Data() {
				isBK := false
				for _, bk := range businessKeys {
					if bk == field {
						isBK = true
						break
					}
				}
				if isBK {
					continue
				}
				if _, ok := mappings[field]; ok {
					continue
				}
				// Also check if it's a "previous" column
				isPrev := false
				for _, p := range mappings {
					if p == field {
						isPrev = true
						break
					}
				}
				if isPrev {
					continue
				}

				q, err := sqlutil.QuoteIdent(driver, field)
				if err != nil {
					continue
				}
				updateParts = append(updateParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
				updateArgs = append(updateArgs, val)
				idx++
			}

			updateWhere := make([]string, 0)
			for _, bk := range businessKeys {
				q, _ := sqlutil.QuoteIdent(driver, bk)
				updateWhere = append(updateWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
				updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, bk))
				idx++
			}

			updateQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(updateParts, ", "), strings.Join(updateWhere, " AND "))
			_, err = db.ExecContext(ctx, updateQuery, updateArgs...)
			if err != nil {
				return msg, err
			}
			msg.SetMetadata("scd_action", "update")
		} else {
			msg.SetMetadata("scd_action", "none")
		}
	} else {
		return t.performInsert(ctx, db, driver, table, msg)
	}

	return msg, nil
}

func (t *SCDTransformer) handleType4(ctx context.Context, db *sql.DB, driver, table string, businessKeys, compareFields []string, config map[string]any, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	historyTable := getConfigString(config, "historyTable")
	if historyTable == "" {
		historyTable = table + "_history"
	}
	quotedHistoryTable, err := sqlutil.QuoteIdent(driver, historyTable)
	if err != nil {
		return msg, err
	}

	// 1. Lookup existing record
	whereParts := make([]string, 0, len(businessKeys))
	args := make([]any, 0, len(businessKeys))
	for i, key := range businessKeys {
		quotedKey, _ := sqlutil.QuoteIdent(driver, key)
		val := evaluator.GetMsgValByPath(msg, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", quotedKey, sqlutil.Placeholder(driver, i+1)))
		args = append(args, val)
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable, strings.Join(whereParts, " AND "))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return msg, err
	}
	defer rows.Close()

	if rows.Next() {
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return msg, err
		}

		existingData := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				existingData[col] = string(b)
			} else {
				existingData[col] = val
			}
		}

		changed := false
		if len(compareFields) == 0 {
			changed = true
		} else {
			for _, field := range compareFields {
				newVal := evaluator.GetMsgValByPath(msg, field)
				oldVal := existingData[field]
				if !reflect.DeepEqual(newVal, oldVal) {
					changed = true
					break
				}
			}
		}

		if changed {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return msg, err
			}
			defer tx.Rollback()

			// Insert old record into history table
			hCols := make([]string, 0, len(cols))
			hPhs := make([]string, 0, len(cols))
			hArgs := make([]any, 0, len(cols))
			for i, col := range cols {
				q, _ := sqlutil.QuoteIdent(driver, col)
				hCols = append(hCols, q)
				hPhs = append(hPhs, sqlutil.Placeholder(driver, i+1))
				hArgs = append(hArgs, values[i])
			}

			hQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedHistoryTable, strings.Join(hCols, ", "), strings.Join(hPhs, ", "))
			_, err = tx.ExecContext(ctx, hQuery, hArgs...)
			if err != nil {
				return msg, fmt.Errorf("failed to insert into history table: %w", err)
			}

			// Update current record (Type 1)
			updateParts := make([]string, 0)
			updateArgs := make([]any, 0)
			uIdx := 1
			for field, val := range msg.Data() {
				isBK := false
				for _, bk := range businessKeys {
					if bk == field {
						isBK = true
						break
					}
				}
				if isBK {
					continue
				}
				q, _ := sqlutil.QuoteIdent(driver, field)
				updateParts = append(updateParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, uIdx)))
				updateArgs = append(updateArgs, val)
				uIdx++
			}

			if len(updateParts) > 0 {
				uWhere := make([]string, 0)
				for _, bk := range businessKeys {
					q, _ := sqlutil.QuoteIdent(driver, bk)
					uWhere = append(uWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, uIdx)))
					updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, bk))
					uIdx++
				}
				uQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(updateParts, ", "), strings.Join(uWhere, " AND "))
				_, err = tx.ExecContext(ctx, uQuery, updateArgs...)
				if err != nil {
					return msg, fmt.Errorf("failed to update current record: %w", err)
				}
			}

			if err := tx.Commit(); err != nil {
				return msg, err
			}
			msg.SetMetadata("scd_action", "history_update")
		} else {
			msg.SetMetadata("scd_action", "none")
		}
	} else {
		return t.performInsert(ctx, db, driver, table, msg)
	}

	return msg, nil
}

func (t *SCDTransformer) handleType6(ctx context.Context, db *sql.DB, driver, table string, businessKeys []string, config map[string]any, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	type1Cols := getConfigStringSlice(config, "type1Columns")
	type2Cols := getConfigStringSlice(config, "type2Columns")

	if len(type1Cols) == 0 && len(type2Cols) == 0 {
		return t.handleType2(ctx, db, driver, table, businessKeys, nil, config, msg)
	}

	// Kimball Type 6:
	// If Type 2 change detected -> Add new row, expire old (Type 2 behavior)
	// If Type 1 change detected -> Overwrite in ALL rows for this business key (Type 1 behavior)

	// 1. Find current record
	startDateCol := getConfigString(config, "startDateColumn")
	if startDateCol == "" {
		startDateCol = "start_date"
	}
	endDateCol := getConfigString(config, "endDateColumn")
	if endDateCol == "" {
		endDateCol = "end_date"
	}
	currentFlagCol := getConfigString(config, "currentFlagColumn")

	qEndDate, _ := sqlutil.QuoteIdent(driver, endDateCol)

	whereParts := make([]string, 0)
	args := make([]any, 0)
	idx := 1
	for _, key := range businessKeys {
		q, _ := sqlutil.QuoteIdent(driver, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
		args = append(args, evaluator.GetMsgValByPath(msg, key))
		idx++
	}

	if currentFlagCol != "" {
		qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", qFlag, sqlutil.Placeholder(driver, idx)))
		args = append(args, true)
	} else {
		whereParts = append(whereParts, fmt.Sprintf("%s IS NULL", qEndDate))
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable, strings.Join(whereParts, " AND "))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return msg, err
	}
	defer rows.Close()

	if rows.Next() {
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		rows.Scan(valuePtrs...)

		existingData := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				existingData[col] = string(b)
			} else {
				existingData[col] = val
			}
		}

		type2Changed := false
		for _, col := range type2Cols {
			if !reflect.DeepEqual(evaluator.GetMsgValByPath(msg, col), existingData[col]) {
				type2Changed = true
				break
			}
		}

		type1Changed := false
		for _, col := range type1Cols {
			if !reflect.DeepEqual(evaluator.GetMsgValByPath(msg, col), existingData[col]) {
				type1Changed = true
				break
			}
		}

		if type2Changed || type1Changed {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return msg, err
			}
			defer tx.Rollback()

			if type1Changed {
				// Update ALL rows for this business key
				updateParts := make([]string, 0)
				updateArgs := make([]any, 0)
				uIdx := 1
				for _, col := range type1Cols {
					q, _ := sqlutil.QuoteIdent(driver, col)
					updateParts = append(updateParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, uIdx)))
					updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, col))
					uIdx++
				}

				uWhere := make([]string, 0)
				for _, bk := range businessKeys {
					q, _ := sqlutil.QuoteIdent(driver, bk)
					uWhere = append(uWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, uIdx)))
					updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, bk))
					uIdx++
				}

				uQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(updateParts, ", "), strings.Join(uWhere, " AND "))
				_, err = tx.ExecContext(ctx, uQuery, updateArgs...)
				if err != nil {
					return msg, err
				}
			}

			if type2Changed {
				// Standard Type 2 logic
				now := time.Now()
				qStartDate, _ := sqlutil.QuoteIdent(driver, startDateCol)

				// Expire current
				expWhere := make([]string, 0)
				expArgs := make([]any, 0)
				eIdx := 1
				expArgs = append(expArgs, now)
				setClause := fmt.Sprintf("%s = %s", qEndDate, sqlutil.Placeholder(driver, eIdx))
				eIdx++
				if currentFlagCol != "" {
					qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
					expArgs = append(expArgs, false)
					setClause += fmt.Sprintf(", %s = %s", qFlag, sqlutil.Placeholder(driver, eIdx))
					eIdx++
				}

				for _, bk := range businessKeys {
					q, _ := sqlutil.QuoteIdent(driver, bk)
					expWhere = append(expWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, eIdx)))
					expArgs = append(expArgs, evaluator.GetMsgValByPath(msg, bk))
					eIdx++
				}
				if currentFlagCol != "" {
					qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
					expWhere = append(expWhere, fmt.Sprintf("%s = %s", qFlag, sqlutil.Placeholder(driver, eIdx)))
					expArgs = append(expArgs, true)
				} else {
					expWhere = append(expWhere, fmt.Sprintf("%s IS NULL", qEndDate))
				}

				expQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, setClause, strings.Join(expWhere, " AND "))
				_, err = tx.ExecContext(ctx, expQuery, expArgs...)
				if err != nil {
					return msg, err
				}

				// Insert new
				iCols := make([]string, 0)
				iPhs := make([]string, 0)
				iArgs := make([]any, 0)
				iIdx := 1
				for field, val := range msg.Data() {
					q, _ := sqlutil.QuoteIdent(driver, field)
					iCols = append(iCols, q)
					iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
					iArgs = append(iArgs, val)
					iIdx++
				}
				iCols = append(iCols, qStartDate)
				iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
				iArgs = append(iArgs, now)
				iIdx++

				if currentFlagCol != "" {
					qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
					iCols = append(iCols, qFlag)
					iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
					iArgs = append(iArgs, true)
				}

				iQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, strings.Join(iCols, ", "), strings.Join(iPhs, ", "))
				_, err = tx.ExecContext(ctx, iQuery, iArgs...)
				if err != nil {
					return msg, err
				}
				msg.SetMetadata("scd_action", "hybrid_update_insert")
			} else {
				msg.SetMetadata("scd_action", "hybrid_update")
			}

			tx.Commit()
		} else {
			msg.SetMetadata("scd_action", "none")
		}
	} else {
		// New record
		return t.handleType2(ctx, db, driver, table, businessKeys, nil, config, msg)
	}

	return msg, nil
}

func (t *SCDTransformer) handleType1(ctx context.Context, db *sql.DB, driver, table string, businessKeys, compareFields []string, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	// 1. Check if record exists
	whereParts := make([]string, 0, len(businessKeys))
	args := make([]any, 0, len(businessKeys))
	for i, key := range businessKeys {
		quotedKey, _ := sqlutil.QuoteIdent(driver, key)
		val := evaluator.GetMsgValByPath(msg, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", quotedKey, sqlutil.Placeholder(driver, i+1)))
		args = append(args, val)
	}

	selectCols := "*"
	if len(compareFields) > 0 {
		allCols := append([]string{}, businessKeys...)
		allCols = append(allCols, compareFields...)
		quotedCols := make([]string, 0, len(allCols))
		for _, col := range allCols {
			q, _ := sqlutil.QuoteIdent(driver, col)
			quotedCols = append(quotedCols, q)
		}
		selectCols = strings.Join(quotedCols, ", ")
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectCols, quotedTable, strings.Join(whereParts, " AND "))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return msg, fmt.Errorf("lookup failed: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		// Existing record found
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return msg, err
		}

		existingData := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				existingData[col] = string(b)
			} else {
				existingData[col] = val
			}
		}

		// Compare
		changed := false
		if len(compareFields) == 0 {
			changed = true
		} else {
			for _, field := range compareFields {
				newVal := evaluator.GetMsgValByPath(msg, field)
				oldVal := existingData[field]
				if !reflect.DeepEqual(newVal, oldVal) {
					changed = true
					break
				}
			}
		}

		if changed {
			// UPDATE
			updateParts := make([]string, 0)
			updateArgs := make([]any, 0)
			idx := 1
			for field, val := range msg.Data() {
				isBK := false
				for _, bk := range businessKeys {
					if bk == field {
						isBK = true
						break
					}
				}
				if isBK {
					continue
				}

				q, err := sqlutil.QuoteIdent(driver, field)
				if err != nil {
					continue
				}
				updateParts = append(updateParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
				updateArgs = append(updateArgs, val)
				idx++
			}

			if len(updateParts) > 0 {
				updateWhere := make([]string, 0)
				for _, bk := range businessKeys {
					q, _ := sqlutil.QuoteIdent(driver, bk)
					updateWhere = append(updateWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
					updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, bk))
					idx++
				}

				updateQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(updateParts, ", "), strings.Join(updateWhere, " AND "))
				_, err = db.ExecContext(ctx, updateQuery, updateArgs...)
				if err != nil {
					return msg, fmt.Errorf("update failed: %w", err)
				}
				msg.SetMetadata("scd_action", "update")
			}
		} else {
			msg.SetMetadata("scd_action", "none")
		}
	} else {
		return t.performInsert(ctx, db, driver, table, msg)
	}

	return msg, nil
}

func (t *SCDTransformer) performInsert(ctx context.Context, db *sql.DB, driver, table string, msg hermod.Message) (hermod.Message, error) {
	quotedTable, _ := sqlutil.QuoteIdent(driver, table)
	cols := make([]string, 0)
	phs := make([]string, 0)
	insertArgs := make([]any, 0)
	idx := 1
	for field, val := range msg.Data() {
		q, err := sqlutil.QuoteIdent(driver, field)
		if err != nil {
			continue
		}
		cols = append(cols, q)
		phs = append(phs, sqlutil.Placeholder(driver, idx))
		insertArgs = append(insertArgs, val)
		idx++
	}

	insertQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, strings.Join(cols, ", "), strings.Join(phs, ", "))
	_, err := db.ExecContext(ctx, insertQuery, insertArgs...)
	if err != nil {
		return msg, fmt.Errorf("insert failed: %w", err)
	}
	msg.SetMetadata("scd_action", "insert")
	return msg, nil
}

func (t *SCDTransformer) handleType2(ctx context.Context, db *sql.DB, driver, table string, businessKeys, compareFields []string, config map[string]any, msg hermod.Message) (hermod.Message, error) {
	quotedTable, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		return msg, err
	}

	startDateCol := getConfigString(config, "startDateColumn")
	endDateCol := getConfigString(config, "endDateColumn")
	currentFlagCol := getConfigString(config, "currentFlagColumn")

	if startDateCol == "" {
		startDateCol = "start_date"
	}
	if endDateCol == "" {
		endDateCol = "end_date"
	}

	qStartDate, _ := sqlutil.QuoteIdent(driver, startDateCol)
	qEndDate, _ := sqlutil.QuoteIdent(driver, endDateCol)

	// 1. Find current active record
	whereParts := make([]string, 0)
	args := make([]any, 0)
	idx := 1
	for _, key := range businessKeys {
		q, _ := sqlutil.QuoteIdent(driver, key)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, idx)))
		args = append(args, evaluator.GetMsgValByPath(msg, key))
		idx++
	}

	if currentFlagCol != "" {
		qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
		whereParts = append(whereParts, fmt.Sprintf("%s = %s", qFlag, sqlutil.Placeholder(driver, idx)))
		args = append(args, true)
		idx++
	} else {
		whereParts = append(whereParts, fmt.Sprintf("%s IS NULL", qEndDate))
	}

	selectCols := "*"
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectCols, quotedTable, strings.Join(whereParts, " AND "))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return msg, err
	}
	defer rows.Close()

	now := time.Now()

	if rows.Next() {
		// Existing record found
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return msg, err
		}

		existingData := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				existingData[col] = string(b)
			} else {
				existingData[col] = val
			}
		}

		// Compare
		changed := false
		for _, field := range compareFields {
			newVal := evaluator.GetMsgValByPath(msg, field)
			oldVal := existingData[field]
			if !reflect.DeepEqual(newVal, oldVal) {
				changed = true
				break
			}
		}

		if changed {
			// Transaction: Update old, Insert new
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return msg, err
			}
			defer tx.Rollback()

			// Update old record
			updateWhere := make([]string, 0)
			updateArgs := make([]any, 0)
			uIdx := 1
			updateArgs = append(updateArgs, now)
			setClause := fmt.Sprintf("%s = %s", qEndDate, sqlutil.Placeholder(driver, uIdx))
			uIdx++
			if currentFlagCol != "" {
				qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
				updateArgs = append(updateArgs, false)
				setClause += fmt.Sprintf(", %s = %s", qFlag, sqlutil.Placeholder(driver, uIdx))
				uIdx++
			}

			for _, key := range businessKeys {
				q, _ := sqlutil.QuoteIdent(driver, key)
				updateWhere = append(updateWhere, fmt.Sprintf("%s = %s", q, sqlutil.Placeholder(driver, uIdx)))
				updateArgs = append(updateArgs, evaluator.GetMsgValByPath(msg, key))
				uIdx++
			}
			if currentFlagCol != "" {
				qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
				updateWhere = append(updateWhere, fmt.Sprintf("%s = %s", qFlag, sqlutil.Placeholder(driver, uIdx)))
				updateArgs = append(updateArgs, true)
				uIdx++
			} else {
				updateWhere = append(updateWhere, fmt.Sprintf("%s IS NULL", qEndDate))
			}

			updateQuery := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, setClause, strings.Join(updateWhere, " AND "))
			_, err = tx.ExecContext(ctx, updateQuery, updateArgs...)
			if err != nil {
				return msg, err
			}

			// Insert new record
			iCols := make([]string, 0)
			iPhs := make([]string, 0)
			iArgs := make([]any, 0)
			iIdx := 1
			for field, val := range msg.Data() {
				q, err := sqlutil.QuoteIdent(driver, field)
				if err != nil {
					continue
				}
				iCols = append(iCols, q)
				iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
				iArgs = append(iArgs, val)
				iIdx++
			}
			// Add metadata columns
			iCols = append(iCols, qStartDate)
			iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
			iArgs = append(iArgs, now)
			iIdx++

			if currentFlagCol != "" {
				qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
				iCols = append(iCols, qFlag)
				iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
				iArgs = append(iArgs, true)
				iIdx++
			}

			insertQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, strings.Join(iCols, ", "), strings.Join(iPhs, ", "))
			_, err = tx.ExecContext(ctx, insertQuery, iArgs...)
			if err != nil {
				return msg, err
			}

			if err := tx.Commit(); err != nil {
				return msg, err
			}
			msg.SetMetadata("scd_action", "update_insert")
		} else {
			msg.SetMetadata("scd_action", "none")
		}
	} else {
		// New record
		iCols := make([]string, 0)
		iPhs := make([]string, 0)
		iArgs := make([]any, 0)
		iIdx := 1
		for field, val := range msg.Data() {
			q, err := sqlutil.QuoteIdent(driver, field)
			if err != nil {
				continue
			}
			iCols = append(iCols, q)
			iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
			iArgs = append(iArgs, val)
			iIdx++
		}
		// Add metadata columns
		iCols = append(iCols, qStartDate)
		iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
		iArgs = append(iArgs, now)
		iIdx++

		if currentFlagCol != "" {
			qFlag, _ := sqlutil.QuoteIdent(driver, currentFlagCol)
			iCols = append(iCols, qFlag)
			iPhs = append(iPhs, sqlutil.Placeholder(driver, iIdx))
			iArgs = append(iArgs, true)
			iIdx++
		}

		insertQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, strings.Join(iCols, ", "), strings.Join(iPhs, ", "))
		_, err = db.ExecContext(ctx, insertQuery, iArgs...)
		if err != nil {
			return msg, fmt.Errorf("initial insert failed: %w", err)
		}
		msg.SetMetadata("scd_action", "insert")
	}

	return msg, nil
}
