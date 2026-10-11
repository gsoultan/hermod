package ml

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// returning more than maxRows rows, or more than the vhost's row or byte
// quota, is refused, and the dataset is left with the rows sent so far, which
// the next fill replaces.
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
	quota, err := s.Quotas(ctx, vhost)
	if err != nil {
		return 0, err
	}
	if err := s.checkNewDataset(ctx, vhost, dataset, quota.MaxDatasets); err != nil {
		return 0, err
	}
	// The vhost's row quota binds when it is the smaller cap; the refusal
	// then says it is a quota, not the caller's own cap.
	var overRows func() error
	if quota.MaxDatasetRows > 0 && quota.MaxDatasetRows < int64(maxRows) {
		maxRows = int(quota.MaxDatasetRows)
		overRows = func() error {
			return refuse(vhost, QuotaDatasetRows, false, "may hold at most %d rows in one dataset", quota.MaxDatasetRows)
		}
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

	up := &uploader{ctx: ctx, w: w, vhost: vhost, dataset: dataset, maxBytes: quota.MaxDatasetBytes}
	if err := scanRows(rows, maxRows, up.add, overRows); err != nil {
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
	// maxBytes is the vhost's byte quota for one dataset, 0 for none; bytes
	// is what the chunks sent so far took as JSON.
	maxBytes, bytes int64
}

func (u *uploader) add(row map[string]any) error {
	u.chunk = append(u.chunk, row)
	if len(u.chunk) < queryChunkRows {
		return nil
	}
	return u.flush()
}

func (u *uploader) flush() error {
	if u.maxBytes > 0 {
		// Measured as the worker is sent it. A chunk that would go over is
		// not sent, so the dataset never holds more than the quota.
		b, err := json.Marshal(u.chunk)
		if err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}
		if u.bytes+int64(len(b)) > u.maxBytes {
			return refuse(u.vhost, QuotaDatasetBytes, false, "may send at most %d bytes to one dataset", u.maxBytes)
		}
		u.bytes += int64(len(b))
	}
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
// of more than maxRows rows with overRows' error, or a plain one when it is
// nil.
func scanRows(rows *sql.Rows, maxRows int, add func(map[string]any) error, overRows func() error) error {
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
			if overRows != nil {
				return overRows()
			}
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

// UploadDataset replaces a vhost's dataset with a CSV or Excel file. A new
// dataset counts against the vhost's dataset quota, the file against its byte
// quota as it streams, and the rows the worker finds in it against the row
// quota. The worker replaces a dataset as it reads the file, so one found to
// hold too many rows is removed rather than left over the quota.
func (s *Service) UploadDataset(ctx context.Context, vhost, dataset, format string, body io.Reader) (worker.DatasetInfo, error) {
	w, err := s.Worker()
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	q, err := s.Quotas(ctx, vhost)
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	if err := s.checkNewDataset(ctx, vhost, dataset, q.MaxDatasets); err != nil {
		return worker.DatasetInfo{}, err
	}
	var counted *byteQuota
	if q.MaxDatasetBytes > 0 {
		counted = &byteQuota{r: body, left: q.MaxDatasetBytes}
		body = counted
	}
	info, err := w.UploadFile(ctx, vhost, dataset, format, body)
	if counted != nil && counted.over {
		return worker.DatasetInfo{}, refuse(vhost, QuotaDatasetBytes, false, "may send at most %d bytes to one dataset", q.MaxDatasetBytes)
	}
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	if q.MaxDatasetRows > 0 && int64(info.Rows) > q.MaxDatasetRows {
		if derr := w.DeleteDataset(ctx, vhost, dataset); derr != nil && !errors.Is(derr, worker.ErrNotFound) {
			return worker.DatasetInfo{}, fmt.Errorf("%w; removing the dataset failed too: %w",
				refuse(vhost, QuotaDatasetRows, false, "may hold at most %d rows in one dataset, and the file holds %d", q.MaxDatasetRows, info.Rows), derr)
		}
		return worker.DatasetInfo{}, refuse(vhost, QuotaDatasetRows, false,
			"may hold at most %d rows in one dataset, and the file holds %d; the dataset was removed", q.MaxDatasetRows, info.Rows)
	}
	return info, nil
}

// byteQuota fails a read that takes the stream past left bytes.
type byteQuota struct {
	r    io.Reader
	left int64
	over bool
}

func (b *byteQuota) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		b.over = true
		return 0, ErrQuotaExceeded
	}
	return n, err
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
