package message

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
)

// SampleIsCDC reports whether ToMap will serialise this sample as a CDC event,
// which is what decides whether the data map doubles as the after-image. It
// mirrors the one condition ToMap uses: a non-empty operation.
func SampleIsCDC(data map[string]any) bool {
	for _, key := range []string{"operation", "Operation", "op", "Op"} {
		if v, ok := data[key]; ok && v != nil && fmt.Sprintf("%v", v) != "" {
			return true
		}
	}
	return false
}

// PopulateFromMap builds a message from a ToMap()-shaped sample -- the shape
// the workflow editor holds, sends to the preview endpoint and stores as a
// source sample.
//
// It lives here, rather than beside either of its callers, because it is the
// single answer to "what would the engine see for this sample". The editor's
// SQL query builder resolved query variables against the sample map itself
// while the preview endpoint resolved them against a message built from it, and
// the two disagree about the CDC envelope: ToMap re-nests the after-image under
// "after", and this function unwraps it back into the root, which is where the
// engine's Data() has it. So `{{.after.payload}}` returned rows in the builder
// and bound NULL in the node that ran the same text. One definition, two
// readers -- see TestTheBuilderBindsWhatTheEngineBinds.
func PopulateFromMap(msg hermod.Message, data map[string]any) {
	dm, isDefault := msg.(*DefaultMessage)
	p := samplePopulator{
		msg:              msg,
		dm:               dm,
		isDefault:        isDefault,
		keepSystemFields: !SampleIsCDC(data),
	}

	for k, v := range data {
		if v == nil {
			continue
		}
		if p.applyReservedKey(k, v) {
			continue
		}
		// Anything else is a row column.
		msg.SetData(k, v)
	}
}

// samplePopulator holds the decisions PopulateFromMap makes once, so each key
// is handled by a small function instead of one long switch.
type samplePopulator struct {
	msg       hermod.Message
	dm        *DefaultMessage
	isDefault bool

	// keepSystemFields decides whether id/operation/table/schema also land in
	// the data map.
	//
	// A sample carrying an operation becomes a CDC message, and ToMap
	// serialises one by marshalling the whole data map as the after-image.
	// Copying the envelope's own fields into that map therefore did two bad
	// things: every system field appeared twice in the previewed message --
	// once at the root, once inside "after" -- and the copy raced the
	// after-image itself, because both write the same key and Go randomises map
	// iteration order. A row with a column called "table", "id", "operation" or
	// "schema" kept the envelope's value in roughly three previews out of four,
	// so the operator mapped downstream nodes against a value the row does not
	// have.
	//
	// The copy was never needed. evaluator.GetMsgValByPath exposes operation,
	// op, table, schema and id as virtual fields resolved from the message
	// itself, and deliberately lets a real data column of the same name outrank
	// them. Leaving these out of the data map is what gives that rule something
	// to resolve against.
	//
	// Non-CDC samples are untouched: ToMap merges their data into the root,
	// there is no after-image to duplicate into, and dropping the copy would
	// change the previewed type of a field like id from a number to a string.
	keepSystemFields bool
}

// applyReservedKey handles the keys that describe the message rather than the
// row, reporting whether it consumed one.
func (p samplePopulator) applyReservedKey(key string, v any) bool {
	switch strings.ToLower(key) {
	case "id", "operation", "op", "table", "schema":
		p.applySystemField(key, v)
	case "metadata":
		p.applyMetadata(v)
	case "before":
		if _, _, raw := envelopeImage(v); len(raw) > 0 && p.isDefault {
			p.dm.SetBefore(raw)
		}
	case "after":
		p.applyAfter(v)
	default:
		return false
	}
	return true
}

func (p samplePopulator) applySystemField(key string, v any) {
	str := fmt.Sprintf("%v", v)
	if str == "" {
		return
	}
	lower := strings.ToLower(key)
	if p.isDefault {
		switch lower {
		case "id":
			p.dm.SetID(str)
		case "operation", "op":
			p.dm.SetOperation(hermod.Operation(str))
		case "table":
			p.dm.SetTable(str)
		case "schema":
			p.dm.SetSchema(str)
		}
	}
	if !p.keepSystemFields {
		return
	}
	// "id" is canonicalised because the message has exactly one; the others
	// keep the key as the sample spelled it.
	if lower == "id" {
		p.msg.SetData("id", v)
		return
	}
	p.msg.SetData(key, v)
}

func (p samplePopulator) applyMetadata(v any) {
	md, ok := v.(map[string]any)
	if !ok {
		return
	}
	for mk, mv := range md {
		if mv != nil {
			p.msg.SetMetadata(mk, fmt.Sprint(mv))
		}
	}
}

func (p samplePopulator) applyAfter(v any) {
	fields, isObject, raw := envelopeImage(v)
	switch {
	case isObject:
		// The engine's data map *is* the after-image, so the envelope is
		// unwrapped into the root here rather than kept as a nested key. That
		// is the whole reason this function exists.
		for ak, av := range fields {
			p.msg.SetData(ak, av)
		}
	case len(raw) > 0 && p.isDefault:
		// Not an object: a payload that is not JSON at all. Keep the bytes.
		// Note SetAfter clears the data map, which is why an empty object must
		// not reach this arm.
		p.dm.SetAfter(raw)
	}
}

// envelopeImage reads one side of a CDC envelope, whatever shape it arrived in,
// and reports both the decoded object and the original bytes.
//
// There are three shapes because there are three producers. ToMap writes
// json.RawMessage (see jsonRawOrWrapped); JSON decoding on the way in from the
// editor turns that same value into a map[string]any; and a source whose body
// was never JSON leaves a string. Only the middle one used to be understood, so
// an in-process ToMap -> PopulateFromMap round trip dropped every row column
// without an error -- invisible over HTTP, which decodes to a map first.
//
// isObject distinguishes "decoded to an object, possibly empty" from "could not
// be decoded". An empty object must not fall through to SetAfter: that clears
// the data map, so an envelope of {} would discard fields set by keys the
// caller happened to visit earlier.
func envelopeImage(v any) (fields map[string]any, isObject bool, raw []byte) {
	switch t := v.(type) {
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return t, true, nil
		}
		return t, true, b
	case json.RawMessage:
		raw = t
	case []byte:
		raw = t
	case string:
		raw = []byte(t)
	default:
		return nil, false, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return nil, false, raw
	}
	return decoded, true, raw
}
