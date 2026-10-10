package structure

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("parse_field", &ParseField{})
}

// defaultParseBytes is how much text parse_field reads when the node sets no
// limit, and maxParseBytes the most it may be set to. The text is upstream
// data and the parsed value is held in memory for the rest of the record's
// journey, typically several times its size.
const (
	defaultParseBytes = 1 << 20
	maxParseBytes     = 16 << 20
)

// ParseField parses a text field into structure.
//
// Config:
//   - field: the field holding the text (a string, or bytes). Required.
//   - format: "json" (default), "csv", "xml" or "kv".
//   - targetField: where the result goes, default the field itself.
//   - maxBytes: longest text accepted, default 1 MiB, at most 16 MiB.
//   - csv: delimiter (default ",", "tab" for a tab), headers (a list or
//     comma-separated names) or hasHeader (the first line names the
//     columns). The result is always an array: of objects when there are
//     headers, of arrays of values when there are none.
//   - kv: pairDelimiter (default any run of spaces) and kvSeparator (default
//     "="). Double-quoted values may hold the delimiter. Values stay text.
//   - xml: elements become objects keyed by name, attributes "@name", text
//     beside children "#text", repeated elements arrays. A document type
//     declaration is refused, and with it every entity definition, so entity
//     expansion cannot happen; nesting is limited to 64 levels.
type ParseField struct{}

func (p *ParseField) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	field := configString(config, "field", "")
	if field == "" {
		return msg, errors.New("parse_field: choose the field to parse")
	}
	text, err := fieldText(msg, field, min(configInt(config, "maxBytes", defaultParseBytes), maxParseBytes))
	if err != nil {
		return msg, fmt.Errorf("parse_field: %w", err)
	}
	var out any
	switch format := strings.ToLower(configString(config, "format", "json")); format {
	case "json":
		err = json.Unmarshal(text, &out)
		if err != nil {
			err = fmt.Errorf("invalid json: %w", err)
		}
	case "csv":
		out, err = parseCSV(text, config)
	case "xml":
		out, err = parseXML(text)
	case "kv":
		out = parseKV(string(text), config)
	default:
		err = fmt.Errorf("format %q is not one of json, csv, xml or kv", format)
	}
	if err != nil {
		return msg, fmt.Errorf("parse_field: %s: %w", field, err)
	}
	msg.SetData(configString(config, "targetField", field), out)
	return msg, nil
}

// fieldText reads the field's text, refusing anything over limit bytes.
func fieldText(msg hermod.Message, field string, limit int) ([]byte, error) {
	var text []byte
	// The raw reader, because the normalising one turns bytes into base64.
	switch v := evaluator.GetMsgRawValByPath(msg, field).(type) {
	case nil:
		return nil, fmt.Errorf("the record has no field %q", field)
	case string:
		text = []byte(v)
	case []byte:
		text = v
	default:
		return nil, fmt.Errorf("field %q holds %T, not text", field, v)
	}
	if len(text) > limit {
		return nil, fmt.Errorf("field %q holds %d bytes, above the %d this node parses", field, len(text), limit)
	}
	return text, nil
}

func parseCSV(text []byte, config map[string]any) (any, error) {
	r := csv.NewReader(bytes.NewReader(text))
	switch d := configString(config, "delimiter", ","); d {
	case "tab", `\t`:
		r.Comma = '\t'
	default:
		r.Comma = []rune(d)[0]
	}
	r.TrimLeadingSpace = true
	headers := core.GetConfigStringSlice(config, "headers")
	if len(headers) == 0 {
		headers = core.SplitComma(core.GetConfigString(config, "headers"))
	}
	if len(headers) > 0 {
		r.FieldsPerRecord = len(headers)
	}
	rows := []any{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invalid csv: %w", err)
		}
		if len(headers) == 0 && evaluator.ToBool(config["hasHeader"]) {
			headers = rec
			r.FieldsPerRecord = len(rec)
			continue
		}
		rows = append(rows, csvRow(rec, headers))
	}
}

func csvRow(rec, headers []string) any {
	if len(headers) == 0 {
		vals := make([]any, len(rec))
		for i, v := range rec {
			vals[i] = v
		}
		return vals
	}
	row := make(map[string]any, len(headers))
	for i, h := range headers {
		row[strings.TrimSpace(h)] = rec[i]
	}
	return row
}

// parseKV reads key=value pairs. A pair with no separator is a key with an
// empty value.
func parseKV(text string, config map[string]any) map[string]any {
	pairSep, _ := config["pairDelimiter"].(string)
	kvSep := configString(config, "kvSeparator", "=")
	out := map[string]any{}
	for _, tok := range splitOutsideQuotes(text, pairSep) {
		k, v, _ := strings.Cut(tok, kvSep)
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil && strings.HasPrefix(v, `"`) {
			v = uq
		}
		out[k] = v
	}
	return out
}

// splitOutsideQuotes splits on sep, or on runs of white space when sep is
// empty or " ", ignoring separators inside double quotes.
func splitOutsideQuotes(s, sep string) []string {
	space := sep == "" || sep == " "
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			inQuote = !inQuote
		case !inQuote && space && unicode.IsSpace(rune(c)):
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		case !inQuote && !space && strings.HasPrefix(s[i:], sep):
			parts = append(parts, cur.String())
			cur.Reset()
			i += len(sep) - 1
			continue
		}
		cur.WriteByte(c)
	}
	return append(parts, cur.String())
}
