package lookup

import (
	"archive/zip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xlsx "github.com/tealeg/xlsx"
)

// Bounds on a reference file. The whole table is held in memory for as long
// as a workflow uses it, once per distinct file and settings.
const (
	defaultReferenceBytes = 32 << 20
	maxReferenceBytes     = 256 << 20
	defaultReferenceRows  = 200_000
	maxReferenceRows      = 2_000_000
	// xlsxInflation bounds an .xlsx's uncompressed size as a multiple of
	// maxBytes. An .xlsx is a zip, and the library inflates all of it.
	xlsxInflation = 10
)

// referenceStatEvery is how often a cached table checks its file for changes.
// Checking on every record would put a stat call on the hot path.
var referenceStatEvery = time.Second

// referenceSpec is what a table is read with. Two nodes with the same spec
// share one table.
type referenceSpec struct {
	path, format, sheet, delimiter, keyColumn string
	maxBytes, maxRows                         int
}

// referenceTable is a loaded file, indexed by its key column.
type referenceTable struct {
	columns []string
	rows    map[string]map[string]string
}

// referenceEntry caches one spec's table with the file state it was read at.
type referenceEntry struct {
	mu      sync.Mutex
	table   *referenceTable
	modTime time.Time
	size    int64
	checked time.Time
}

var referenceCache = struct {
	sync.Mutex
	entries map[referenceSpec]*referenceEntry
}{entries: map[referenceSpec]*referenceEntry{}}

// loadReference returns spec's table, reading the file the first time and
// again whenever its modification time or size has changed.
func loadReference(spec referenceSpec) (*referenceTable, error) {
	referenceCache.Lock()
	e, ok := referenceCache.entries[spec]
	if !ok {
		e = &referenceEntry{}
		referenceCache.entries[spec] = e
	}
	referenceCache.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if e.table != nil && now.Sub(e.checked) < referenceStatEvery {
		return e.table, nil
	}
	info, err := statReference(spec)
	if err != nil {
		return nil, err
	}
	e.checked = now
	if e.table != nil && info.ModTime().Equal(e.modTime) && info.Size() == e.size {
		return e.table, nil
	}
	table, err := readReference(spec)
	if err != nil {
		return nil, err
	}
	e.table, e.modTime, e.size = table, info.ModTime(), info.Size()
	return table, nil
}

func statReference(spec referenceSpec) (os.FileInfo, error) {
	info, err := os.Stat(spec.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", spec.path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", spec.path)
	}
	if info.Size() > int64(spec.maxBytes) {
		return nil, fmt.Errorf("%s is %d bytes, above the %d bytes this node reads", spec.path, info.Size(), spec.maxBytes)
	}
	return info, nil
}

// referenceFormat is the format the node names, or the one the file's
// extension implies.
func referenceFormat(path, format string) (string, error) {
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
	switch format {
	case "csv", "tsv", "xlsx":
		return format, nil
	}
	return "", fmt.Errorf("%s: format %q is not csv, tsv or xlsx", path, format)
}

func readReference(spec referenceSpec) (*referenceTable, error) {
	var records [][]string
	var err error
	if spec.format == "xlsx" {
		records, err = readXLSX(spec)
	} else {
		records, err = readCSV(spec)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", spec.path, err)
	}
	return indexReference(spec, records)
}

func readCSV(spec referenceSpec) ([][]string, error) {
	f, err := os.Open(spec.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(io.LimitReader(f, int64(spec.maxBytes)))
	r.FieldsPerRecord = -1
	switch {
	case spec.delimiter == "tab" || spec.delimiter == `\t` || (spec.delimiter == "" && spec.format == "tsv"):
		r.Comma = '\t'
	case spec.delimiter != "":
		r.Comma = []rune(spec.delimiter)[0]
	}
	var records [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, err
		}
		// One more than the limit: the header row is not a data row.
		if len(records) > spec.maxRows {
			return nil, rowLimitError(spec)
		}
		records = append(records, rec)
	}
}

func readXLSX(spec referenceSpec) ([][]string, error) {
	if err := checkInflatedSize(spec.path, int64(spec.maxBytes)*xlsxInflation); err != nil {
		return nil, err
	}
	wb, err := xlsx.OpenFile(spec.path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	var sh *xlsx.Sheet
	switch {
	case spec.sheet != "":
		named, ok := wb.Sheet[spec.sheet]
		if !ok {
			return nil, fmt.Errorf("sheet not found: %s", spec.sheet)
		}
		sh = named
	case len(wb.Sheets) > 0:
		sh = wb.Sheets[0]
	default:
		return nil, errors.New("xlsx has no sheets")
	}
	if len(sh.Rows) > spec.maxRows+1 {
		return nil, rowLimitError(spec)
	}
	records := make([][]string, 0, len(sh.Rows))
	for _, row := range sh.Rows {
		rec := make([]string, len(row.Cells))
		for i, c := range row.Cells {
			rec[i] = c.String()
		}
		records = append(records, rec)
	}
	return records, nil
}

// checkInflatedSize refuses a zip whose entries would inflate past limit,
// before anything inflates them.
func checkInflatedSize(path string, limit int64) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = zr.Close() }()
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if total > uint64(limit) {
		return fmt.Errorf("the workbook inflates to %d bytes, above the %d allowed", total, limit)
	}
	return nil
}

func rowLimitError(spec referenceSpec) error {
	return fmt.Errorf("the file has more than the %d rows this node reads", spec.maxRows)
}

// indexReference keys the data rows by the key column. The first row is the
// header. A key that appears twice keeps its first row.
func indexReference(spec referenceSpec, records [][]string) (*referenceTable, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("%s is empty; its first row must name the columns", spec.path)
	}
	header := make([]string, len(records[0]))
	keyIdx := -1
	for i, h := range records[0] {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		header[i] = h
		if h == spec.keyColumn {
			keyIdx = i
		}
	}
	if keyIdx < 0 {
		return nil, fmt.Errorf("%s has no column %q; its columns are %s", spec.path, spec.keyColumn, strings.Join(header, ", "))
	}
	rows := make(map[string]map[string]string, len(records)-1)
	for _, rec := range records[1:] {
		if keyIdx >= len(rec) {
			continue
		}
		key := strings.TrimSpace(rec[keyIdx])
		if _, seen := rows[key]; seen {
			continue
		}
		row := make(map[string]string, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			} else {
				row[h] = ""
			}
		}
		rows[key] = row
	}
	return &referenceTable{columns: header, rows: rows}, nil
}
