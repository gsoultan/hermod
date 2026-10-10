package onnxscore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/encoding/protowire"
)

// The protobuf decoding of the part of onnx.proto (onnx-ml.proto, IR version
// 10) a tabular model uses, written against the wire format directly so
// that no generated code or vendored .proto is needed. Field numbers are
// onnx.proto's. A field that would change what the graph computes and that
// this package does not handle — model-local functions, sparse
// initializers, subgraph or type attributes, tensors stored outside the
// file — makes the model unsupported; anything else unknown (doc strings,
// metadata) is skipped.

type modelProto struct {
	opsets map[string]int64
	graph  graphProto
}

type graphProto struct {
	nodes   []nodeProto
	inits   []tensorProto
	inputs  []valueInfo
	outputs []valueInfo
}

type nodeProto struct {
	inputs, outputs  []string
	name, op, domain string
	attrs            []attrProto
}

// attrProto keeps every value field; the attribute's type says which one
// holds it, and the operators read the one they expect.
type attrProto struct {
	name    string
	typ     int32
	f       float32
	i       int64
	s       []byte
	t       *tensorProto
	floats  []float32
	ints    []int64
	strings [][]byte
}

// AttributeProto.AttributeType values this package reads.
const (
	attrFloat   = 1
	attrInt     = 2
	attrString  = 3
	attrTensor  = 4
	attrFloats  = 6
	attrInts    = 7
	attrStrings = 8
)

type tensorProto struct {
	name     string
	dims     []int64
	dataType int32
	floats   []float32
	int64s   []int64
	strings  [][]byte
	raw      []byte
	hasRaw   bool
}

// valueInfo is a graph input or output: its name, and its element type when
// it is a tensor. tensor is false for a sequence, map or anything else.
type valueInfo struct {
	name     string
	tensor   bool
	elemType int32
}

var errTruncated = errors.New("the model file is truncated or not an ONNX model")

// fields walks one message, calling fn for every field with its number, wire
// type and raw value: for a varint the number itself in v, for fixed32 and
// fixed64 the bits in v, and for a length-delimited field its bytes in b.
func fields(buf []byte, fn func(num protowire.Number, typ protowire.Type, v uint64, b []byte) error) error {
	for len(buf) > 0 {
		num, typ, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return errTruncated
		}
		buf = buf[n:]
		var (
			v uint64
			b []byte
		)
		switch typ {
		case protowire.VarintType:
			v, n = protowire.ConsumeVarint(buf)
		case protowire.Fixed32Type:
			var x uint32
			x, n = protowire.ConsumeFixed32(buf)
			v = uint64(x)
		case protowire.Fixed64Type:
			v, n = protowire.ConsumeFixed64(buf)
		case protowire.BytesType:
			b, n = protowire.ConsumeBytes(buf)
		default:
			n = protowire.ConsumeFieldValue(num, typ, buf)
		}
		if n < 0 {
			return errTruncated
		}
		buf = buf[n:]
		if err := fn(num, typ, v, b); err != nil {
			return err
		}
	}
	return nil
}

func decodeModel(buf []byte) (modelProto, error) {
	m := modelProto{opsets: map[string]int64{}}
	var hasGraph bool
	err := fields(buf, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		switch num {
		case 7: // graph
			if typ != protowire.BytesType {
				return errTruncated
			}
			g, err := decodeGraph(b)
			if err != nil {
				return err
			}
			m.graph, hasGraph = g, true
		case 8: // opset_import
			if typ != protowire.BytesType {
				return errTruncated
			}
			domain, version, err := decodeOpset(b)
			if err != nil {
				return err
			}
			m.opsets[domain] = version
		case 25: // functions
			return unsupported("the model defines its own functions")
		case 20: // training_info
			return unsupported("the model carries training information")
		}
		return nil
	})
	if err != nil {
		return modelProto{}, err
	}
	if !hasGraph {
		return modelProto{}, errTruncated
	}
	return m, nil
}

func decodeOpset(buf []byte) (string, int64, error) {
	var (
		domain  string
		version int64
	)
	err := fields(buf, func(num protowire.Number, typ protowire.Type, v uint64, b []byte) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			domain = string(b)
		case num == 2 && typ == protowire.VarintType:
			version = int64(v)
		}
		return nil
	})
	return domain, version, err
}

func decodeGraph(buf []byte) (graphProto, error) {
	var g graphProto
	err := fields(buf, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		if num == 15 { // sparse_initializer
			return unsupported("the graph has sparse initializers")
		}
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1: // node
			n, err := decodeNode(b)
			if err != nil {
				return err
			}
			g.nodes = append(g.nodes, n)
		case 5: // initializer
			t, err := decodeTensor(b)
			if err != nil {
				return err
			}
			g.inits = append(g.inits, t)
		case 11, 12: // input, output
			vi, err := decodeValueInfo(b)
			if err != nil {
				return err
			}
			if num == 11 {
				g.inputs = append(g.inputs, vi)
			} else {
				g.outputs = append(g.outputs, vi)
			}
		}
		return nil
	})
	return g, err
}

func decodeNode(buf []byte) (nodeProto, error) {
	var n nodeProto
	err := fields(buf, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1:
			n.inputs = append(n.inputs, string(b))
		case 2:
			n.outputs = append(n.outputs, string(b))
		case 3:
			n.name = string(b)
		case 4:
			n.op = string(b)
		case 7:
			n.domain = string(b)
		case 5:
			a, err := decodeAttr(b)
			if err != nil {
				return err
			}
			n.attrs = append(n.attrs, a)
		}
		return nil
	})
	return n, err
}

