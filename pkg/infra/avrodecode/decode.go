// Package avrodecode decodes Avro binary records into map[string]any.
//
// # Why this exists rather than a dependency
//
// github.com/hamba/avro/v2 — which Hermod already uses to parse schemas and to
// encode — carries three unfixed denial-of-service advisories in its decoder:
// GO-2026-5046 / 5047 / 5048, CVE-2026-46385. Its array and map decoders loop
// over an attacker-controlled block count without re-checking the reader's
// error state inside the loop body, so a record declaring up to math.MaxInt64
// elements followed by a truncated payload pins a CPU core until the process is
// killed. The module is archived upstream; no fixed release exists.
//
// The schema *parser* and the *encoder* are not implicated, and this package
// keeps using them. Only the decoder is reimplemented, because only the decoder
// is the problem.
//
// # Why this shape is not vulnerable to the same bug
//
// Structural, not a patch. The decoder reads from a byte slice that is already
// fully in memory — a Kafka record, not a stream — so there is no deferred
// reader error state that a loop body can forget to consult. Running out of
// bytes is an immediate hard error at the point of the read, and every loop
// returns on the first error rather than continuing to the declared count.
//
// On top of that, three bounds apply to every input (see Limits):
//
//   - No allocation is ever sized from a declared count. A collection claiming
//     a billion elements allocates a small capacity and grows only as elements
//     actually decode.
//   - Collection sizes are bounded cumulatively across blocks, not per block,
//     so splitting a huge collection into many small ones does not evade them.
//   - Nesting is depth-limited, so recursion terminates on a bound rather than
//     on the goroutine stack.
//
// The zero-width case is worth naming because it defeats the obvious defence: a
// count cannot exceed the bytes remaining *unless the item type is null*, which
// encodes to nothing. An array of null is therefore arithmetically consistent
// with an empty body at any count, and only the absolute bound stops it.
package avrodecode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/hamba/avro/v2"
)

// Limits bound what a single record may cost. The defaults are generous for
// real data and still refuse anything shaped like an attack.
type Limits struct {
	// MaxBytes bounds one string, bytes or fixed value.
	MaxBytes int

	// MaxCollection bounds the total number of elements in one array or map,
	// summed across all of its blocks.
	MaxCollection int

	// MaxDepth bounds schema nesting during decode.
	MaxDepth int

	// MaxValues bounds the total number of values decoded from one record, so
	// that many individually-legal collections cannot add up to an unbounded
	// amount of work.
	MaxValues int
}

// DefaultLimits returns bounds suitable for ordinary records.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:      16 << 20, // 16 MiB — far above any sane field
		MaxCollection: 1 << 20,  // 1,048,576 elements
		MaxDepth:      64,       // real schemas nest single digits deep
		MaxValues:     4 << 20,  // 4,194,304 values per record
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxCollection <= 0 {
		l.MaxCollection = d.MaxCollection
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxValues <= 0 {
		l.MaxValues = d.MaxValues
	}
	return l
}

// ErrMalformed reports input that is not a valid encoding of the schema.
var ErrMalformed = errors.New("malformed Avro record")

// ErrLimitExceeded reports input that is well-formed but exceeds a bound.
// It is separate from ErrMalformed because the operator responses differ: a
// malformed record is a poison message, where a limit may be a limit set too
// low for legitimate data.
var ErrLimitExceeded = errors.New("record exceeds a decode limit")

// Decode decodes one Avro binary record against schema.
//
// The top-level schema must be a record: that is what a Confluent subject
// registers, and it is what gives the result its field names.
//
// Every byte of data must be consumed. Trailing bytes mean the record does not
// match this schema, which is a silent-corruption risk rather than a crash, and
// so is reported rather than ignored.
func Decode(schema avro.Schema, data []byte, lim Limits) (map[string]any, error) {
	if schema == nil {
		return nil, fmt.Errorf("%w: schema is nil", ErrMalformed)
	}
	rec, ok := deref(schema).(*avro.RecordSchema)
	if !ok {
		return nil, fmt.Errorf(
			"%w: top-level schema is %q, want a record", ErrMalformed, schema.Type())
	}

	d := &decoder{buf: data, lim: lim.withDefaults()}

	v, err := d.record(rec, 0)
	if err != nil {
		return nil, err
	}
	if d.pos != len(d.buf) {
		return nil, fmt.Errorf("%w: %d trailing bytes after a complete record",
			ErrMalformed, len(d.buf)-d.pos)
	}
	return v, nil
}

