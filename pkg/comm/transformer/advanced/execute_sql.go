package advanced

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/transformer/core"
)

func init() {
	transformer.Register("execute_sql", &ExecuteSQLTransformer{})
}

type ExecuteSQLTransformer struct{}

func (t *ExecuteSQLTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value(hermod.RegistryKey).(interface {
		GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error)
	})

	if !ok {
		return msg, errors.New("registry not found in context or does not implement GetOrOpenDBByID")
	}

	sourceID, _ := config["sourceId"].(string)
	queryTemplate, _ := config["queryTemplate"].(string)

	if sourceID == "" || queryTemplate == "" {
		return msg, nil
	}

	db, driver, err := registry.GetOrOpenDBByID(ctx, sourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get database for execute_sql: %w", err)
	}

	sqlText, args := core.ParameterizeTemplate(driver, queryTemplate, msg.Data())
	if strings.TrimSpace(sqlText) == "" {
		return msg, errors.New("empty queryTemplate after processing")
	}

	res, err := db.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return msg, fmt.Errorf("failed to execute SQL: %w", err)
	}

	// Optionally store affected rows
	if targetField, ok := config["affectedRowsField"].(string); ok && targetField != "" {
		rows, _ := res.RowsAffected()
		msg.SetData(targetField, rows)
	}

	return msg, nil
}
