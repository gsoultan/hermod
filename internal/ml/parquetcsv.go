// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/common"
	"github.com/xitongsys/parquet-go/reader"
)

// parquetChunk is how many rows are materialised at a time when a parquet
// file is turned into CSV.
const parquetChunk = int64(4096)

// parquetColumn is one flat column of the export: its path in the file and
// the name the CSV header gets.
type parquetColumn struct{ inPath, name string }

// parquetColumns lists the file's columns, refusing a nested one.
func parquetColumns(pr *reader.ParquetReader) ([]parquetColumn, error) {
	var columns []parquetColumn
	for _, inPath := range pr.SchemaHandler.ValueColumns {
		path := common.StrToPath(inPath)
		if len(path) != 2 {
			return nil, errors.New("the exported dataset has a nested column, which a CSV cannot hold")
		}
		name := path[1]
		if ex, ok := pr.SchemaHandler.InPathToExPath[inPath]; ok && ex != "" {
			if exPath := common.StrToPath(ex); len(exPath) > 0 {
				name = exPath[len(exPath)-1]
			}
		}
		columns = append(columns, parquetColumn{inPath: inPath, name: name})
	}
	if len(columns) == 0 {
		return nil, errors.New("the exported dataset has no columns")
	}
	return columns, nil
}

// readChunk reads the next n values of every column as CSV cells, column by
// column, into cells.
func readChunk(pr *reader.ParquetReader, columns []parquetColumn, n int64, cells [][]string) error {
	for i, col := range columns {
		values, _, _, err := pr.ReadColumnByPath(col.inPath, n)
		if err != nil {
			return fmt.Errorf("reading column %s of the exported dataset: %w", col.name, err)
		}
		if int64(len(values)) != n {
			return fmt.Errorf("column %s of the exported dataset yielded %d values for %d rows", col.name, len(values), n)
		}
		cells[i] = cells[i][:0]
		for _, v := range values {
			cells[i] = append(cells[i], csvCell(v))
		}
	}
	return nil
}

// parquetToCSV writes the rows of a flat parquet file (what the ML worker's
// export produces: bool, number and string columns) to w as CSV with a header
// row, and reports how many rows it wrote. A missing value is an empty cell.
func parquetToCSV(data []byte, w io.Writer) (int64, error) {
	pf := buffer.NewBufferFileFromBytes(data)
	pr, err := reader.NewParquetColumnReader(pf, 2)
	if err != nil {
		return 0, fmt.Errorf("reading the exported dataset: %w", err)
	}
	defer pr.ReadStop()
	columns, err := parquetColumns(pr)
	if err != nil {
		return 0, err
	}

	cw := csv.NewWriter(w)
	header := make([]string, len(columns))
	for i, col := range columns {
		header[i] = col.name
	}
	if err := cw.Write(header); err != nil {
		return 0, err
	}
	var written int64
	cells := make([][]string, len(columns))
	row := make([]string, len(columns))
	for remaining := pr.GetNumRows(); remaining > 0; {
		n := min(remaining, parquetChunk)
		if err := readChunk(pr, columns, n, cells); err != nil {
			return written, err
		}
		for r := range n {
			for i := range columns {
				row[i] = cells[i][r]
			}
			if err := cw.Write(row); err != nil {
				return written, err
			}
			written++
		}
		remaining -= n
	}
	cw.Flush()
	return written, cw.Error()
}

// csvCell is one parquet value as a CSV cell: what a spreadsheet would show.
func csvCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return fmt.Sprint(v)
}
