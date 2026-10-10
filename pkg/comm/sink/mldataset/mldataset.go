// Package mldataset is the Collect Dataset sink: each message a workflow sends
// it becomes a row of a dataset on Hermod's hermod-ml worker, so the next
// training of a model on that dataset learns from it.
//
// The sink appends; it never replaces a dataset. Batching is the engine's:
// the sink's batch_size and batch_timeout settings decide how many rows go to
// the worker in one call, and a failed call fails the batch, which the engine
// retries and then sends to the dead-letter sink like any other sink's.
// Nothing is held in the sink, so there is nothing to lose when it closes.
package mldataset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/security"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// DefaultMaxRows caps a dataset the sink fills when its config names no cap,
// the same cap a dataset filled from a query has.
const DefaultMaxRows = 1_000_000

// defaultVHost is the vhost of a workflow and a sink that name none.
const defaultVHost = "default"

// datasetName is the rule the worker applies to a dataset name.
var datasetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// maskTypes are the masks security.MaskValue applies.
var maskTypes = map[string]bool{"all": true, "partial": true, "email": true, "pii": true}

// Appender is what the sink needs of the ML worker; *worker.Client is one.
type Appender interface {
	AppendRows(ctx context.Context, vhost, dataset string, rows []map[string]any, replace bool) (int, error)
	Dataset(ctx context.Context, vhost, dataset string) (worker.DatasetInfo, error)
	Ready(ctx context.Context) error
}

// Sink appends each message to a dataset of its workflow's vhost.
//
// Config:
//   - dataset: required; the dataset's name.
//   - column_mappings: which fields become which columns, as the database
//     sinks write them: [{"source_field": "customer.age", "target_column":
//     "age"}, ...]. A blank source_field reads the column's own name. Empty
//     means the whole record, nested objects flattened to a_b columns.
//   - mask_fields: columns to mask before the row leaves Hermod, comma
//     separated; mask_type is all (the default), partial, email or pii, as
//     the mask node applies them.
//   - max_rows: the dataset stops growing at this many rows, default
//     DefaultMaxRows. Later rows are not added, and a warning says so once.
type Sink struct {
	dataset  string
	columns  []sqlutil.ColumnMapping
	mask     map[string]bool
	maskType string
	maxRows  int
	vhost    string
	w        Appender
	logger   hermod.Logger

	mu sync.Mutex
	// held is how many rows each vhost's dataset holds, as far as the sink
	// knows: read from the worker once, then from what each append returns.
	held map[string]int
	// warned is set once the sink has said a dataset is full.
	warned map[string]bool
}

// New builds a sink for a sink of vhost (empty for the default vhost) from
// its config.
func New(cfg map[string]string, vhost string, w Appender) (*Sink, error) {
	if w == nil {
		return nil, errors.New("ml_dataset sink: no ML worker is configured: set HERMOD_ML_WORKER_URL")
	}
	s := &Sink{
		dataset: strings.TrimSpace(cfg["dataset"]), vhost: vhost, w: w,
		maskType: strings.TrimSpace(cfg["mask_type"]), maxRows: DefaultMaxRows,
		held: map[string]int{}, warned: map[string]bool{},
	}
	if s.dataset == "" {
		return nil, errors.New("ml_dataset sink: name the dataset rows are added to")
	}
	if !datasetName.MatchString(s.dataset) {
		return nil, fmt.Errorf("ml_dataset sink: dataset name %q may hold only letters, digits, '_', '.' or '-'", s.dataset)
	}
	columns, err := parseColumns(cfg["column_mappings"])
	if err != nil {
		return nil, err
	}
	s.columns = columns
	if s.maskType == "" {
		s.maskType = "all"
	}
	if !maskTypes[s.maskType] {
		return nil, fmt.Errorf("ml_dataset sink: mask_type is all, partial, email or pii, not %q", s.maskType)
	}
	for _, f := range strings.Split(cfg["mask_fields"], ",") {
		if f = strings.TrimSpace(f); f != "" {
			if s.mask == nil {
				s.mask = map[string]bool{}
			}
			s.mask[f] = true
		}
	}
	if raw := strings.TrimSpace(cfg["max_rows"]); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("ml_dataset sink: max_rows must be a whole number of rows, not %q", raw)
		}
		if n > 0 {
			s.maxRows = n
		}
	}
	return s, nil
}

// parseColumns reads column_mappings, refusing a field mapped to no column.
func parseColumns(raw string) ([]sqlutil.ColumnMapping, error) {
	columns, err := sqlutil.ParseColumnMappings(raw)
	if err != nil {
		return nil, fmt.Errorf("ml_dataset sink: column_mappings must be a JSON list of {source_field, target_column}: %w", err)
	}
	for _, c := range columns {
		if strings.TrimSpace(c.TargetColumn) == "" {
			return nil, fmt.Errorf("ml_dataset sink: field %q is mapped to no column", c.SourceField)
		}
	}
	return columns, nil
}