func decodeAttr(buf []byte) (attrProto, error) {
	var a attrProto
	err := fields(buf, func(num protowire.Number, typ protowire.Type, v uint64, b []byte) error {
		var err error
		switch num {
		case 1:
			a.name = string(b)
		case 20:
			a.typ = int32(v)
		case 2:
			if typ != protowire.Fixed32Type {
				return errTruncated
			}
			a.f = math.Float32frombits(uint32(v))
		case 3:
			a.i = int64(v)
		case 4:
			a.s = b
		case 5:
			var t tensorProto
			t, err = decodeTensor(b)
			a.t = &t
		case 7:
			a.floats, err = appendFloats(a.floats, typ, v, b)
		case 8:
			a.ints, err = appendInts(a.ints, typ, v, b)
		case 9:
			a.strings = append(a.strings, b)
		case 6, 10, 11, 14, 15, 21, 22, 23:
			// graphs, tensor lists, types, sparse tensors and references to
			// a function's attributes: nothing a tabular export holds.
			return unsupported("attribute %q holds a value of a kind this package does not read", a.name)
		}
		return err
	})
	return a, err
}

func decodeTensor(buf []byte) (tensorProto, error) {
	var t tensorProto
	err := fields(buf, func(num protowire.Number, typ protowire.Type, v uint64, b []byte) error {
		var err error
		switch num {
		case 1:
			t.dims, err = appendInts(t.dims, typ, v, b)
		case 2:
			t.dataType = int32(v)
		case 3:
			return unsupported("tensor is stored in segments")
		case 4:
			t.floats, err = appendFloats(t.floats, typ, v, b)
		case 6:
			t.strings = append(t.strings, b)
		case 7:
			t.int64s, err = appendInts(t.int64s, typ, v, b)
		case 8:
			t.name = string(b)
		case 9:
			t.raw, t.hasRaw = b, true
		case 13, 14:
			if num == 14 && v == 0 {
				return nil // data_location DEFAULT
			}
			return unsupported("tensor %q is stored outside the model file", t.name)
		}
		return err
	})
	return t, err
}

func decodeValueInfo(buf []byte) (valueInfo, error) {
	var vi valueInfo
	err := fields(buf, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			vi.name = string(b)
		case num == 2 && typ == protowire.BytesType: // TypeProto
			return fields(b, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
				if num != 1 || typ != protowire.BytesType { // tensor_type
					return nil
				}
				vi.tensor = true
				return fields(b, func(num protowire.Number, typ protowire.Type, v uint64, _ []byte) error {
					if num == 1 && typ == protowire.VarintType {
						vi.elemType = int32(v)
					}
					return nil
				})
			})
		}
		return nil
	})
	return vi, err
}

// appendFloats reads a repeated float field, packed or not.
func appendFloats(dst []float32, typ protowire.Type, v uint64, b []byte) ([]float32, error) {
	switch typ {
	case protowire.Fixed32Type:
		return append(dst, math.Float32frombits(uint32(v))), nil
	case protowire.BytesType:
		if len(b)%4 != 0 {
			return nil, errTruncated
		}
		for i := 0; i < len(b); i += 4 {
			dst = append(dst, math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
		}
		return dst, nil
	}
	return nil, errTruncated
}

// appendInts reads a repeated int32 or int64 field, packed or not. Both are
// varints on the wire, two's complement in 64 bits.
func appendInts(dst []int64, typ protowire.Type, v uint64, b []byte) ([]int64, error) {
	switch typ {
	case protowire.VarintType:
		return append(dst, int64(v)), nil
	case protowire.BytesType:
		for len(b) > 0 {
			x, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, errTruncated
			}
			dst = append(dst, int64(x))
			b = b[n:]
		}
		return dst, nil
	}
	return nil, errTruncated
}

// tensor turns an initializer or a tensor attribute into a Tensor. Float,
// int64 and string tensors are supported; any other element type (int32 and
// double data are skipped while decoding) is refused here.
func (tp *tensorProto) tensor() (*Tensor, error) {
	shape := make([]int, len(tp.dims))
	for i, d := range tp.dims {
		if d < 0 || d > math.MaxInt32 {
			return nil, fmt.Errorf("tensor %q has dimension %d", tp.name, d)
		}
		shape[i] = int(d)
	}
	t := &Tensor{Shape: shape}
	var err error
	switch tp.dataType {
	case int32(Float):
		t.Type, t.Floats = Float, tp.floats
		if tp.hasRaw {
			if t.Floats, err = rawFloats(tp.raw); err != nil {
				return nil, err
			}
		}
	case int32(Int64):
		t.Type, t.Ints = Int64, tp.int64s
		if tp.hasRaw {
			if t.Ints, err = rawInts(tp.raw); err != nil {
				return nil, err
			}
		}
	case int32(String):
		t.Type = String
		t.Strings = make([]string, len(tp.strings))
		for i, s := range tp.strings {
			t.Strings[i] = string(s)
		}
	default:
		return nil, unsupported("tensor %q has element type %d", tp.name, tp.dataType)
	}
	if !t.valid() {
		return nil, fmt.Errorf("tensor %q holds %d values for shape %v", tp.name, t.count(), t.Shape)
	}
	return t, nil
}

// rawFloats reads little-endian float32 raw_data.
func rawFloats(raw []byte) ([]float32, error) {
	if len(raw)%4 != 0 {
		return nil, errTruncated
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out, nil
}

// rawInts reads little-endian int64 raw_data.
func rawInts(raw []byte) ([]int64, error) {
	if len(raw)%8 != 0 {
		return nil, errTruncated
	}
	out := make([]int64, len(raw)/8)
	for i := range out {
		out[i] = int64(binary.LittleEndian.Uint64(raw[8*i:]))
	}
	return out, nil
}
