package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("ml_recommend_build", &RecommendBuildNode{})
}

// recommendBuilder is the registry's batch build of a recommendation model
// (internal/engine/registry/registry_ml.go): every salesman's, customer's or
// product's answer as rows, written to the target table when config names
// one, refused before anything is written when there are more than maxRows.
type recommendBuilder interface {
	MLRecommendBuild(ctx context.Context, vhost, model string, config map[string]any) ([]map[string]any, int, error)
}

// defaultMaxBuildMessages bounds the messages one build emits when the node
// names no maxRows: a build of every customer can be far larger than a
// workflow should carry, and its table is the place for that.
const defaultMaxBuildMessages = 10000

// RecommendBuildNode builds a recommendation model's answers for every
// salesman, customer or product of its vhost and emits one message per
// result row, each the incoming record with the row's fields on it: model,
// version, recommendation_id, for_id, item_id, rank, score, plan_date,
// due_date, reasons_text, built_at. With a target it also writes the rows to
// that table first, as a scheduled build does (docs/ml.md, "Build
// recommendations"); due_date only when the table has that column.
//
// Config:
//   - model: the recommendation model. Required.
//   - for: all_salesmen, all_customers or all_products. Required.
//   - periodDays: a visit plan's days from today; the model's period when
//     empty.
//   - withinDays: for a reorder model, only the customers due to order
//     within that many days from today, overdue ones included.
//   - k: results per subject; the model's default when empty.
//   - targetSourceId, targetTable, targetMode (replace or append): the
//     table to write; optional, both of the first two or neither.
//   - maxRows: the most rows one build may make, default 10000. More fails
//     the record before anything is written.
type RecommendBuildNode struct{}

func (n *RecommendBuildNode) Execute(ctx context.Context, _ interfaces.NodeContext, _ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	model, _ := node.Config["model"].(string)
	if model = strings.TrimSpace(model); model == "" {
		return nil, "", errors.New("ml_recommend_build: name the recommendation model")
	}
	builder, ok := ctx.Value(hermod.RegistryKey).(recommendBuilder)
	if !ok {
		return nil, "", errors.New("ml_recommend_build: this registry cannot build recommendations")
	}
	config := map[string]any{}
	for _, key := range []string{"for", "targetSourceId", "targetTable", "targetMode"} {
		s, _ := node.Config[key].(string)
		config[key] = strings.TrimSpace(s)
	}
	for _, key := range []string{"periodDays", "withinDays", "k", "maxRows"} {
		v, err := wholeNumber(node.Config[key])
		if err != nil {
			return nil, "", fmt.Errorf("ml_recommend_build: %s: %w", key, err)
		}
		config[key] = v
	}
	if n, _ := config["maxRows"].(int); n <= 0 {
		config["maxRows"] = defaultMaxBuildMessages
	}
	if (config["targetSourceId"] == "") != (config["targetTable"] == "") {
		return nil, "", errors.New("ml_recommend_build: a target names both targetSourceId and targetTable")
	}
	vhost := "default"
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped.VHost() != "" {
		vhost = scoped.VHost()
	}
	rows, _, err := builder.MLRecommendBuild(ctx, vhost, model, config)
	if err != nil {
		return nil, "", fmt.Errorf("ml_recommend_build: %w", err)
	}
	total := strconv.Itoa(len(rows))
	out := make([]hermod.Message, 0, len(rows))
	for i, row := range rows {
		m := msg.Clone()
		for _, k := range buildFields {
			m.SetData(k, row[k])
		}
		m.SetMetadata("_fanout_group", msg.ID())
		m.SetMetadata("_fanout_index", strconv.Itoa(i))
		m.SetMetadata("_fanout_total", total)
		out = append(out, m)
	}
	return out, "", nil
}

// buildFields are a build row's fields, in the order the table has them.
var buildFields = []string{"model", "version", "recommendation_id", "for_id", "item_id", "rank", "score", "plan_date", "due_date",
	"reasons_text", "built_at"}

// wholeNumber reads a setting the editor may save as a number or text; 0
// when unset.
func wholeNumber(v any) (int, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return n, nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%v is not a whole number", n)
		}
		return int(n), nil
	case string:
		if strings.TrimSpace(n) == "" {
			return 0, nil
		}
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", n)
		}
		return i, nil
	}
	return 0, fmt.Errorf("%T is not a whole number", v)
}