type decoder struct {
	buf []byte
	pos int
	lim Limits

	// values counts every scalar and collection element decoded, so that the
	// total work for one record is bounded even when no single collection is.
	values int
}

// deref resolves a named-type reference to the schema it names.
func deref(s avro.Schema) avro.Schema {
	if ref, ok := s.(*avro.RefSchema); ok {
		return ref.Schema()
	}
	return s
}

func (d *decoder) charge() error {
	d.values++
	if d.values > d.lim.MaxValues {
		return fmt.Errorf("%w: more than %d values in one record",
			ErrLimitExceeded, d.lim.MaxValues)
	}
	return nil
}

// take returns the next n bytes, or an error if fewer remain.
//
// This is the single choke point that makes truncation an immediate failure.
// n is validated against the remaining length before any slicing, so a
// declared length cannot index past the buffer.
func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("%w: negative length %d", ErrMalformed, n)
	}
	if n > len(d.buf)-d.pos {
		return nil, fmt.Errorf("%w: need %d bytes, %d remain",
			ErrMalformed, n, len(d.buf)-d.pos)
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}

func (d *decoder) remaining() int { return len(d.buf) - d.pos }

// varint reads one zigzag-encoded variable-length integer.
//
// Avro encodes int and long identically. Ten bytes is the maximum a 64-bit
// value can occupy; an eleventh means the input is not a varint at all, and
// refusing it here stops the read walking the whole buffer.
func (d *decoder) varint() (int64, error) {
	var u uint64
	var shift uint
	for i := 0; i < 10; i++ {
		if d.pos >= len(d.buf) {
			return 0, fmt.Errorf("%w: varint truncated after %d bytes", ErrMalformed, i)
		}
		b := d.buf[d.pos]
		d.pos++
		u |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			// Zigzag: the low bit is the sign.
			return int64(u>>1) ^ -int64(u&1), nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("%w: varint longer than 10 bytes", ErrMalformed)
}

func (d *decoder) value(s avro.Schema, depth int) (any, error) {
	if depth > d.lim.MaxDepth {
		return nil, fmt.Errorf("%w: nesting depth exceeds the %d level limit",
			ErrLimitExceeded, d.lim.MaxDepth)
	}
	if err := d.charge(); err != nil {
		return nil, err
	}

	s = deref(s)

	switch t := s.(type) {
	case *avro.NullSchema:
		return nil, nil

	case *avro.PrimitiveSchema:
		return d.primitiveLogical(t)

	case *avro.RecordSchema:
		return d.record(t, depth+1)

	case *avro.ArraySchema:
		return d.array(t, depth+1)

	case *avro.MapSchema:
		return d.mapValue(t, depth+1)

	case *avro.UnionSchema:
		return d.union(t, depth+1)

	case *avro.EnumSchema:
		return d.enum(t)

	case *avro.FixedSchema:
		return d.fixed(t)

	default:
		return nil, fmt.Errorf("%w: unsupported schema type %q", ErrMalformed, s.Type())
	}
}

// primitiveLogical decodes a primitive and then reinterprets it if a logical
// type annotates it.
//
// The raw bytes are kept for `decimal`, whose value is the unscaled integer
// they spell rather than the string or []byte the primitive decode produced.
func (d *decoder) primitiveLogical(s *avro.PrimitiveSchema) (any, error) {
	v, err := d.primitive(s)
	if err != nil {
		return nil, err
	}
	ls := s.Logical()
	if ls == nil {
		return v, nil
	}
	var raw []byte
	if b, ok := v.([]byte); ok {
		raw = b
	}
	return d.applyLogical(ls, v, raw)
}

func (d *decoder) primitive(s *avro.PrimitiveSchema) (any, error) {
	switch s.Type() {
	case avro.Null:
		return nil, nil

	case avro.Boolean:
		b, err := d.take(1)
		if err != nil {
			return nil, err
		}
		switch b[0] {
		case 0x00:
			return false, nil
		case 0x01:
			return true, nil
		default:
			return nil, fmt.Errorf("%w: boolean byte %#02x is neither 0 nor 1", ErrMalformed, b[0])
		}

	case avro.Int:
		v, err := d.varint()
		if err != nil {
			return nil, err
		}
		if v > math.MaxInt32 || v < math.MinInt32 {
			return nil, fmt.Errorf("%w: %d does not fit an Avro int", ErrMalformed, v)
		}
		return int32(v), nil

	case avro.Long:
		return d.varint()

	case avro.Float:
		b, err := d.take(4)
		if err != nil {
			return nil, err
		}
		return math.Float32frombits(binary.LittleEndian.Uint32(b)), nil

	case avro.Double:
		b, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil

	default:
		return d.primitiveBytes(s)
	}
}

// primitiveBytes handles the length-prefixed primitives. It is split from
// primitive only to keep each switch within the cyclomatic budget.
func (d *decoder) primitiveBytes(s *avro.PrimitiveSchema) (any, error) {
	switch s.Type() {
	case avro.Bytes:
		b, err := d.lengthPrefixed()
		if err != nil {
			return nil, err
		}
		return b, nil

	case avro.String:
		b, err := d.lengthPrefixed()
		if err != nil {
			return nil, err
		}
		return string(b), nil

	default:
		return nil, fmt.Errorf("%w: unsupported primitive %q", ErrMalformed, s.Type())
	}
}

// lengthPrefixed reads a varint length followed by that many bytes.
//
// The length is checked against both the configured bound and the bytes
// actually remaining, before any allocation. The returned slice is copied: it
// must not alias the caller's buffer, which may be a pooled read buffer that is
// reused for the next record.
func (d *decoder) lengthPrefixed() ([]byte, error) {
	n, err := d.varint()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, fmt.Errorf("%w: negative length %d", ErrMalformed, n)
	}
	if n > int64(d.lim.MaxBytes) {
		return nil, fmt.Errorf("%w: value of %d bytes exceeds the %d byte limit",
			ErrLimitExceeded, n, d.lim.MaxBytes)
	}
	// Redundant with take's own check, but it keeps the error specific: a
	// length larger than the whole buffer is a malformed record, not a limit.
	if n > int64(d.remaining()) {
		return nil, fmt.Errorf("%w: value declares %d bytes, %d remain",
			ErrMalformed, n, d.remaining())
	}
	b, err := d.take(int(n))
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (d *decoder) record(s *avro.RecordSchema, depth int) (map[string]any, error) {
	if depth > d.lim.MaxDepth {
		return nil, fmt.Errorf("%w: nesting depth exceeds the %d level limit",
			ErrLimitExceeded, d.lim.MaxDepth)
	}

	fields := s.Fields()
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		v, err := d.value(f.Type(), depth+1)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", f.Name(), err)
		}
		out[f.Name()] = v
	}
	return out, nil
}

