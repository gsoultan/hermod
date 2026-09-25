package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// deadline is the instant every case below names, written in whichever shape
// that case is about.
var deadline = time.Date(2026, 9, 22, 7, 26, 7, 173529602, time.UTC)

// A date conversion whose configured layout is narrower than the value still
// has to read the value. The editor's Date Format field placeholds "2006-01-02",
// so a date-only layout against an RFC3339 column is the common configuration,
// and it failed the whole message with
//
//	field "Deadline": parsing time "2026-09-22T07:26:07.173529602Z": extra text: "T07:26:07.173529602Z"
func TestDataConversion_Date_ReadsATimestampUnderADateOnlyLayout(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("tasks")
	msg.SetAfter([]byte(`{"id":"1","DateDeadline":"2026-09-22T07:26:07.173529602Z"}`))

	tr := &DataConversionTransformer{}
	out, err := tr.Transform(context.Background(), msg, map[string]any{
		"conversions": []any{
			map[string]any{"field": "DateDeadline", "targetType": "date", "format": "2006-01-02"},
		},
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	got, ok := out.Data()["DateDeadline"].(time.Time)
	if !ok {
		t.Fatalf("DateDeadline = %#v, want a time.Time", out.Data()["DateDeadline"])
	}
	// The instant is preserved, not truncated to the layout: a deadline at
	// 07:26 must not silently become midnight.
	if !got.Equal(deadline) {
		t.Errorf("DateDeadline = %s, want %s", got.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
}

// The same value reaches a node in several shapes depending on which source
// path produced it, and the configured layout describes at most one of them.
func TestToDate_Shapes(t *testing.T) {
	cases := []struct {
		name    string
		format  string
		val     any
		want    time.Time
		wantErr bool
	}{
		{"the configured layout matches", "2006-01-02", "2024-03-01", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), false},
		{"RFC3339 text under a date-only layout", "2006-01-02", "2026-09-22T07:26:07.173529602Z", deadline, false},
		{"RFC3339 text, no layout configured", "", "2026-09-22T07:26:07.173529602Z", deadline, false},
		{"a driver's time.Time", "2006-01-02", deadline, deadline, false},
		{"a pointer to a driver's time.Time", "", &deadline, deadline, false},
		{"text handed over as bytes", "", []byte("2026-09-22T07:26:07.173529602Z"), deadline, false},
		{"PostgreSQL timestamptz text", "2006-01-02", "2026-09-22 14:26:07.173529602+07", deadline, false},
		{"PostgreSQL timestamp text", "", "2026-09-22 07:26:07.173529602", deadline, false},
		{"a date column", "", "2026-09-22", time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC), false},
		// Nothing below reads as a date without guessing which number is the
		// month, and a guess here is a silently wrong date rather than an error
		// an operator can see.
		{"a non-ISO shape the layout does not match", "2006-01-02", "09/22/2026", time.Time{}, true},
		{"empty text", "", "", time.Time{}, true},
		{"not a time at all", "", "hello", time.Time{}, true},
		{"nil", "", nil, time.Time{}, true},
	}

	tr := &DataConversionTransformer{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tr.toDate(tc.val, tc.format, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toDate(%#v, %q) = %#v, want an error", tc.val, tc.format, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("toDate(%#v, %q): %v", tc.val, tc.format, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("toDate(%#v, %q) = %s, want %s", tc.val, tc.format,
					got.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
			}
		})
	}
}

// A field read through the evaluator is JSON-normalised, so a time.Time that a
// query path put in the data map arrives at the node as RFC3339Nano text. Both
// shapes have to land on the same instant or a workflow's output depends on
// which source path produced the row.
func TestDataConversion_Date_QueryAndCDCShapesAgree(t *testing.T) {
	tr := &DataConversionTransformer{}
	config := map[string]any{
		"conversions": []any{
			map[string]any{"field": "deadline", "targetType": "date", "format": "2006-01-02"},
		},
	}

	fromDriver := message.AcquireMessage()
	fromDriver.SetOperation(hermod.OpCreate)
	fromDriver.SetTable("tasks")
	fromDriver.SetData("deadline", deadline)

	fromCDC := message.AcquireMessage()
	fromCDC.SetOperation(hermod.OpCreate)
	fromCDC.SetTable("tasks")
	fromCDC.SetData("deadline", "2026-09-22 14:26:07.173529602+07")

	for _, msg := range []hermod.Message{fromDriver, fromCDC} {
		out, err := tr.Transform(context.Background(), msg, config)
		if err != nil {
			t.Fatalf("Transform(%v): %v", evaluator.EvaluateField(msg, "deadline"), err)
		}
		got, ok := out.Data()["deadline"].(time.Time)
		if !ok {
			t.Fatalf("deadline = %#v, want a time.Time", out.Data()["deadline"])
		}
		if !got.Equal(deadline) {
			t.Errorf("deadline = %s, want %s", got.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
	}
}

// A date row writes text only when it names an Output Format.
//
// The row's only format used to be the one it *reads* with, and the editor
// labelled it "Date Format". An operator who set it to "02 January 2006" over
// 2026-09-18T04:30:57.333046Z got the timestamp straight back: the layout did
// not match the value, the ISO sweep read it instead, and the time.Time that came
// out renders as RFC3339 wherever the message is serialised. Nothing was wrong
// by the code's definition and everything was wrong by the operator's.
func TestDataConversion_Date_WritesTheOutputFormat(t *testing.T) {
	// The row exactly as the editor stores it: JSON, so it reaches the node as
	// []any of map[string]any, never as a typed struct.
	var cfg map[string]any
	if err := json.Unmarshal([]byte(`{"conversions":[
		{"field":"created_at","targetType":"date","outputFormat":"02 January 2006"}
	]}`), &cfg); err != nil {
		t.Fatal(err)
	}

	msg := message.AcquireMessage()
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("orders")
	msg.SetAfter([]byte(`{"id":"1","created_at":"2026-09-18T04:30:57.333046Z"}`))

	out, err := (&DataConversionTransformer{}).Transform(context.Background(), msg, cfg)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["created_at"]; got != "18 September 2026" {
		t.Errorf("created_at = %#v, want \"18 September 2026\"", got)
	}
}

func TestDataConversion_Date_OutputFormats(t *testing.T) {
	cases := []struct {
		name         string
		val          any
		format       string
		outputFormat string
		want         string
	}{
		{"an ISO date", "2026-09-18T04:30:57.333046Z", "", "2006-01-02", "2026-09-18"},
		{"day first, with the time", "2026-09-18T04:30:57.333046Z", "", "02/01/2006 15:04", "18/09/2026 04:30"},
		{"twelve-hour clock", "2026-09-18T16:30:57Z", "", "03:04 PM", "04:30 PM"},
		{"with the weekday", "2026-09-18T04:30:57Z", "", "Monday, 02 January 2006", "Friday, 18 September 2026"},
		// Rendered in the value's own zone, not converted to UTC and not to the
		// server's zone: the text says what the source said.
		{"keeps the value's own offset", "2026-09-18 23:30:00+07", "", "2006-01-02 15:04 Z07:00", "2026-09-18 23:30 +07:00"},
		{"a driver's time.Time", deadline, "", "02 Jan 2006 15:04", "22 Sep 2026 07:26"},
		// The two formats together reshape a value: read day-first text, write
		// ISO. This is the conversion an import from a spreadsheet needs.
		{"reads one shape and writes another", "18/09/2026", "02/01/2006", "2006-01-02", "2026-09-18"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertMulti(t, map[string]any{"d": tc.val}, map[string]any{
				"conversions": []any{map[string]any{
					"field": "d", "targetType": "date", "format": tc.format, "outputFormat": tc.outputFormat,
				}},
			})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got["d"] != tc.want {
				t.Errorf("d = %#v, want %q", got["d"], tc.want)
			}
		})
	}
}

// An Output Format that prints no part of a date writes the same text into
// every row. "DD MMMM YYYY" is the natural thing to type for anyone who knows
// any date library other than Go's, and Go prints it back verbatim -- every
// message would reach the sink carrying the literal string "DD MMMM YYYY".
//
// That is a fault in the node, not in a value, so it fails even under On Error
// "null": the operator's choice of what to do with a bad *value* would
// otherwise turn a typo into a column of nulls.
func TestDataConversion_Date_OutputFormatThatPrintsNoDateFailsTheNode(t *testing.T) {
	for _, layout := range []string{"DD MMMM YYYY", "dd/MM/yyyy"} {
		t.Run(layout, func(t *testing.T) {
			_, err := convertMulti(t, map[string]any{"d": "2026-09-18T04:30:57Z"}, map[string]any{
				"errorBehavior": "null",
				"conversions": []any{map[string]any{
					"field": "d", "targetType": "date", "outputFormat": layout,
				}},
			})
			if err == nil {
				t.Fatalf("output format %q was accepted; every row would be written as %q", layout, layout)
			}
			for _, want := range []string{`"d"`, layout, "2006"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// The editor keeps a row's keys when its target type changes, so a row that was
// a date can carry an outputFormat it no longer uses. Only a date row reads it.
func TestDataConversion_OutputFormatIsIgnoredOffADateRow(t *testing.T) {
	got, err := convertMulti(t, map[string]any{"qty": "7"}, map[string]any{
		"conversions": []any{map[string]any{
			"field": "qty", "targetType": "int", "outputFormat": "DD MMMM YYYY",
		}},
	})
	if err != nil {
		t.Fatalf("an int row failed on a date setting it does not use: %v", err)
	}
	if got["qty"] != int64(7) {
		t.Errorf("qty = %#v, want int64(7)", got["qty"])
	}
}

func TestDataConversion_Date_OutputFormat_PreparedAndUnpreparedAgree(t *testing.T) {
	cfg := map[string]any{
		"conversions": []any{
			map[string]any{"field": "d", "targetType": "date", "outputFormat": "02 January 2006"},
		},
	}
	in := map[string]any{"d": "2026-09-18T04:30:57.333046Z"}

	unprepared, err := convertMulti(t, in, cfg)
	if err != nil {
		t.Fatalf("unprepared: %v", err)
	}
	preparedCfg, err := (&DataConversionTransformer{}).Prepare(cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prepared, err := convertMulti(t, in, preparedCfg)
	if err != nil {
		t.Fatalf("prepared: %v", err)
	}
	if prepared["d"] != "18 September 2026" || unprepared["d"] != prepared["d"] {
		t.Errorf("prepared = %#v, unprepared = %#v, want both \"18 September 2026\"", prepared["d"], unprepared["d"])
	}
}

// uiDateFormatsPath is the editor's list of date formats an operator picks from
// instead of typing a Go layout.
const uiDateFormatsPath = "../../../../ui/src/components/workflow/Transformation/configs/data/dateFormat/dateFormatOptions.ts"

var (
	uiDateFormatRE      = regexp.MustCompile(`\{\s*layout:\s*'([^']+)',\s*example:\s*'([^']+)'`)
	uiExampleInstantRE  = regexp.MustCompile(`EXAMPLE_INSTANT\s*=\s*'([^']+)'`)
	uiNextExportRE      = regexp.MustCompile(`\nexport `)
	uiInputFormatsDecl  = "export const INPUT_DATE_FORMATS"
	uiOutputFormatsDecl = "export const OUTPUT_DATE_FORMATS"
)

// uiDateFormats returns the layout/example pairs declared by one of the
// editor's lists: everything from its declaration to the next export.
func uiDateFormats(t *testing.T, src, decl string) [][2]string {
	t.Helper()
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatalf("%s not found in %s", decl, uiDateFormatsPath)
	}
	section := src[start+len(decl):]
	if end := uiNextExportRE.FindStringIndex(section); end != nil {
		section = section[:end[0]]
	}
	var pairs [][2]string
	seen := map[string]bool{}
	for _, m := range uiDateFormatRE.FindAllStringSubmatch(section, -1) {
		if seen[m[1]] {
			// Mantine refuses to render a Select whose values repeat.
			t.Errorf("%s lists %q twice", decl, m[1])
		}
		seen[m[1]] = true
		pairs = append(pairs, [2]string{m[1], m[2]})
	}
	if len(pairs) == 0 {
		t.Fatalf("no formats found under %s; has the list been reformatted?", decl)
	}
	return pairs
}

// TestDataConversion_Date_EditorFormatsDoWhatTheyShow keeps the picker honest.
//
// The editor shows each format as the text it produces -- "18 September 2026",
// not "02 January 2006" -- because a Go layout is exactly the thing an operator
// mistypes. That makes every example a claim about what this node writes, and a
// hand-written claim drifts. Each one is checked here against the node itself.
func TestDataConversion_Date_EditorFormatsDoWhatTheyShow(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(uiDateFormatsPath))
	if err != nil {
		t.Fatalf("cannot read the editor's format list at %s: %v", uiDateFormatsPath, err)
	}
	src := string(raw)

	m := uiExampleInstantRE.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("EXAMPLE_INSTANT not found in the editor's format list")
	}
	instant, err := time.Parse(time.RFC3339, m[1])
	if err != nil {
		t.Fatalf("EXAMPLE_INSTANT %q is not RFC3339: %v", m[1], err)
	}

	convert := func(val any, row map[string]any) (any, error) {
		row["field"], row["targetType"] = "d", "date"
		got, err := convertMulti(t, map[string]any{"d": val}, map[string]any{"conversions": []any{row}})
		if err != nil {
			return nil, err
		}
		return got["d"], nil
	}

	for _, p := range uiDateFormats(t, src, uiOutputFormatsDecl) {
		layout, example := p[0], p[1]
		got, err := convert(instant.Format(time.RFC3339Nano), map[string]any{"outputFormat": layout})
		if err != nil {
			t.Errorf("output format %q: %v", layout, err)
			continue
		}
		if got != example {
			t.Errorf("output format %q writes %#v, but the editor shows %q", layout, got, example)
		}
	}

	for _, p := range uiDateFormats(t, src, uiInputFormatsDecl) {
		layout, example := p[0], p[1]
		want, err := time.Parse(layout, example)
		if err != nil {
			t.Errorf("input format %q cannot read its own example %q: %v", layout, example, err)
			continue
		}
		got, err := convert(example, map[string]any{"format": layout})
		if err != nil {
			t.Errorf("input format %q: the node cannot read %q: %v", layout, example, err)
			continue
		}
		if parsed, ok := got.(time.Time); !ok || !parsed.Equal(want) {
			t.Errorf("input format %q reads %q as %#v, want %s", layout, example, got, want)
		}
	}
}

// A date is written in the value's own zone unless the row names one, and
// Hermod's operators are rarely in UTC: 2026-09-18T20:00:00Z is already the
// 19th in Jakarta, so a date-only output format written in UTC is a day early
// there for seven hours of every day.
func TestDataConversion_Date_TimeZone_WritesTheDateInThatZone(t *testing.T) {
	cases := []struct {
		name string
		val  any
		zone string
		want string
	}{
		{"UTC text", "2026-09-18T20:00:00Z", "Asia/Jakarta", "19 September 2026 03:00"},
		{"text with its own offset", "2026-09-18 23:30:00+07", "America/New_York", "18 September 2026 12:30"},
		{"a driver's time.Time", time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC), "Asia/Jakarta", "19 September 2026 03:00"},
		{"UTC named as a zone", "2026-09-18 23:30:00+07", "UTC", "18 September 2026 16:30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertMulti(t, map[string]any{"d": tc.val}, map[string]any{
				"conversions": []any{map[string]any{
					"field": "d", "targetType": "date", "outputFormat": "02 January 2006 15:04", "timeZone": tc.zone,
				}},
			})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got["d"] != tc.want {
				t.Errorf("d = %#v, want %q", got["d"], tc.want)
			}
		})
	}
}

// A value that carries no zone -- MySQL DATETIME text, a day-first date, a bare
// date -- is that zone's wall clock. Reading it as UTC and then converting would
// move every such value by the zone's offset, which is the bug the option exists
// to prevent turned the other way round.
func TestDataConversion_Date_TimeZone_ReadsAZonelessValueInThatZone(t *testing.T) {
	cases := []struct {
		name   string
		val    string
		format string
		want   string
	}{
		{"timestamp text with no zone", "2026-09-18 20:00:00", "", "2026-09-18 20:00 +07:00"},
		{"a day-first layout", "18/09/2026 20:00", "02/01/2006 15:04", "2026-09-18 20:00 +07:00"},
		{"a bare date", "2026-09-18", "", "2026-09-18 00:00 +07:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertMulti(t, map[string]any{"d": tc.val}, map[string]any{
				"conversions": []any{map[string]any{
					"field": "d", "targetType": "date", "format": tc.format,
					"outputFormat": "2006-01-02 15:04 Z07:00", "timeZone": "Asia/Jakarta",
				}},
			})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got["d"] != tc.want {
				t.Errorf("d = %#v, want %q", got["d"], tc.want)
			}
		})
	}
}

