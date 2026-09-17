package smtp

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"maps"
	"math"
	"text/template"
	"time"

	// tzdata embeds the IANA time zone database so that a template asking for
	// "Asia/Jakarta" resolves on an image that carries no /usr/share/zoneinfo.
	// Without it the lookup fails at send time, on the machine the operator
	// cannot open a shell on.
	_ "time/tzdata"

	"github.com/gsoultan/gsmail"
)

// Timestamp is a message value that Hermod recognised as a date or a time.
//
// It is a string, and it holds the exact text the message carried: a template
// that writes {{.created_at}} renders what it always rendered, and eq, len,
// slice and printf keep treating the column as text. What it adds are the
// time methods, so a column can be formatted where it is used:
//
//	{{ .created_at.Format "2006-01-02" }}
//	{{ .start_at.In "Asia/Jakarta" }}
//	{{ .start_at.In (time.LoadLocation "Asia/Jakarta") }}
type Timestamp string

// Time parses the text back, which reaches the whole time.Time method set:
// {{ .created_at.Time.Year }}.
func (t Timestamp) Time() (time.Time, error) {
	parsed, _, ok := parseTimestamp(string(t))
	if !ok {
		return time.Time{}, fmt.Errorf("%q is not a date or time this sink can read", string(t))
	}
	return parsed, nil
}

// Format renders the timestamp with a Go reference layout, where the parts of
// "2006-01-02 15:04:05" name themselves: {{ .created_at.Format "2006-01-02" }}
// writes 2026-12-01.
func (t Timestamp) Format(layout string) (string, error) {
	parsed, err := t.Time()
	if err != nil {
		return "", err
	}
	return parsed.Format(layout), nil
}

// In moves the timestamp to another zone, named ("Asia/Jakarta") or already
// loaded ((time.LoadLocation "Asia/Jakarta")).
//
// The result prints in the layout the column arrived in, so a row that reads
// 2026-12-01T09:30:00Z reads 2026-12-01T16:30:00+07:00; chain .Format to
// choose a different one.
func (t Timestamp) In(zone any) (Timestamp, error) {
	loc, err := toLocation(zone)
	if err != nil {
		return "", err
	}
	parsed, layout, ok := parseTimestamp(string(t))
	if !ok {
		return "", fmt.Errorf("%q is not a date or time this sink can read", string(t))
	}
	return Timestamp(parsed.In(loc).Format(layout)), nil
}

// UTC is In "UTC".
func (t Timestamp) UTC() (Timestamp, error) { return t.In(time.UTC) }

// Unix is the timestamp in seconds since the epoch.
func (t Timestamp) Unix() (int64, error) {
	parsed, err := t.Time()
	if err != nil {
		return 0, err
	}
	return parsed.Unix(), nil
}

// Sub is the interval from another value, which may be a text column, a
// driver's time or an epoch number: {{ .ended_at.Sub .started_at }}.
func (t Timestamp) Sub(v any) (time.Duration, error) {
	parsed, err := t.Time()
	if err != nil {
		return 0, err
	}
	return TimeValue{parsed}.Sub(v)
}

// Before reports whether this value is earlier than another.
func (t Timestamp) Before(v any) (bool, error) {
	parsed, err := t.Time()
	if err != nil {
		return false, err
	}
	return TimeValue{parsed}.Before(v)
}

// After reports whether this value is later than another.
func (t Timestamp) After(v any) (bool, error) {
	parsed, err := t.Time()
	if err != nil {
		return false, err
	}
	return TimeValue{parsed}.After(v)
}

// Equal reports whether two values name the same instant, whatever zone each
// of them is written in.
func (t Timestamp) Equal(v any) (bool, error) {
	parsed, err := t.Time()
	if err != nil {
		return false, err
	}
	return TimeValue{parsed}.Equal(v)
}