// blockCount reads one block header and returns the element count.
//
// A negative count is legal Avro and means abs(count) elements preceded by the
// block's byte size, which lets a reader skip it. math.MinInt64 is the case
// that makes naive negation wrong — negating it overflows back to itself — so
// it is refused explicitly rather than becoming a huge positive count.
func (d *decoder) blockCount() (int64, error) {
	n, err := d.varint()
	if err != nil {
		return 0, err
	}
	if n == math.MinInt64 {
		return 0, fmt.Errorf("%w: block count math.MinInt64 has no positive magnitude", ErrMalformed)
	}
	if n < 0 {
		n = -n
		// The byte size follows a negative count. It is read and discarded:
		// the elements are decoded rather than skipped.
		if _, err := d.varint(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// checkCount rejects a declared element count before anything is allocated.
//
// zeroWidth says whether one element can encode to zero bytes, which is true
// only for null. When it is false, a count exceeding the bytes remaining is
// impossible and is caught here rather than after that many failed reads.
func (d *decoder) checkCount(n, total int64, zeroWidth bool) error {
	if n < 0 {
		return fmt.Errorf("%w: negative element count %d", ErrMalformed, n)
	}
	if total+n > int64(d.lim.MaxCollection) {
		return fmt.Errorf("%w: collection of %d elements exceeds the %d element limit",
			ErrLimitExceeded, total+n, d.lim.MaxCollection)
	}
	if !zeroWidth && n > int64(d.remaining()) {
		return fmt.Errorf("%w: block declares %d elements but only %d bytes remain",
			ErrMalformed, n, d.remaining())
	}
	return nil
}

// isZeroWidth reports whether a value of s can encode to no bytes at all.
func isZeroWidth(s avro.Schema) bool {
	switch t := deref(s).(type) {
	case *avro.NullSchema:
		return true
	case *avro.PrimitiveSchema:
		return t.Type() == avro.Null
	case *avro.RecordSchema:
		// A record of no fields, or only zero-width ones, encodes to nothing.
		for _, f := range t.Fields() {
			if !isZeroWidth(f.Type()) {
				return false
			}
		}
		return true
	case *avro.FixedSchema:
		return t.Size() == 0
	default:
		return false
	}
}

// prealloc caps the capacity taken from a declared count.
//
// Nothing is ever sized from an attacker's number: the collection grows as
// elements actually decode, so a count of a billion costs a small slice rather
// than a billion-element allocation.
func prealloc(n int64) int {
	const maxPrealloc = 64
	if n < maxPrealloc {
		return int(n)
	}
	return maxPrealloc
}

func (d *decoder) array(s *avro.ArraySchema, depth int) (any, error) {
	items := s.Items()
	zw := isZeroWidth(items)

	out := []any{}
	var total int64

	for {
		n, err := d.blockCount()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
		if err := d.checkCount(n, total, zw); err != nil {
			return nil, err
		}
		if cap(out) == 0 {
			out = make([]any, 0, prealloc(n))
		}
		for i := int64(0); i < n; i++ {
			v, err := d.value(items, depth)
			if err != nil {
				return nil, fmt.Errorf("array element %d: %w", total+i, err)
			}
			out = append(out, v)
		}
		total += n
	}
}

func (d *decoder) mapValue(s *avro.MapSchema, depth int) (any, error) {
	values := s.Values()
	// A map entry always carries a length-prefixed key, so an entry is never
	// zero-width even when its value is.
	const zeroWidth = false

	out := map[string]any{}
	var total int64

	for {
		n, err := d.blockCount()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
		if err := d.checkCount(n, total, zeroWidth); err != nil {
			return nil, err
		}
		for i := int64(0); i < n; i++ {
			kb, err := d.lengthPrefixed()
			if err != nil {
				return nil, fmt.Errorf("map key %d: %w", total+i, err)
			}
			v, err := d.value(values, depth)
			if err != nil {
				return nil, fmt.Errorf("map key %q: %w", kb, err)
			}
			out[string(kb)] = v
		}
		total += n
	}
}

// union decodes the branch its index selects.
//
// The result is the branch's value, unwrapped. hamba's encoder represents a
// union as a single-entry map naming the branch; carrying that shape into a
// decoded record would push an encoding detail into every downstream consumer.
func (d *decoder) union(s *avro.UnionSchema, depth int) (any, error) {
	idx, err := d.varint()
	if err != nil {
		return nil, err
	}
	types := s.Types()
	if idx < 0 || idx >= int64(len(types)) {
		return nil, fmt.Errorf("%w: union branch %d, but the union has %d branches",
			ErrMalformed, idx, len(types))
	}
	return d.value(types[idx], depth)
}

func (d *decoder) enum(s *avro.EnumSchema) (any, error) {
	idx, err := d.varint()
	if err != nil {
		return nil, err
	}
	symbols := s.Symbols()
	if idx < 0 || idx >= int64(len(symbols)) {
		return nil, fmt.Errorf("%w: enum symbol %d, but %q has %d symbols",
			ErrMalformed, idx, s.FullName(), len(symbols))
	}
	return symbols[idx], nil
}

// fixed reads exactly the schema's declared size. The size comes from the
// schema, which is operator-controlled, not from the record.
func (d *decoder) fixed(s *avro.FixedSchema) (any, error) {
	n := s.Size()
	if n < 0 || n > d.lim.MaxBytes {
		return nil, fmt.Errorf("%w: fixed %q declares %d bytes, over the %d byte limit",
			ErrLimitExceeded, s.FullName(), n, d.lim.MaxBytes)
	}
	b, err := d.take(n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(b))
	copy(out, b)

	if ls := s.Logical(); ls != nil {
		return d.applyLogical(ls, out, out)
	}
	return out, nil
}
