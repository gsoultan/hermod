package core

import (
	"context"
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
			got, err := tr.toDate(tc.val, tc.format)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toDate(%#v, %q) = %#v, want an error", tc.val, tc.format, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("toDate(%#v, %q): %v", tc.val, tc.format, err)
			}
			parsed, ok := got.(time.Time)
			if !ok {
				t.Fatalf("toDate(%#v, %q) = %#v, want a time.Time", tc.val, tc.format, got)
			}
			if !parsed.Equal(tc.want) {
				t.Errorf("toDate(%#v, %q) = %s, want %s", tc.val, tc.format,
					parsed.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
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
