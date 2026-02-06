package transformer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

func init() {
	Register("execute_sql", &ExecuteSQLTransformer{})
}

type ExecuteSQLTransformer struct{}

func (t *ExecuteSQLTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value("registry").(interface {
		GetSource(ctx context.Context, id string) (storage.Source, error)
		GetOrOpenDB(src storage.Source) (*sql.DB, error)
	})

	if !ok {
		return msg, fmt.Errorf("registry not found in context")
	}

	sourceID, _ := config["sourceId"].(string)
	queryTemplate, _ := config["queryTemplate"].(string)

	if sourceID == "" || queryTemplate == "" {
		return msg, nil
	}

	src, err := registry.GetSource(ctx, sourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get source for execute_sql: %w", err)
	}

	db, err := registry.GetOrOpenDB(src)
	if err != nil {
		return msg, fmt.Errorf("failed to get database for execute_sql: %w", err)
	}

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

	sqlText, args := parameterizeTemplate(driver, queryTemplate, msg.Data())
	if strings.TrimSpace(sqlText) == "" {
		return msg, fmt.Errorf("empty queryTemplate after processing")
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
