package ml

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// queryChunkRows is how many rows go to the worker per call when a dataset is
// filled from a query: enough to keep calls few, small enough that neither
// side holds much at once.
const queryChunkRows = 1000

// DefaultMaxQueryRows caps a dataset filled from a query when the caller names
// no cap.
const DefaultMaxQueryRows = 1_000_000

// DatasetFromQuery replaces a vhost's dataset with the rows a query returns,
// streaming them to the worker in chunks. Only a SELECT (or WITH … SELECT) is
// run, inside a read-only transaction where the driver offers one. A query
// returning more than maxRows rows is refused, and the dataset is left with
// the rows sent so far, which the next fill replaces.
func (s *Service) DatasetFromQuery(ctx context.Context, vhost, dataset string, db *sql.DB, query string, maxRows int) (int, error) {
	w, err := s.Worker()
	if err != nil {
		return 0, err
	}
	if !readsOnly(query) {
		return 0, errors.New("a dataset query must be a SELECT")
	}
	if maxRows <= 0 {
		maxRows = DefaultMaxQueryRows
	}

	var q interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	} = db
	// A driver that cannot open a read-only transaction still gets only a
	// SELECT; the transaction is a second guard where there is one.
	if tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); err == nil {
		defer func() { _ = tx.Rollback() }()
		q = tx
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("running the dataset query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	up := &uploader{ctx: ctx, w: w, vhost: vhost, dataset: dataset}
	if err := scanRows(rows, maxRows, up.add); err != nil {
		return up.total, err
	}
	// The last chunk, or an empty one so a query with no rows still replaces
	// the dataset rather than leaving the old rows to be trained on.
	if len(up.chunk) > 0 || up.sent == 0 {
		if err := up.flush(); err != nil {
			return up.total, err
		}
	}
	return up.total, nil
}

// uploader sends rows to a dataset in chunks; the first chunk replaces it.
type uploader struct {
	ctx            context.Context
	w              *worker.Client
	vhost, dataset string
	chunk          []map[string]any
	sent, total    int
}

func (u *uploader) add(row map[string]any) error {
	u.chunk = append(u.chunk, row)
	if len(u.chunk) < queryChunkRows {
		return nil
	}
	return u.flush()
}

func (u *uploader) flush() error {
	n, err := u.w.AppendRows(u.ctx, u.vhost, u.dataset, u.chunk, u.sent == 0)
	if err != nil {
		return err
	}
	u.sent += len(u.chunk)
	u.total = n
	u.chunk = u.chunk[:0]
	return nil
}

// scanRows hands each row to add as a column -> value map, refusing a result
// of more than maxRows rows.
func scanRows(rows *sql.Rows, maxRows int, add func(map[string]any) error) error {
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("reading the dataset query's columns: %w", err)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if n >= maxRows {
			return fmt.Errorf("the query returns more than %d rows; narrow it or raise the cap", maxRows)
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("reading a row of the dataset query: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = jsonValue(vals[i])
		}
		if err := add(row); err != nil {
			return err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading the dataset query: %w", err)
	}
	return nil
}

// readsOnly reports whether a query starts with SELECT or WITH.
func readsOnly(query string) bool {
	f := strings.Fields(strings.ToUpper(query))
	return len(f) > 0 && (f[0] == "SELECT" || f[0] == "WITH")
}

// jsonValue turns what a driver scans into something JSON carries as the
// worker expects: text for bytes, RFC 3339 for times.
func jsonValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	}
	return v
}
