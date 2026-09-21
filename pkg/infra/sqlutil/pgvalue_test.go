package sqlutil

import (
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// Every expectation below is PostgreSQL 18.4's own text output, captured from
// the server rather than recalled:
//
//	SELECT interval '14 months'::text;  -- 1 year 2 mons
//	SELECT interval '-14 months'::text; -- -1 years -2 mons
//	SELECT '08:30:00'::time::text;      -- 08:30:00
//
// That matters because pgx's own text encoder is close but not the same:
// it renders the first as "14 mon" and the time as "08:30:00.000000". Using it
// would leave the two paths still disagreeing, just less visibly.
func TestFormatPGIntervalMatchesPostgres(t *testing.T) {
	tests := []struct {
		name string
		in   pgtype.Interval
		want string
	}{
		{"1 day 02:00:00", pgtype.Interval{Days: 1, Microseconds: 7200000000, Valid: true}, "1 day 02:00:00"},
		{"only months", pgtype.Interval{Months: 14, Valid: true}, "1 year 2 mons"},
		{"one month", pgtype.Interval{Months: 1, Valid: true}, "1 mon"},
		{"one year", pgtype.Interval{Months: 12, Valid: true}, "1 year"},
		{"only days", pgtype.Interval{Days: 3, Valid: true}, "3 days"},
		{"one day", pgtype.Interval{Days: 1, Valid: true}, "1 day"},
		{"negative days", pgtype.Interval{Days: -3, Valid: true}, "-3 days"},

		// The quirk worth having a case for: PostgreSQL pluralises on n != 1,
		// not on |n| != 1, so minus one year is "-1 years".
		{"negative months", pgtype.Interval{Months: -14, Valid: true}, "-1 years -2 mons"},

		{"fractional seconds", pgtype.Interval{Microseconds: 1500000, Valid: true}, "00:00:01.5"},
		{"negative seconds", pgtype.Interval{Microseconds: -1000000, Valid: true}, "-00:00:01"},

		// Hours are not wrapped at 24 and the time part carries its own sign,
		// independently of the day count's.
		{"hours past a day", pgtype.Interval{Microseconds: 360000000000, Valid: true}, "100:00:00"},
		{"day minus hours", pgtype.Interval{Days: 1, Microseconds: -7200000000, Valid: true}, "1 day -02:00:00"},

		{"days and fractional time", pgtype.Interval{Days: 3, Microseconds: 14706500000, Valid: true}, "3 days 04:05:06.5"},

		// A zero time part is omitted when anything else is present, and is the
		// whole rendering when nothing is.
		{"months and days, no time", pgtype.Interval{Months: 14, Days: 3, Valid: true}, "1 year 2 mons 3 days"},
		{"zero", pgtype.Interval{Valid: true}, "00:00:00"},

		{"full mix", pgtype.Interval{Months: 14, Days: 3, Microseconds: 14706000000, Valid: true}, "1 year 2 mons 3 days 04:05:06"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatPGInterval(tc.in); got != tc.want {
				t.Errorf("formatPGInterval = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatPGTimeMatchesPostgres(t *testing.T) {
	tests := []struct {
		in   pgtype.Time
		want string
	}{
		{pgtype.Time{Microseconds: 30600000000, Valid: true}, "08:30:00"},
		{pgtype.Time{Microseconds: 30600123456, Valid: true}, "08:30:00.123456"},
		{pgtype.Time{Microseconds: 0, Valid: true}, "00:00:00"},
		{pgtype.Time{Microseconds: 86399000000, Valid: true}, "23:59:59"},
		{pgtype.Time{Microseconds: 1500000, Valid: true}, "00:00:01.5"},
	}
	for _, tc := range tests {
		if got := formatPGTime(tc.in); got != tc.want {
			t.Errorf("formatPGTime(%d) = %q, want %q", tc.in.Microseconds, got, tc.want)
		}
	}
}

// DecodePGXValue is what the native Postgres and Yugabyte read paths apply to
// every value pgx hands back. Before it, three types reached a message as
// something no operator could write a field path against: pgtype.Time and
// pgtype.Interval serialised as their structs --
// {"Microseconds":30600000000,"Valid":true} -- and a macaddr as base64,
// because net.HardwareAddr is a *named* []byte type and the source's
// `val.([]byte)` assertion does not match it.
func TestDecodePGXValueNormalisesTheLeakingTypes(t *testing.T) {
	mac, err := net.ParseMAC("08:00:2b:01:02:03")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"time", pgtype.Time{Microseconds: 30600000000, Valid: true}, "08:30:00"},
		{"interval", pgtype.Interval{Days: 1, Microseconds: 7200000000, Valid: true}, "1 day 02:00:00"},
		{"macaddr", mac, "08:00:2b:01:02:03"},

		// A NULL of those types stays null rather than becoming the string a
		// zero value would render as.
		{"null time", pgtype.Time{}, nil},
		{"null interval", pgtype.Interval{}, nil},

		// Unchanged: the []byte -> string conversion these call sites already
		// did, and everything pgx already decodes into a usable Go value.
		{"driver bytes", []byte("hi"), "hi"},
		{"string", "x", "x"},
		{"int", int32(7), int32(7)},
		{"nil", nil, nil},
		{"map from jsonb", map[string]any{"a": 1}, map[string]any{"a": 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodePGXValue(tc.in)
			if m, ok := tc.want.(map[string]any); ok {
				gm, ok := got.(map[string]any)
				if !ok || len(gm) != len(m) {
					t.Fatalf("DecodePGXValue = %#v, want %#v", got, tc.want)
				}
				return
			}
			if got != tc.want {
				t.Errorf("DecodePGXValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}
