package sqlutil

import (
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Normalising what pgx hands back on the *native* read path.
//
// PostgresSource and YugabyteSource read through pgx directly and take whatever
// rows.Values() produces. For most types that is a usable Go value, but three
// reached messages as something no operator can write a field path against, and
// no sink can sensibly write out:
//
//	time      {"Microseconds":30600000000,"Valid":true}
//	interval  {"Microseconds":7200000000,"Days":1,"Months":0,"Valid":true}
//	macaddr   "CAArAQID"
//
// Measured at message level against PostgreSQL 18.4. The same columns read
// through database/sql arrive as "08:30:00", "1 day 02:00:00" and
// "08:00:2b:01:02:03", so this brings the native path to the generic one rather
// than the other way round: a struct full of microseconds is not a shape anyone
// writes a workflow against.
//
// macaddr is the subtle one. net.HardwareAddr is a *named* []byte type, so the
// `val.([]byte)` assertion these call sites already had never matched it, and
// it fell through to Go's default []byte marshalling, which is base64.

// DecodePGXValue is the per-value rule the native pgx read paths apply.
//
// It subsumes the `[]byte -> string` conversion those call sites already did,
// so it is a drop-in for that line rather than an addition to it.
func DecodePGXValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case net.HardwareAddr:
		return t.String()
	case pgtype.Time:
		if !t.Valid {
			return nil
		}
		return formatPGTime(t)
	case pgtype.Interval:
		if !t.Valid {
			return nil
		}
		return formatPGInterval(t)
	}
	return v
}

// formatPGTime renders a time the way PostgreSQL's own text output does.
//
// Not pgx's encoder: that always emits six fractional digits, so a whole-second
// time becomes "08:30:00.000000" where the server and the generic read path
// both say "08:30:00".
func formatPGTime(t pgtype.Time) string {
	return formatMicrosAsClock(t.Microseconds)
}

// formatPGInterval renders an interval the way PostgreSQL's own text output
// does, which pgx's encoder does not: it renders 14 months as "14 mon" where
// the server says "1 year 2 mons".
//
// Every rule here is taken from the server's output rather than from memory,
// including the one that looks like a bug: PostgreSQL pluralises on n != 1 and
// not on |n| != 1, so minus one year is "-1 years". The unit counts and the
// clock part carry their signs independently -- a day minus two hours is
// "1 day -02:00:00".
func formatPGInterval(iv pgtype.Interval) string {
	// Go truncates integer division toward zero, which is what PostgreSQL does
	// here too: -14 months is -1 years and -2 mons, not -2 and +10.
	years, months := iv.Months/12, iv.Months%12

	var parts []string
	if years != 0 {
		parts = append(parts, fmt.Sprintf("%d year%s", years, pgPlural(years)))
	}
	if months != 0 {
		parts = append(parts, fmt.Sprintf("%d mon%s", months, pgPlural(months)))
	}
	if iv.Days != 0 {
		parts = append(parts, fmt.Sprintf("%d day%s", iv.Days, pgPlural(iv.Days)))
	}
	// A zero clock part is omitted when anything else is present, and is the
	// whole rendering when nothing is -- a zero interval is "00:00:00".
	if iv.Microseconds != 0 || len(parts) == 0 {
		parts = append(parts, formatMicrosAsClock(iv.Microseconds))
	}
	return strings.Join(parts, " ")
}

func pgPlural[T int32 | int64](n T) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatMicrosAsClock renders microseconds as PostgreSQL's HH:MM:SS[.ffffff].
//
// Hours are not wrapped at 24 -- an interval of 100 hours is "100:00:00" --
// and trailing zeros in the fraction are trimmed, so 1.5 seconds is
// "00:00:01.5" rather than "00:00:01.500000".
func formatMicrosAsClock(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}

	const perSecond = 1000000
	hours := micros / (3600 * perSecond)
	micros %= 3600 * perSecond
	minutes := micros / (60 * perSecond)
	micros %= 60 * perSecond
	seconds := micros / perSecond
	frac := micros % perSecond

	out := fmt.Sprintf("%s%02d:%02d:%02d", sign, hours, minutes, seconds)
	if frac != 0 {
		out += "." + strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
	}
	return out
}