// TimeValue is a driver's own time.Time on its way into a template.
//
// A column reaches a sink as text on the CDC path — PostgreSQL's logical
// stream sends text — and as a time.Time on the query path, and one template
// has to read both. TimeValue prints exactly as the time.Time it wraps and
// keeps its whole method set; what it replaces are the methods that take a
// zone or another time, which here also accept a zone by name and a column
// that arrived as text.
type TimeValue struct {
	time.Time
}

// In moves the time to another zone, named ("Asia/Jakarta") or already loaded
// ((time.LoadLocation "Asia/Jakarta")).
func (t TimeValue) In(zone any) (TimeValue, error) {
	loc, err := toLocation(zone)
	if err != nil {
		return TimeValue{}, err
	}
	return TimeValue{t.Time.In(loc)}, nil
}

// UTC keeps the chain in this type, so {{ .ts.UTC.In "Asia/Jakarta" }} reads.
func (t TimeValue) UTC() TimeValue { return TimeValue{t.Time.UTC()} }

// Local is UTC, in the worker's own zone.
func (t TimeValue) Local() TimeValue { return TimeValue{t.Time.Local()} }

// Sub is the interval from another value, which may be a text column.
func (t TimeValue) Sub(v any) (time.Duration, error) {
	other, err := toTime(v)
	if err != nil {
		return 0, err
	}
	return t.Time.Sub(other), nil
}

// Before reports whether this value is earlier than another.
func (t TimeValue) Before(v any) (bool, error) {
	other, err := toTime(v)
	if err != nil {
		return false, err
	}
	return t.Time.Before(other), nil
}

// After reports whether this value is later than another.
func (t TimeValue) After(v any) (bool, error) {
	other, err := toTime(v)
	if err != nil {
		return false, err
	}
	return t.Time.After(other), nil
}

// Equal reports whether two values name the same instant, whatever zone each
// of them is written in.
func (t TimeValue) Equal(v any) (bool, error) {
	other, err := toTime(v)
	if err != nil {
		return false, err
	}
	return t.Time.Equal(other), nil
}

// timestampLayouts are the layouts a column's text is tried against, most
// specific first. Every one of them starts with a YYYY-MM-DD date, which is
// what looksLikeTimestamp screens for before any parsing happens.
var timestampLayouts = []string{
	time.RFC3339Nano,                          // 2026-12-01T09:30:00.123456789Z07:00
	"2006-01-02T15:04:05.999999999",           // the same without a zone
	"2006-01-02 15:04:05.999999999Z07:00",     // PostgreSQL timestamptz
	"2006-01-02 15:04:05.999999999Z07",        // PostgreSQL's short offset, +07
	"2006-01-02 15:04:05.999999999 -0700 MST", // Go's own time.Time.String()
	"2006-01-02 15:04:05.999999999",           // PostgreSQL timestamp, MySQL DATETIME
	"2006-01-02",                              // a date column
}

// looksLikeTimestamp screens a value before any parsing, so a row of forty
// text columns does not pay for a layout sweep on each of them.
func looksLikeTimestamp(s string) bool {
	const isoDate = len("2006-01-02")
	if len(s) < isoDate || len(s) > 40 {
		return false
	}
	if s[4] != '-' || s[7] != '-' {
		return false
	}
	for _, i := range [...]int{0, 1, 2, 3, 5, 6, 8, 9} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseTimestamp reports whether a value reads as a time, and with which
// layout. The layout is what In renders the converted value back through.
func parseTimestamp(s string) (time.Time, string, bool) {
	if !looksLikeTimestamp(s) {
		return time.Time{}, "", false
	}
	for _, layout := range timestampLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, layout, true
		}
	}
	return time.Time{}, "", false
}

// maxTimestampDepth bounds the walk. Message data is decoded JSON, which
// cannot be cyclic, but the sink should not take a caller's word for that.
const maxTimestampDepth = 16

// withTimestamps returns the template data with every value that reads as a
// date or time replaced by a Timestamp.
func withTimestamps(data map[string]any) map[string]any {
	wrapped, _ := wrapMap(data, 0)
	return wrapped
}