// With no output format the row still hands over a time.Time: the same instant,
// now in the zone, so a serialised message carries the zone's offset and a date
// column binds the zone's date.
func TestDataConversion_Date_TimeZone_KeepsTheInstantWithoutAnOutputFormat(t *testing.T) {
	got, err := convertMulti(t, map[string]any{"d": "2026-09-18T20:00:00Z"}, map[string]any{
		"conversions": []any{map[string]any{"field": "d", "targetType": "date", "timeZone": "Asia/Jakarta"}},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	ts, ok := got["d"].(time.Time)
	if !ok {
		t.Fatalf("d = %#v, want a time.Time", got["d"])
	}
	if want := time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC); !ts.Equal(want) {
		t.Errorf("d = %s, want the instant %s", ts.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got := ts.Format(time.RFC3339); got != "2026-09-19T03:00:00+07:00" {
		t.Errorf("d renders as %s, want 2026-09-19T03:00:00+07:00", got)
	}
}

// A zone that does not load fails the node whatever On Error says, as an output
// format that prints no date does: "null" would turn a typo into a column of
// nulls. "Local" loads, but names whatever zone the server happens to run in,
// so the same workflow would write different dates on different machines.
func TestDataConversion_Date_UnknownTimeZoneFailsTheNode(t *testing.T) {
	for _, zone := range []string{"Asia/Jakart", "WIB", "Local"} {
		t.Run(zone, func(t *testing.T) {
			_, err := convertMulti(t, map[string]any{"d": "2026-09-18T20:00:00Z"}, map[string]any{
				"errorBehavior": "null",
				"conversions": []any{map[string]any{
					"field": "d", "targetType": "date", "outputFormat": "2006-01-02", "timeZone": zone,
				}},
			})
			if err == nil {
				t.Fatalf("time zone %q was accepted", zone)
			}
			for _, want := range []string{`"d"`, zone} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// The editor keeps a row's keys when its target type changes; only a date row
// reads its zone.
func TestDataConversion_TimeZoneIsIgnoredOffADateRow(t *testing.T) {
	got, err := convertMulti(t, map[string]any{"qty": "7"}, map[string]any{
		"conversions": []any{map[string]any{"field": "qty", "targetType": "int", "timeZone": "Asia/Jakart"}},
	})
	if err != nil {
		t.Fatalf("an int row failed on a date setting it does not use: %v", err)
	}
	if got["qty"] != int64(7) {
		t.Errorf("qty = %#v, want int64(7)", got["qty"])
	}
}

func TestDataConversion_Date_TimeZone_PreparedAndUnpreparedAgree(t *testing.T) {
	cfg := map[string]any{
		"conversions": []any{map[string]any{
			"field": "d", "targetType": "date", "outputFormat": "02 January 2006 15:04", "timeZone": "Asia/Jakarta",
		}},
	}
	in := map[string]any{"d": "2026-09-18T20:00:00Z"}

	unprepared, err := convertMulti(t, in, cfg)
	if err != nil {
		t.Fatalf("unprepared: %v", err)
	}
	preparedCfg, err := (&DataConversionTransformer{}).Prepare(cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prepared, err := convertMulti(t, in, preparedCfg)
	if err != nil {
		t.Fatalf("prepared: %v", err)
	}
	if prepared["d"] != "19 September 2026 03:00" || unprepared["d"] != prepared["d"] {
		t.Errorf("prepared = %#v, unprepared = %#v, want both \"19 September 2026 03:00\"", prepared["d"], unprepared["d"])
	}
}
