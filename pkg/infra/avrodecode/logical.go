package avrodecode

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/hamba/avro/v2"
)

// Duration is Avro's `duration` logical type: a fixed(12) holding months, days
// and milliseconds as three little-endian uint32.
//
// It is deliberately not a time.Duration. A month is not a fixed length of
// time, so collapsing the three components into nanoseconds would require
// inventing a calendar the record does not carry.
type Duration struct {
	Months       uint32
	Days         uint32
	Milliseconds uint32
}

// epochDate is the zero point for the `date` logical type.
var epochDate = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// applyLogical reinterprets an already-decoded primitive according to the
// logical type annotating it.
//
// Two rules keep a bad annotation from corrupting good data. An *unrecognised*
// logical type falls through to the underlying value: the annotation is
// advisory, and refusing the record would break a topic that is otherwise
// perfectly readable. A *recognised* logical type on the wrong underlying
// primitive also falls through, because reinterpreting a string as an epoch
// produces a garbage date out of valid bytes — worse than leaving it alone.
func (d *decoder) applyLogical(ls avro.LogicalSchema, v any, raw []byte) (any, error) {
	if ls == nil {
		return v, nil
	}

	switch ls.Type() {
	case avro.Date, avro.TimeMillis, avro.TimeMicros,
		avro.TimestampMillis, avro.LocalTimestampMillis,
		avro.TimestampMicros, avro.LocalTimestampMicros:
		return applyTemporal(ls.Type(), v), nil

	case avro.UUID:
		// Already a string. Validating it against RFC 4122 is schema
		// validation's job, not the decoder's.
		return v, nil

	case avro.Decimal:
		dec, ok := ls.(*avro.DecimalLogicalSchema)
		if !ok || raw == nil {
			return v, nil
		}
		return d.decimal(raw, dec)

	case avro.Duration:
		if len(raw) != 12 {
			return v, nil
		}
		return Duration{
			Months:       binary.LittleEndian.Uint32(raw[0:4]),
			Days:         binary.LittleEndian.Uint32(raw[4:8]),
			Milliseconds: binary.LittleEndian.Uint32(raw[8:12]),
		}, nil

	default:
		return v, nil
	}
}

// applyTemporal converts the integer logical types that denote a point or a
// span of time.
//
// A value on the wrong underlying primitive is returned untouched: asInt64
// fails, and reinterpreting a string as an epoch would turn valid bytes into a
// garbage date.
func applyTemporal(lt avro.LogicalType, v any) any {
	n, ok := asInt64(v)
	if !ok {
		return v
	}

	switch lt {
	case avro.Date:
		return epochDate.AddDate(0, 0, int(n))
	case avro.TimeMillis:
		return time.Duration(n) * time.Millisecond
	case avro.TimeMicros:
		return time.Duration(n) * time.Microsecond

	// local-timestamp carries no zone. It is returned in UTC so the wall-clock
	// reading is preserved exactly; shifting it by any offset would change the
	// value the record states.
	case avro.TimestampMillis, avro.LocalTimestampMillis:
		return time.UnixMilli(n).UTC()
	case avro.TimestampMicros, avro.LocalTimestampMicros:
		return time.UnixMicro(n).UTC()
	default:
		return v
	}
}

// asInt64 widens the integer primitives a logical type may sit on. Anything
// else means the annotation does not belong here.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

// maxDecimalBytes returns the most unscaled bytes a decimal of this precision
// can legitimately need.
//
// This bound is the point of the function. big.Int.String is superlinear in
// the digit count, so an unscaled value limited only by MaxBytes (16 MiB by
// default) is a CPU-exhaustion primitive made entirely of valid bytes. The
// schema's precision is operator-supplied and states how large the value can
// be, which makes it the right bound rather than an arbitrary constant.
//
// digits × log2(10) bits, plus a sign bit, rounded up to whole bytes.
func maxDecimalBytes(precision int) int {
	if precision <= 0 {
		// Precision is required by the spec; a schema without one gets a
		// generous but finite allowance rather than MaxBytes.
		return 64
	}
	bits := float64(precision)*math.Log2(10) + 1
	return int(math.Ceil(bits/8)) + 1
}

// decimal renders an unscaled two's-complement big-endian integer as an exact
// decimal string.
//
// A string rather than a float or a *big.Rat because the scale is part of the
// value: 1.10 and 1.1 are different to a NUMERIC column, and only the string
// keeps that distinction. It is also what SQL drivers accept for NUMERIC
// without a lossy conversion on the way.
func (d *decoder) decimal(raw []byte, ls *avro.DecimalLogicalSchema) (any, error) {
	precision := ls.Precision()
	if limit := maxDecimalBytes(precision); len(raw) > limit {
		return nil, fmt.Errorf(
			"%w: decimal has %d unscaled bytes, but precision %d needs at most %d",
			ErrLimitExceeded, len(raw), precision, limit)
	}

	// Two's-complement big-endian, per the Avro spec.
	n := new(big.Int).SetBytes(raw)
	if len(raw) > 0 && raw[0]&0x80 != 0 {
		// Negative: subtract 2^(8*len).
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(len(raw))*8))
	}

	return formatDecimal(n, ls.Scale()), nil
}

// formatDecimal places the decimal point, padding with leading zeros when the
// value has fewer digits than the scale.
func formatDecimal(n *big.Int, scale int) string {
	if scale <= 0 {
		return n.String()
	}

	neg := n.Sign() < 0
	digits := new(big.Int).Abs(n).String()

	// 1 at scale 4 is 0.0001, so the digits need padding before splitting.
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}

	split := len(digits) - scale
	out := digits[:split] + "." + digits[split:]
	if neg {
		out = "-" + out
	}
	return out
}