// wrapMap rebuilds a map only when one of its values changed. The nested maps
// belong to the message, and the message is shared with every other sink on
// the workflow, so rewriting one in place would hand them a value they never
// stored.
func wrapMap(m map[string]any, depth int) (map[string]any, bool) {
	if depth > maxTimestampDepth {
		return m, false
	}
	var out map[string]any
	for k, v := range m {
		nv, changed := wrapValue(v, depth)
		if !changed {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(m))
			maps.Copy(out, m)
		}
		out[k] = nv
	}
	if out == nil {
		return m, false
	}
	return out, true
}

func wrapSlice(s []any, depth int) ([]any, bool) {
	if depth > maxTimestampDepth {
		return s, false
	}
	var out []any
	for i, v := range s {
		nv, changed := wrapValue(v, depth)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]any, len(s))
			copy(out, s)
		}
		out[i] = nv
	}
	if out == nil {
		return s, false
	}
	return out, true
}

func wrapValue(v any, depth int) (any, bool) {
	switch val := v.(type) {
	case string:
		if _, _, ok := parseTimestamp(val); ok {
			return Timestamp(val), true
		}
	case time.Time:
		return TimeValue{val}, true
	case *time.Time:
		// A nil stays nil: a template that prints an absent column should keep
		// printing what it printed.
		if val != nil {
			return TimeValue{*val}, true
		}
	case map[string]any:
		return wrapMap(val, depth+1)
	case []any:
		return wrapSlice(val, depth+1)
	}
	return v, false
}

// templateFuncs is the function set every SMTP template is rendered with:
// the subject, each recipient, the idempotency key and an inline body.
var templateFuncs = template.FuncMap{
	"time":       func() timeNS { return timeNS{} },
	"now":        time.Now,
	"date":       formatDate,
	"dateInZone": formatDateInZone,
	"toDate":     toTime,
}

// timeNS is the value behind the template name "time". A function name cannot
// contain a dot, so {{ time.LoadLocation "Asia/Jakarta" }} can only be a
// namespace value whose methods are the helpers.
type timeNS struct{}

// LoadLocation loads a zone from the IANA database by name.
func (timeNS) LoadLocation(name string) (*time.Location, error) { return time.LoadLocation(name) }

// Now is the send time.
func (timeNS) Now() time.Time { return time.Now() }

// Parse reads a value the sink does not recognise on its own, by naming the
// layout: {{ (time.Parse "02/01/2006" .due).Format "2006-01-02" }}.
func (timeNS) Parse(layout, value string) (time.Time, error) { return time.Parse(layout, value) }

// UTC is the UTC zone, for {{ .start_at.In time.UTC }}.
func (timeNS) UTC() *time.Location { return time.UTC }

// Local is the worker's own zone.
func (timeNS) Local() *time.Location { return time.Local }

// Unix reads an epoch-seconds column as a time.
func (timeNS) Unix(v any) (time.Time, error) {
	sec, err := toFloat(v)
	if err != nil {
		return time.Time{}, err
	}
	return epochSeconds(sec), nil
}

// UnixMilli reads an epoch-milliseconds column as a time.
func (timeNS) UnixMilli(v any) (time.Time, error) {
	ms, err := toFloat(v)
	if err != nil {
		return time.Time{}, err
	}
	return epochSeconds(ms / 1000), nil
}

// formatDate renders any value as a time. The value comes last so the call
// also reads as a pipeline: {{ .created_at | date "2006-01-02" }}.
func formatDate(layout string, v any) (string, error) {
	parsed, err := toTime(v)
	if err != nil {
		return "", err
	}
	return parsed.Format(layout), nil
}

// formatDateInZone is formatDate in a named zone:
// {{ dateInZone "2006-01-02 15:04" "Asia/Jakarta" .created_at }}.
func formatDateInZone(layout, zone string, v any) (string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", err
	}
	parsed, err := toTime(v)
	if err != nil {
		return "", err
	}
	return parsed.In(loc).Format(layout), nil
}

