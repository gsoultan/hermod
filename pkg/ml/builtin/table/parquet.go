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
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/common"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/types"
)

// ParseParquet reads a Parquet file, as the worker's pyarrow does: a boolean
// column is bool; an integer, floating-point or decimal one number; a date,
// time or timestamp one string in ISO 8601, as Python's isoformat writes it;
// and text (or bytes, as UTF-8) string. A list, map or nested group has no
// one value per row, so a file holding one is refused.
func ParseParquet(raw []byte) (*Table, error) {
	if len(raw) == 0 {
		return nil, errors.New("the Parquet file is empty")
	}
	pf := buffer.NewBufferFileFromBytes(raw)
	pr, err := openParquet(pf)
	if err != nil {
		return nil, fmt.Errorf("could not read the Parquet file: %w", err)
	}
	defer pr.ReadStop()

	sh := pr.SchemaHandler
	n := pr.GetNumRows()
	t := &Table{Rows: int(n)}
	var header []string
	for _, inPath := range sh.ValueColumns {
		path := common.StrToPath(inPath)
		name := exName(sh.InPathToExPath[inPath], path)
		// path[0] is the schema's root: a flat column sits right below it.
		if len(path) != 2 {
			return nil, fmt.Errorf("column %q is a list, map or nested group; flatten it into plain columns first", name)
		}
		el := sh.SchemaElements[sh.MapIndex[inPath]]
		values, _, _, err := pr.ReadColumnByPath(inPath, n)
		if err != nil {
			return nil, fmt.Errorf("could not read column %q of the Parquet file: %w", name, err)
		}
		if int64(len(values)) != n {
			return nil, fmt.Errorf("column %q of the Parquet file holds %d values for %d rows", name, len(values), n)
		}
		vals := make([]any, len(values))
		for i, v := range values {
			vals[i] = parquetValue(el, v)
		}
		header = append(header, name)
		t.Columns = append(t.Columns, columnOf(name, vals))
	}
	if err := checkUnique(header); err != nil {
		return nil, err
	}
	return t, nil
}

// openParquet opens the file's footer. The reader panics on some files that
// are not Parquet at all; that is an unreadable file, not a crash.
func openParquet(pf *buffer.BufferFile) (pr *reader.ParquetReader, err error) {
	defer func() {
		if r := recover(); r != nil {
			pr, err = nil, fmt.Errorf("%v", r)
		}
	}()
	return reader.NewParquetColumnReader(pf, 1)
}

// exName is a column's name as the file gives it.
func exName(ex string, in []string) string {
	if p := common.StrToPath(ex); len(p) > 1 {
		return p[1]
	}
	if len(in) > 1 {
		return in[1]
	}
	return in[len(in)-1]
}

// parquetValue turns one value the reader gives (nil, bool, int32, int64,
// float32, float64, or a string of bytes) into a JSON-like one, by the
// column's logical type.
func parquetValue(el *parquet.SchemaElement, v any) any {
	if v == nil {
		return nil
	}
	if t, ok := temporalValue(el, v); ok {
		return t
	}
	if d, ok := decimalValue(el, v); ok {
		return d
	}
	if i, ok := v.(int64); ok && isUint64(el) {
		return float64(uint64(i))
	}
	switch x := v.(type) {
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	}
	// bool, float64, and text or bytes as a string.
	return v
}

// logicalType is the column's logical type, empty when it has none.
func logicalType(el *parquet.SchemaElement) *parquet.LogicalType {
	if lt := el.GetLogicalType(); lt != nil {
		return lt
	}
	return &parquet.LogicalType{}
}

