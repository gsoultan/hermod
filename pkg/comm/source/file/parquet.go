package file

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/common"
	"github.com/xitongsys/parquet-go/reader"
)

// parquetChunkRows is how many rows are materialised at a time. The file's
// bytes are already buffered by readFileBytes; this bounds the far larger cost
// of holding every row as a map.
const parquetChunkRows = int64(1024)

// parquetColumn is one leaf column: the internal path ReadColumnByPath wants,
// and the name it had in the file.
type parquetColumn struct {
	inPath string
	name   string
}

// parquetRows iterates one parquet file, producing a message per row.
//
// It reads column-wise in chunks — which is how parquet is laid out — and
// transposes each chunk into rows, rather than asking the library for a Go
// struct it would have to be given a type for. The file's schema is whatever
// the producer wrote; nothing here is compiled in.
type parquetRows struct {
	pr      *reader.ParquetReader
	pf      io.Closer
	columns []parquetColumn

	remaining int64
	buf       []map[string]any
	pos       int
	row       int64

	fileName string
	table    string
	keyField string
	opField  string
}

// newParquetRows opens an in-memory parquet file. The caller owns b until Close.
func newParquetRows(b []byte, fileName, table, keyField, opField string) (*parquetRows, error) {
	pf := buffer.NewBufferFileFromBytes(b)
	pr, err := reader.NewParquetColumnReader(pf, 4)
	if err != nil {
		return nil, fmt.Errorf("open parquet file %s: %w", fileName, err)
	}

	columns := make([]parquetColumn, 0, len(pr.SchemaHandler.ValueColumns))
	for _, inPath := range pr.SchemaHandler.ValueColumns {
		path := common.StrToPath(inPath)
		// path[0] is the schema root. Anything deeper than one level below it is
		// a list, map or nested group, whose values do not line up one-per-row
		// with the flat columns beside them. Reading those as if they did would
		// shift every later column onto the wrong record, so refuse instead.
		if len(path) != 2 {
			pr.ReadStop()
			_ = pf.Close()
			return nil, fmt.Errorf("parquet file %s has a nested or repeated column (%s); "+
				"only flat schemas are supported",
				fileName, strings.Join(path[1:], "."))
		}
		name := path[len(path)-1]
		if ex, ok := pr.SchemaHandler.InPathToExPath[inPath]; ok && ex != "" {
			if exPath := common.StrToPath(ex); len(exPath) > 0 {
				name = exPath[len(exPath)-1]
			}
		}
		columns = append(columns, parquetColumn{inPath: inPath, name: name})
	}
	if len(columns) == 0 {
		pr.ReadStop()
		_ = pf.Close()
		return nil, fmt.Errorf("parquet file %s declares no columns", fileName)
	}

	return &parquetRows{
		pr:        pr,
		pf:        pf,
		columns:   columns,
		remaining: pr.GetNumRows(),
		fileName:  fileName,
		table:     table,
		keyField:  keyField,
		opField:   opField,
	}, nil
}

// fill materialises the next chunk of rows. It leaves buf empty at end of file.
func (p *parquetRows) fill() error {
	n := min(p.remaining, parquetChunkRows)
	if n <= 0 {
		p.buf, p.pos = nil, 0
		return nil
	}

	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = make(map[string]any, len(p.columns))
	}
	for _, col := range p.columns {
		values, _, _, err := p.pr.ReadColumnByPath(col.inPath, n)
		if err != nil {
			return fmt.Errorf("read parquet column %s of %s: %w", col.name, p.fileName, err)
		}
		// One value per row is what a flat column owes us. A short read would
		// otherwise slide the remaining values up onto earlier rows.
		if int64(len(values)) != n {
			return fmt.Errorf("parquet column %s of %s yielded %d values for %d rows",
				col.name, p.fileName, len(values), n)
		}
		for i, v := range values {
			rows[i][col.name] = v
		}
	}

	p.buf, p.pos = rows, 0
	p.remaining -= n
	return nil
}

