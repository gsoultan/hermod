package advanced

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
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

	// An error, not a silent pass-through. This transformer exists to write
	// rows; a node missing either field used to write none and report success,
	// which leaves the pipeline green and the table empty -- the one outcome
	// nothing downstream can detect.
	if sourceID == "" || queryTemplate == "" {
		return msg, fmt.Errorf("execute_sql: incomplete config (sourceId=%q, queryTemplate=%q)",
			sourceID, queryTemplate)
	}

	// No CDC guard here, deliberately, and it is worth saying why because
	// db_lookup and batch_sql both have one.
	//
	// Theirs is about read load: a per-message query against a database that is
	// also serving logical replication puts that load exactly where it hurts.
	// That argument holds whatever table is being read, so a blanket refusal is
	// right for them.
	//
	// A write is a different question. The danger is not load, it is feeding the
	// stream the source is reading -- and whether that happens depends entirely
	// on whether the target table is in the publication, which nothing in this
	// config can tell us. Writing to an audit table that the publication does not
	// include is an ordinary, correct thing to do against a CDC database, and a
	// blanket refusal would break it. The workflow validator already declined
	// this for the same reason; see TestValidateWorkflowLeavesExecuteSQLAlone in
	// internal/workflow/transport/http. Refusing here and not there would also
	// mean a workflow that validates clean and then fails on every message.
	db, driver, err := registry.GetOrOpenDBByID(ctx, sourceID)
	if err != nil {
		return msg, fmt.Errorf("failed to get database for execute_sql: %w", err)
	}

	b := core.ParameterizeTemplateEx(driver, queryTemplate, msg.Data())
	if b.Err != nil {
		return msg, b.Err
	}

	// A token that resolved to nothing is bound as NULL, which is deliberate:
	// an optional message field is a legitimate reason for a path to be empty,
	// and on a write NULL is the fail-safe direction -- a WHERE that matches no
	// rows rather than one that matches too many. But a typo looks exactly the
	// same, and then the statement runs and changes nothing, forever, silently.
	// The two cannot be told apart automatically, so this is a choice the node
	// makes rather than one made for it. Default stays as it was.
	if strings.EqualFold(strings.TrimSpace(core.GetConfigString(config, "onUnresolved")), "fail") && len(b.Unresolved) > 0 {
		return msg, fmt.Errorf("execute_sql: template variable(s) resolved to nothing on this message: %s",
			strings.Join(b.Unresolved, ", "))
	}

	sqlText, args := b.SQL, b.Args
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
