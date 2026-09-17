package smtp

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// dataMessage is a message whose template data the test writes, so a case can
// say exactly which columns the row carries.
type dataMessage struct {
	mockMessage
	data map[string]any
}

var _ hermod.Message = (*dataMessage)(nil)

func (m *dataMessage) Data() map[string]any    { return m.data }
func (m *dataMessage) DataRef() map[string]any { return m.data }
func (m *dataMessage) After() []byte           { return nil }

// renderInline sends one message through an inline body template and returns
// the body the sender was handed.
func renderInline(t *testing.T, tmplStr string, data map[string]any) (string, error) {
	t.Helper()
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{"to@example.com"},
		subject:        "Subject",
		templateSource: "inline",
		template:       tmplStr,
	}
	if err := sink.Write(t.Context(), &dataMessage{data: data}); err != nil {
		return "", err
	}
	return string(mock.lastEmail.Body), nil
}

func mustRenderInline(t *testing.T, tmplStr string, data map[string]any) string {
	t.Helper()
	body, err := renderInline(t, tmplStr, data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	return body
}

func TestTemplate_TimestampFieldFormats(t *testing.T) {
	got := mustRenderInline(t, `{{ .created_at.Format "2006-01-02" }}`,
		map[string]any{"created_at": "2026-12-01T09:30:00Z"})
	if want := "2026-12-01"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_TimestampFieldConvertsToLoadedLocation(t *testing.T) {
	got := mustRenderInline(t, `{{ .start_at.In (time.LoadLocation "Asia/Jakarta") }}`,
		map[string]any{"start_at": "2026-12-01T09:30:00Z"})
	if want := "2026-12-01T16:30:00+07:00"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_TimestampFieldConvertsToNamedZone(t *testing.T) {
	got := mustRenderInline(t, `{{ (.start_at.In "Asia/Jakarta").Format "15:04" }}`,
		map[string]any{"start_at": "2026-12-01T09:30:00Z"})
	if want := "16:30"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_DateFunctionsAcceptEveryValueShape(t *testing.T) {
	data := map[string]any{
		"created_at": "2026-12-01T09:30:00Z",
		"updated_at": time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC),
		// A JSON number reaches a sink as float64, never int64.
		"epoch": float64(time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC).Unix()),
	}
	got := mustRenderInline(t, `{{ date "2006-01-02" .created_at }}|`+
		`{{ date "2006-01-02 15:04" .updated_at }}|`+
		`{{ dateInZone "2006-01-02 15:04" "Asia/Jakarta" .created_at }}|`+
		`{{ (time.Unix .epoch).UTC.Format "15:04" }}`, data)
	want := "2026-12-01|2026-12-01 09:30|2026-12-01 16:30|09:30"
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_SubjectGetsTheSameFunctions(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{`{{ .email }}`},
		subject:        `Report for {{ date "02 Jan 2006" .created_at }}`,
		templateSource: "inline",
		template:       "body",
	}
	msg := &dataMessage{data: map[string]any{
		"created_at": "2026-12-01T09:30:00Z",
		"email":      "ops@example.com",
	}}
	if err := sink.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if want := "Report for 01 Dec 2026"; mock.lastEmail.Subject != want {
		t.Errorf("subject = %q, want %q", mock.lastEmail.Subject, want)
	}
	if len(mock.lastEmail.To) != 1 || mock.lastEmail.To[0] != "ops@example.com" {
		t.Errorf("recipients = %v, want [ops@example.com]", mock.lastEmail.To)
	}
}

// A recognised timestamp stays a string, so every template that treated the
// column as text before this change renders exactly what it rendered then.
func TestTemplate_TimestampKeepsStringBehaviour(t *testing.T) {
	got := mustRenderInline(t,
		`{{ .created_at }}|`+
			`{{ if eq .created_at "2026-12-01T09:30:00Z" }}eq{{ end }}|`+
			`{{ slice .created_at 0 10 }}|`+
			`{{ len .created_at }}|`+
			`{{ printf "%T" .created_at }}`,
		map[string]any{"created_at": "2026-12-01T09:30:00Z"})
	want := `2026-12-01T09:30:00Z|eq|2026-12-01|20|smtp.Timestamp`
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_NonTimeValuesAreLeftAlone(t *testing.T) {
	got := mustRenderInline(t,
		`{{ printf "%T" .note }}|{{ printf "%T" .code }}|{{ .code }}`,
		map[string]any{"note": "hello", "code": "2026-13-45"})
	want := "string|string|2026-13-45"
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// The row's nested maps are shared with every other sink on the workflow, so
// recognising a timestamp inside one must not rewrite it where they can see.
func TestTemplate_NestedRowDataIsNotRewrittenInPlace(t *testing.T) {
	nested := map[string]any{"created_at": "2026-12-01T09:30:00Z"}
	got := mustRenderInline(t, `{{ .after.created_at.Format "2006" }}`,
		map[string]any{"after": nested})
	if want := "2026"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if _, ok := nested["created_at"].(string); !ok {
		t.Errorf("nested row data was rewritten in place: %T", nested["created_at"])
	}
}

func TestTemplate_UnknownZoneNamesTheZone(t *testing.T) {
	_, err := renderInline(t, `{{ .start_at.In "Mars/Phobos" }}`,
		map[string]any{"start_at": "2026-12-01T09:30:00Z"})
	if err == nil {
		t.Fatal("expected an unknown time zone to fail the send")
	}
	if !strings.Contains(err.Error(), "Mars/Phobos") {
		t.Errorf("error %q does not name the zone", err)
	}
}

func TestTemplate_HTMLBodyStillEscapesValues(t *testing.T) {
	got := mustRenderInline(t, `<h1>Hello {{ .name }}</h1>`,
		map[string]any{"name": `<b>x</b>`})
	if want := `<h1>Hello &lt;b&gt;x&lt;/b&gt;</h1>`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTemplate_OutlookConversionStillApplies(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:            mock,
		from:              "from@example.com",
		to:                []string{"to@example.com"},
		subject:           "Subject",
		templateSource:    "inline",
		template:          `<h1>Hi {{ .name }}</h1>`,
		outlookCompatible: true,
	}
	if err := sink.Write(t.Context(), &dataMessage{data: map[string]any{"name": "Ana"}}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	body := string(mock.lastEmail.Body)
	if !strings.Contains(body, "OutlookHolder") {
		t.Errorf("body was not converted for Outlook: %q", body)
	}
	if !strings.Contains(body, "Hi Ana") {
		t.Errorf("body lost its rendered value: %q", body)
	}
}

// Every helper the sink advertises, exercised once, so the documentation and
// the function set cannot drift apart.
func TestTemplate_EveryAdvertisedHelper(t *testing.T) {
	utc := time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)
	data := map[string]any{
		"created_at": "2026-12-01T09:30:00Z",
		"start_at":   "2026-12-01T09:30:00Z",
		"due":        "01/12/2026",
		"epoch":      float64(utc.Unix()),
		"ms":         float64(utc.UnixMilli()),
	}
	cases := []struct{ tmpl, want string }{
		{`{{ .created_at.Time.Year }}`, "2026"},
		{`{{ .created_at.Unix }}`, strconv.FormatInt(utc.Unix(), 10)},
		{`{{ (.start_at.In "Asia/Jakarta").UTC }}`, "2026-12-01T09:30:00Z"},
		{`{{ .start_at.In time.UTC }}`, "2026-12-01T09:30:00Z"},
		{`{{ .start_at.In time.Local }}`, utc.In(time.Local).Format(time.RFC3339Nano)},
		{`{{ (time.Parse "02/01/2006" .due).Format "2006-01-02" }}`, "2026-12-01"},
		{`{{ (time.Unix .epoch).UTC.Format "15:04" }}`, "09:30"},
		{`{{ (time.UnixMilli .ms).UTC.Format "15:04:05.000" }}`, "09:30:00.000"},
		{`{{ (toDate .created_at).Year }}`, "2026"},
		{`{{ now.Format "2006" }}`, time.Now().Format("2006")},
		{`{{ time.Now.Format "2006" }}`, time.Now().Format("2006")},
		{`{{ .created_at | date "02 Jan 2006" }}`, "01 Dec 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			got := mustRenderInline(t, tc.tmpl, data)
			if got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

// A column reaches a sink as text on the CDC path and as a driver's own
// time.Time on the query path, and an operator writes one template for both.
func TestTemplate_DriverTimeAndTextBehaveAlike(t *testing.T) {
	data := map[string]any{
		"driver_ts": time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC),
		// What PostgreSQL's logical stream sends for the same column.
		"cdc_ts":  "2026-12-01 09:30:00+00",
		"cdc_ts2": "2026-12-01 11:00:00+00",
	}
	cases := []struct{ tmpl, want string }{
		{`{{ .driver_ts.Format "2006-01-02 15:04" }}`, "2026-12-01 09:30"},
		{`{{ .cdc_ts.Format "2006-01-02 15:04" }}`, "2026-12-01 09:30"},
		{`{{ (.driver_ts.In "Asia/Jakarta").Format "15:04 MST" }}`, "16:30 WIB"},
		{`{{ (.cdc_ts.In "Asia/Jakarta").Format "15:04 MST" }}`, "16:30 WIB"},
		// A second zone, on a different offset, deliberately. The text path
		// used to render .In back to a string and re-parse it, and time.Parse
		// recovers a named zone from a bare offset only when that offset
		// matches the host's own -- so this suite passed on a machine set to
		// Jakarta and printed "+0700" on every other one, CI included. Two
		// zones cannot both match one host, so neither can hide the next
		// regression.
		{`{{ (.driver_ts.In "Asia/Kolkata").Format "15:04 MST" }}`, "15:00 IST"},
		{`{{ (.cdc_ts.In "Asia/Kolkata").Format "15:04 MST" }}`, "15:00 IST"},
		{`{{ .driver_ts.In (time.LoadLocation "Asia/Jakarta") }}`, "2026-12-01 16:30:00 +0700 WIB"},
		// Printing a driver time is unchanged: Go's own rendering.
		{`{{ .driver_ts }}`, "2026-12-01 09:30:00 +0000 UTC"},
		{`{{ .driver_ts.Year }}`, "2026"},
		// Comparisons reach across the two shapes.
		{`{{ .cdc_ts2.Sub .driver_ts }}`, "1h30m0s"},
		{`{{ .driver_ts.Before .cdc_ts2 }}`, "true"},
		{`{{ .driver_ts.Equal .cdc_ts }}`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			got := mustRenderInline(t, tc.tmpl, data)
			if got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

// The text a PostgreSQL column actually arrives as on the CDC path, taken from
// a live server (18) rather than from memory:
//
//	SET TIME ZONE 'Asia/Kolkata'; SELECT '2026-12-01 09:30:00'::timestamptz::text;
//
// The offset is whatever the session's zone makes it, and a whole-hour one is
// written +07 where a staggered one is written +05:30 — two different layouts
// for one column type, which is what makes this worth pinning.
func TestTemplate_PostgresTextShapes(t *testing.T) {
	cases := []struct{ column, text, want string }{
		{"timestamptz, UTC", "2026-12-01 09:30:00+00", "2026-12-01 09:30"},
		{"timestamptz, Asia/Jakarta", "2026-12-01 09:30:00+07", "2026-12-01 09:30"},
		{"timestamptz, Asia/Kolkata", "2026-12-01 09:30:00+05:30", "2026-12-01 09:30"},
		{"timestamptz, fractional", "2026-12-01 09:30:00.123456+05:45", "2026-12-01 09:30"},
		{"timestamp", "2026-12-01 09:30:00.5", "2026-12-01 09:30"},
		{"date", "2026-12-01", "2026-12-01 00:00"},
	}
	for _, tc := range cases {
		t.Run(tc.column, func(t *testing.T) {
			got := mustRenderInline(t, `{{ .c.Format "2006-01-02 15:04" }}`, map[string]any{"c": tc.text})
			if got != tc.want {
				t.Errorf("%s: body = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// A time-only column is left as text: it names no day, so a zone conversion on
// it would be a guess.
func TestTemplate_TimeOnlyColumnStaysText(t *testing.T) {
	got := mustRenderInline(t, `{{ .c }}|{{ printf "%T" .c }}`, map[string]any{"c": "09:30:00"})
	if want := "09:30:00|string"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