// toTime reads a column as a time, whatever shape the source handed it over
// in: a driver's time.Time, text, or a number of seconds since the epoch.
func toTime(v any) (time.Time, error) {
	switch val := v.(type) {
	case Timestamp:
		return val.Time()
	case TimeValue:
		return val.Time, nil
	case time.Time:
		return val, nil
	case *time.Time:
		if val == nil {
			return time.Time{}, errors.New("cannot read a time from a nil value")
		}
		return *val, nil
	case string:
		return Timestamp(val).Time()
	case []byte:
		return Timestamp(val).Time()
	}
	sec, err := toFloat(v)
	if err != nil {
		return time.Time{}, err
	}
	return epochSeconds(sec), nil
}

// toLocation accepts a zone by name or already loaded, so .In reads the same
// whether the template says "Asia/Jakarta" or (time.LoadLocation "Asia/Jakarta").
func toLocation(zone any) (*time.Location, error) {
	switch val := zone.(type) {
	case *time.Location:
		if val == nil {
			return nil, errors.New("cannot read a time zone from a nil value")
		}
		return val, nil
	case string:
		return time.LoadLocation(val)
	}
	return nil, fmt.Errorf("cannot read a time zone from %T", zone)
}

// toFloat accepts every numeric shape a message can carry. JSON numbers reach
// a sink as float64; a driver's own columns arrive as sized integers.
func toFloat(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case uint:
		return float64(val), nil
	case uint32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	}
	return 0, fmt.Errorf("cannot read a number from %T", v)
}

// epochSeconds keeps the fractional part of a float epoch, which is how a
// JSON number carries milliseconds.
func epochSeconds(sec float64) time.Time {
	whole, frac := math.Modf(sec)
	return time.Unix(int64(whole), int64(frac*float64(time.Second)))
}

// setInlineBody renders the configured template into the email.
//
// gsmail's Email.SetBody would do this, but it parses with no function map,
// and the function map is the point: {{ time.LoadLocation "Asia/Jakarta" }}
// is a parse error without it. The HTML sniff, the contextual escaping and
// the Outlook conversion stay gsmail's, so a body that used no function
// renders exactly as it did before.
func setInlineBody(email *gsmail.Email, tmplStr string, data any) error {
	var body []byte
	var err error
	if gsmail.IsHTML([]byte(tmplStr)) {
		body, err = renderHTMLTemplate(tmplStr, data)
		if err == nil && email.OutlookCompatible {
			body = gsmail.ToOutlookHTML(body)
		}
	} else {
		body, err = renderTextTemplate(tmplStr, data)
	}
	if err != nil {
		return fmt.Errorf("set body: %w", err)
	}
	email.Body = body
	return nil
}

func renderHTMLTemplate(tmplStr string, data any) ([]byte, error) {
	// html/template.FuncMap is an alias for the text/template one, so the same
	// set covers both renderers.
	tmpl, err := htmltemplate.New("email").Funcs(templateFuncs).Parse(tmplStr) //nolint:gosec // G708: see renderTextTemplate
	if err != nil {
		return nil, fmt.Errorf("parse html template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute html template: %w", err)
	}
	return buf.Bytes(), nil
}

// renderTextTemplate renders a plaintext body.
//
// gosec reads the template text as tainted because it arrives from storage.
// It is sink configuration, written by an editor, and rendering it is the
// feature; message data only ever reaches the template as data, and the URL a
// remote template is fetched from is never itself templated, so no row can
// choose the text that gets parsed.
func renderTextTemplate(tmplStr string, data any) ([]byte, error) {
	tmpl, err := template.New("email").Funcs(templateFuncs).Parse(tmplStr) //nolint:gosec // G708: see the note on this function
	if err != nil {
		return nil, fmt.Errorf("parse text template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute text template: %w", err)
	}
	return buf.Bytes(), nil
}