// SetLogger is where the sink says a dataset is full.
func (s *Sink) SetLogger(l hermod.Logger) { s.logger = l }

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// WriteBatch appends the batch's rows to the dataset in one call per vhost.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	byVHost := map[string][]map[string]any{}
	var order []string
	for _, msg := range msgs {
		if msg == nil || msg.Operation() == hermod.OpDelete {
			// A deletion is not an example to learn from.
			continue
		}
		vhost, err := s.vhostOf(msg)
		if err != nil {
			return err
		}
		if _, seen := byVHost[vhost]; !seen {
			order = append(order, vhost)
		}
		byVHost[vhost] = append(byVHost[vhost], s.row(msg))
	}
	for _, vhost := range order {
		if err := s.append(ctx, vhost, byVHost[vhost]); err != nil {
			return err
		}
	}
	return nil
}

// vhostOf is the vhost whose dataset the message goes to: the vhost the engine
// marked it with, which is its workflow's, and never anything its payload
// says. A sink of one vhost refuses a workflow of another.
func (s *Sink) vhostOf(msg hermod.Message) (string, error) {
	var marked string
	if scoped, ok := msg.(hermod.VHostScoped); ok {
		marked = scoped.VHost()
	}
	switch {
	case marked != "" && s.vhost != "" && marked != s.vhost:
		return "", fmt.Errorf("%w: ml_dataset sink of vhost %q cannot add rows from a workflow of vhost %q",
			hermod.ErrPermanent, s.vhost, marked)
	case marked != "":
		return marked, nil
	case s.vhost != "":
		return s.vhost, nil
	}
	return defaultVHost, nil
}

// row is the message as a dataset row: the mapped columns, or the whole
// record flattened, with the masked columns masked. The message is not
// changed.
func (s *Sink) row(msg hermod.Message) map[string]any {
	row := map[string]any{}
	if len(s.columns) == 0 {
		flatten("", msg.Data(), row)
	} else {
		for _, c := range s.columns {
			row[c.TargetColumn] = value(evaluator.GetMsgValByPath(msg, c.SourceField))
		}
	}
	for col := range s.mask {
		if v, ok := row[col]; ok && v != nil {
			row[col] = security.MaskValue(fmt.Sprint(v), s.maskType)
		}
	}
	return row
}

// flatten writes data's fields into row, a nested object's as parent_child.
func flatten(prefix string, data map[string]any, row map[string]any) {
	for k, v := range data {
		key := k
		if prefix != "" {
			key = prefix + "_" + k
		}
		if nested, ok := v.(map[string]any); ok {
			flatten(key, nested, row)
			continue
		}
		row[key] = value(v)
	}
}

// value is what JSON carries to the worker as it expects: text for bytes and
// RFC 3339 for times. Lists stay lists; the worker keeps them as JSON text.
func value(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case json.Number:
		return x.String()
	}
	return v
}

// append sends rows to the vhost's dataset, as many as fit under the cap.
// Appends are one at a time, so the count the cap is checked against is the
// one the last append returned.
func (s *Sink) append(ctx context.Context, vhost string, rows []map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, known := s.held[vhost]
	if !known {
		info, err := s.w.Dataset(ctx, vhost, s.dataset)
		switch {
		case err == nil:
			held = info.Rows
		case errors.Is(err, worker.ErrNotFound):
			held = 0
		default:
			return fmt.Errorf("ml_dataset sink: reading dataset %q: %w", s.dataset, err)
		}
		s.held[vhost] = held
	}
	if room := s.maxRows - held; len(rows) > room {
		if !s.warned[vhost] && s.logger != nil {
			s.logger.Warn("Dataset is full; further rows are not added",
				"vhost", vhost, "dataset", s.dataset, "max_rows", s.maxRows)
		}
		s.warned[vhost] = true
		rows = rows[:max(room, 0)]
	}
	if len(rows) == 0 {
		return nil
	}
	total, err := s.w.AppendRows(ctx, vhost, s.dataset, rows, false)
	if err != nil {
		return fmt.Errorf("ml_dataset sink: adding %d rows to dataset %q: %w", len(rows), s.dataset, err)
	}
	s.held[vhost] = total
	return nil
}

// Ping checks the worker answers.
func (s *Sink) Ping(ctx context.Context) error {
	return s.w.Ready(ctx)
}

// Close has nothing to flush: every write reached the worker before it
// returned.
func (s *Sink) Close() error { return nil }