// temporalValue is a date, time or timestamp value as text; false for a
// column of another type, or a value that is not one.
func temporalValue(el *parquet.SchemaElement, v any) (any, bool) {
	lt := logicalType(el)
	switch {
	case lt.DATE != nil:
		return dateValue(v)
	case lt.TIMESTAMP != nil:
		return isoTimestamp(v, unitOf(lt.TIMESTAMP.Unit), lt.TIMESTAMP.IsAdjustedToUTC), true
	case lt.TIME != nil:
		return isoTime(v, unitOf(lt.TIME.Unit)), true
	case el.GetType() == parquet.Type_INT96:
		if s, ok := v.(string); ok && len(s) == 12 {
			return isoTime64(types.INT96ToTime(s), true), true
		}
		return nil, false
	}
	if el.ConvertedType == nil {
		return nil, false
	}
	switch *el.ConvertedType {
	case parquet.ConvertedType_DATE:
		return dateValue(v)
	case parquet.ConvertedType_TIMESTAMP_MILLIS:
		return isoTimestamp(v, time.Millisecond, true), true
	case parquet.ConvertedType_TIMESTAMP_MICROS:
		return isoTimestamp(v, time.Microsecond, true), true
	case parquet.ConvertedType_TIME_MILLIS:
		return isoTime(v, time.Millisecond), true
	case parquet.ConvertedType_TIME_MICROS:
		return isoTime(v, time.Microsecond), true
	}
	return nil, false
}

// dateValue is a DATE (days since 1970-01-01) as 2006-01-02.
func dateValue(v any) (any, bool) {
	d, ok := v.(int32)
	if !ok {
		return nil, false
	}
	return time.Unix(int64(d)*86400, 0).UTC().Format(time.DateOnly), true
}

// decimalValue is a DECIMAL value as a number; false for another column.
func decimalValue(el *parquet.SchemaElement, v any) (any, bool) {
	if lt := logicalType(el); lt.DECIMAL != nil {
		return decimal(v, lt.DECIMAL.Scale), true
	}
	if el.ConvertedType != nil && *el.ConvertedType == parquet.ConvertedType_DECIMAL {
		return decimal(v, el.GetScale()), true
	}
	return nil, false
}

// isUint64 says whether the column holds unsigned 64-bit integers, which the
// reader hands over as int64.
func isUint64(el *parquet.SchemaElement) bool {
	if in := logicalType(el).INTEGER; in != nil {
		return !in.IsSigned && in.BitWidth == 64
	}
	return el.ConvertedType != nil && *el.ConvertedType == parquet.ConvertedType_UINT_64
}

func unitOf(u *parquet.TimeUnit) time.Duration {
	switch {
	case u == nil:
		return time.Microsecond
	case u.MILLIS != nil:
		return time.Millisecond
	case u.NANOS != nil:
		return time.Nanosecond
	}
	return time.Microsecond
}

func ticks(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int32:
		return int64(x), true
	}
	return 0, false
}

func isoTimestamp(v any, unit time.Duration, utc bool) any {
	n, ok := ticks(v)
	if !ok {
		return v
	}
	return isoTime64(time.Unix(0, 0).Add(time.Duration(n)*unit).UTC(), utc)
}

// isoTime64 is t as Python's isoformat writes it: microseconds when there
// are any (nanoseconds when there are, as pandas does), and +00:00 for a time
// in UTC.
func isoTime64(t time.Time, utc bool) string {
	s := t.Format("2006-01-02T15:04:05") + fraction(t.Nanosecond())
	if utc {
		s += "+00:00"
	}
	return s
}

func isoTime(v any, unit time.Duration) any {
	n, ok := ticks(v)
	if !ok {
		return v
	}
	t := time.Unix(0, 0).Add(time.Duration(n) * unit).UTC()
	return t.Format("15:04:05") + fraction(t.Nanosecond())
}

func fraction(ns int) string {
	switch {
	case ns == 0:
		return ""
	case ns%1000 == 0:
		return fmt.Sprintf(".%06d", ns/1000)
	}
	return fmt.Sprintf(".%09d", ns)
}

// decimal is a decimal's value as a float: an int32 or int64 unscaled value,
// or big-endian two's complement bytes.
func decimal(v any, scale int32) any {
	var u *big.Int
	switch x := v.(type) {
	case int32:
		u = big.NewInt(int64(x))
	case int64:
		u = big.NewInt(x)
	case string:
		u = new(big.Int).SetBytes([]byte(x))
		if len(x) > 0 && x[0]&0x80 != 0 {
			u.Sub(u, new(big.Int).Lsh(big.NewInt(1), uint(8*len(x))))
		}
	default:
		return v
	}
	f, _ := new(big.Float).SetInt(u).Float64()
	return f / math.Pow10(int(scale))
}