// next returns the message for the next row, or nil when the file is drained.
func (p *parquetRows) next() (hermod.Message, error) {
	if p.pos >= len(p.buf) {
		if err := p.fill(); err != nil {
			return nil, err
		}
		if len(p.buf) == 0 {
			return nil, nil
		}
	}
	row := p.buf[p.pos]
	p.pos++
	p.row++

	op := hermod.OpCreate
	if p.opField != "" {
		if raw, ok := row[p.opField]; ok {
			parsed, err := parseOperation(raw)
			if err != nil {
				return nil, fmt.Errorf("%s row %d: %w", p.fileName, p.row, err)
			}
			op = parsed
			// The operation now lives in the envelope. Leaving it in the record
			// as well would have a database sink write an "operation" column the
			// target table has no reason to have, and the s3parquet sink puts it
			// back from the envelope on the way out.
			delete(row, p.opField)
		}
	}

	id, err := p.idFor(row, op)
	if err != nil {
		return nil, err
	}

	msg := message.AcquireMessage()
	msg.SetID(id)
	msg.SetOperation(op)
	if p.table != "" {
		msg.SetTable(p.table)
	}
	for k, v := range row {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "parquet")
	msg.SetMetadata("file_name", p.fileName)
	msg.SetMetadata("row", strconv.FormatInt(p.row, 10))
	return msg, nil
}

// idFor resolves the message ID, which is what a sink with no column mappings
// targets the row by.
func (p *parquetRows) idFor(row map[string]any, op hermod.Operation) (string, error) {
	if p.keyField == "" {
		// An update or a delete with a synthetic ID is a statement that matches
		// no row and still reports success — the file would appear to apply
		// while the target never changed. Refuse while it is still a config
		// problem rather than a silent one.
		if op == hermod.OpUpdate || op == hermod.OpDelete {
			return "", fmt.Errorf("%s row %d carries operation %q but no key_field is configured: "+
				"a sink without column mappings targets the row by message ID, so this would "+
				"match nothing and still report success",
				p.fileName, p.row, op)
		}
		return fmt.Sprintf("%s-%d", p.fileName, p.row), nil
	}

	v, ok := row[p.keyField]
	if !ok {
		return "", fmt.Errorf("%s row %d: key_field %q is not a column in this file (columns: %s)",
			p.fileName, p.row, p.keyField, strings.Join(p.columnNames(), ", "))
	}
	if v == nil {
		return "", fmt.Errorf("%s row %d: key_field %q is NULL, so the row cannot be targeted",
			p.fileName, p.row, p.keyField)
	}
	return fmt.Sprint(v), nil
}

func (p *parquetRows) columnNames() []string {
	names := make([]string, 0, len(p.columns))
	for _, c := range p.columns {
		names = append(names, c.name)
	}
	return names
}

func (p *parquetRows) Close() error {
	if p.pr != nil {
		p.pr.ReadStop()
		p.pr = nil
	}
	if p.pf != nil {
		err := p.pf.Close()
		p.pf = nil
		return err
	}
	return nil
}

// parseOperation maps the value of the operation column onto a CDC operation.
//
// It accepts the spelled-out names and the single letters Debezium uses, so a
// file produced by either convention loads without a transform in between. A
// value it does not recognise is an error: defaulting a misspelt "delete" to an
// insert is how a row that should have been removed comes quietly back.
func parseOperation(v any) (hermod.Operation, error) {
	var s string
	switch t := v.(type) {
	case nil:
		return hermod.OpCreate, nil
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		return "", fmt.Errorf("operation column holds %T (%v), want a string", v, v)
	}

	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "c", "i", "create", "insert":
		return hermod.OpCreate, nil
	case "u", "update", "upsert":
		return hermod.OpUpdate, nil
	case "d", "delete":
		return hermod.OpDelete, nil
	case "r", "s", "read", "snapshot":
		return hermod.OpSnapshot, nil
	default:
		return "", fmt.Errorf("unrecognised operation %q: want create/insert/c/i, "+
			"update/upsert/u, delete/d or snapshot/read/r/s", s)
	}
}
