// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package table

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tealeg/xlsx"
)

// naText are the cells pandas.read_csv reads as missing by default.
var naText = map[string]bool{
	"": true, "#N/A": true, "#N/A N/A": true, "#NA": true, "-1.#IND": true, "-1.#QNAN": true, "-NaN": true,
	"-nan": true, "1.#IND": true, "1.#QNAN": true, "<NA>": true, "N/A": true, "NA": true, "NULL": true,
	"NaN": true, "None": true, "n/a": true, "nan": true, "null": true,
}

// boolText are the cells pandas reads as booleans, when a column holds no
// other value.
var boolText = map[string]bool{"True": true, "TRUE": true, "true": true, "False": false, "FALSE": false, "false": false}

// ParseCSV reads a CSV file with a header row, as the worker's pandas does:
// UTF-8 (a byte order mark is skipped), the usual missing markers (NA, null,
// an empty cell, …) missing, a column of True/False cells bool, a column of
// numbers number, and anything else string.
func ParseCSV(r io.Reader) (*Table, error) {
	br := bufio.NewReader(r)
	if bom, err := br.Peek(3); err == nil && bytes.Equal(bom, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.ReuseRecord = false
	header, err := cr.Read()
	if errors.Is(err, io.EOF) || (err == nil && len(header) == 1 && strings.TrimSpace(header[0]) == "") {
		return nil, errors.New("the CSV file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse the CSV file: %w", err)
	}
	for i, h := range header {
		if h == "" {
			header[i] = fmt.Sprintf("Unnamed: %d", i)
		}
	}
	if err := checkUnique(header); err != nil {
		return nil, err
	}
	cells, err := readCells(cr, len(header))
	if err != nil {
		return nil, err
	}
	t := &Table{}
	if len(cells) > 0 {
		t.Rows = len(cells[0])
	}
	for i, name := range header {
		t.Columns = append(t.Columns, textColumn(name, cells[i]))
	}
	return t, nil
}

// readCells reads the records after the header, column by column.
func readCells(cr *csv.Reader, width int) ([][]string, error) {
	cells := make([][]string, width)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return cells, nil
		}
		if err != nil {
			return nil, fmt.Errorf("could not parse the CSV file: %w", err)
		}
		for i := range width {
			cells[i] = append(cells[i], rec[i])
		}
	}
}

// textColumn types a column of CSV cells.
func textColumn(name string, cells []string) *Column {
	allBool, allNum, seen := true, true, false
	for _, s := range cells {
		if naText[s] {
			continue
		}
		seen = true
		if _, ok := boolText[s]; !ok {
			allBool = false
		}
		if _, ok := parseNumber(s); !ok {
			allNum = false
		}
	}
	kind := String
	switch {
	case !seen:
		kind = Number // all missing: pandas reads float64 NaN
	case allBool:
		kind = Bool
	case allNum:
		kind = Number
	}
	c := newColumn(name, kind, len(cells))
	for i, s := range cells {
		if naText[s] {
			continue
		}
		switch kind {
		case Bool:
			if boolText[s] {
				c.Num[i] = 1
			} else {
				c.Num[i] = 0
			}
		case Number:
			c.Num[i], _ = parseNumber(s)
		default:
			c.Str[i], c.Null[i] = s, false
		}
	}
	return c
}

// parseNumber reads a CSV cell as a finite number.
func parseNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// ParseXLSX reads the first sheet of an Excel workbook, its first row the
// header. A cell's own type decides: a number is a number (a date an ISO
// 8601 string), true/false a bool, text a string, and a column whose cells
// disagree is string.
func ParseXLSX(raw []byte) (*Table, error) {
	if len(raw) == 0 {
		return nil, errors.New("the XLSX file is empty")
	}
	wb, err := xlsx.OpenBinary(raw)
	if err != nil {
		return nil, fmt.Errorf("could not read the XLSX file: %w", err)
	}
	if len(wb.Sheets) == 0 || len(wb.Sheets[0].Rows) == 0 {
		return nil, errors.New("the XLSX file's first sheet is empty")
	}
	sheet := wb.Sheets[0]
	var header []string
	for i, c := range sheet.Rows[0].Cells {
		h := strings.TrimSpace(c.String())
		if h == "" {
			h = fmt.Sprintf("Unnamed: %d", i)
		}
		header = append(header, h)
	}
	if err := checkUnique(header); err != nil {
		return nil, err
	}
	values := make([][]any, len(header))
	for _, row := range sheet.Rows[1:] {
		if row == nil {
			continue
		}
		for i := range header {
			var v any
			if i < len(row.Cells) {
				v = cellValue(row.Cells[i], wb.Date1904)
			}
			values[i] = append(values[i], v)
		}
	}
	t := &Table{}
	if len(values) > 0 {
		t.Rows = len(values[0])
	}
	for i, name := range header {
		t.Columns = append(t.Columns, columnOf(name, values[i]))
	}
	return t, nil
}

// cellValue is one Excel cell as a JSON-like value, nil when empty.
func cellValue(c *xlsx.Cell, date1904 bool) any {
	if c == nil {
		return nil
	}
	switch c.Type() {
	case xlsx.CellTypeBool:
		return c.Bool()
	case xlsx.CellTypeNumeric:
		if c.IsTime() {
			if t, err := c.GetTime(date1904); err == nil {
				return t.UTC().Format(time.RFC3339)
			}
		}
		if f, err := c.Float(); err == nil {
			return f
		}
	case xlsx.CellTypeError:
		return nil
	}
	s := c.String()
	if s == "" {
		return nil
	}
	return s
}
