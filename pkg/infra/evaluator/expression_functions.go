package evaluator

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	// tzdata embeds the IANA time zone database, so to_timezone('Asia/Jakarta')
	// answers on an image that carries no /usr/share/zoneinfo -- the
	// distroless one Hermod ships in.
	_ "time/tzdata"

	"golang.org/x/text/unicode/norm"
)

// The functions CallFunction dispatches to that take more than a line. Each
// answers nil for a call it cannot answer -- too few arguments, a value it
// cannot read -- as the older functions in CallFunction do, so coalesce or
// default can supply a fallback and a preview shows null where it went wrong.

// maxPadWidth bounds pad_left and pad_right. The width can come from message
// data, and a width of a billion is a gigabyte per record.
const maxPadWidth = 10000

// isMissing is what default replaces, and what coalesce skips: no value, or
// empty text. 0 and false are values.
func isMissing(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// isEmptyValue is is_empty: missing, text with nothing but whitespace, or an
// empty list or object.
func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// textLength is len: characters, not bytes, of the value as text; the items of
// a list and the keys of an object.
func textLength(v any) int {
	switch t := v.(type) {
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	}
	return utf8.RuneCountInString(stringify(v))
}

// wholeNumber reads v as a whole number from lo to hi. Anything else -- text
// that is not a number, a fraction, a number out of range -- is not one, and
// the caller answers nil rather than guess.
func wholeNumber(v any, lo, hi int) (int, bool) {
	f, ok := ToFloat64(v)
	if !ok || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
		return 0, false
	}
	return int(f), true
}

// pad is pad_left and pad_right: s widened to width characters with fill,
// repeated and cut to fit. Text already that wide is returned whole.
func pad(args []any, left bool) any {
	if len(args) < 2 {
		return nil
	}
	width, ok := wholeNumber(args[1], 0, maxPadWidth)
	if !ok {
		return nil
	}
	s := stringify(args[0])
	fill := " "
	if len(args) >= 3 {
		if f := stringify(args[2]); f != "" {
			fill = f
		}
	}
	short := width - utf8.RuneCountInString(s)
	if short <= 0 {
		return s
	}
	fillRunes := []rune(fill)
	padding := make([]rune, short)
	for i := range padding {
		padding[i] = fillRunes[i%len(fillRunes)]
	}
	if left {
		return string(padding) + s
	}
	return s + string(padding)
}

// titleCase capitalises the first letter of every word and lowercases the
// rest. A word starts after anything that is not a letter, a digit or an
// apostrophe, so jean-luc is Jean-Luc and o'neil is O'neil, not O'Neil.
func titleCase(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inWord := false
	for _, r := range s {
		if inWord {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(unicode.ToTitle(r))
		}
		inWord = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’'
	}
	return sb.String()
}

// slugify is slug: lowercase letters and digits with their accents dropped,
// every run of anything else one hyphen, and no hyphen at either end. A letter
// with no unaccented form, such as ß, stays as it is.
func slugify(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	hyphen := false
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// The accent NFD split off its letter.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if hyphen && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			hyphen = false
			sb.WriteRune(unicode.ToLower(r))
		default:
			hyphen = true
		}
	}
	return sb.String()
}

// functionPattern compiles a regex_extract or regex_replace pattern through
// the condition operators' bounded cache, and refuses one longer than
// maxFunctionPatternLen before it is compiled or cached.
func functionPattern(v any) *regexp.Regexp {
	expr := stringify(v)
	if len(expr) > maxFunctionPatternLen {
		return nil
	}
	re, err := compilePattern(expr)
	if err != nil {
		return nil
	}
	return re
}

// regexExtract is regex_extract(text, pattern, [group]): the first match, or
// the group of it numbered or named. A group that took no part in the match
// is nil, the same as no match.
func regexExtract(args []any) any {
	if len(args) < 2 {
		return nil
	}
	re := functionPattern(args[1])
	if re == nil {
		return nil
	}
	group := 0
	if len(args) >= 3 {
		if _, isNumber := ToFloat64(args[2]); isNumber {
			g, ok := wholeNumber(args[2], 0, re.NumSubexp())
			if !ok {
				return nil
			}
			group = g
		} else if group = re.SubexpIndex(stringify(args[2])); group < 0 {
			return nil
		}
	}
	s := stringify(args[0])
	m := re.FindStringSubmatchIndex(s)
	if m == nil || m[2*group] < 0 {
		return nil
	}
	return s[m[2*group]:m[2*group+1]]
}

// regexReplace is regex_replace(text, pattern, with): every match replaced,
// with $1 or ${name} in `with` standing for a group.
func regexReplace(args []any) any {
	if len(args) < 3 {
		return nil
	}
	re := functionPattern(args[1])
	if re == nil {
		return nil
	}
	return re.ReplaceAllString(stringify(args[0]), stringify(args[2]))
}

// finiteNumber is a computed number as an expression answers it: nil when it
// has no JSON form, and 0 rather than -0, which JSON would write as "-0".
func finiteNumber(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	if f == 0 {
		return 0.0
	}
	return f
}

// unaryNumber is floor and ceil. Not a number reads as 0, as it does in add.
func unaryNumber(args []any, fn func(float64) float64) any {
	if len(args) == 0 {
		return nil
	}
	v, _ := ToFloat64(args[0])
	return finiteNumber(fn(v))
}

// extreme is min and max: the smallest or largest of the values that are
// numbers, a list's items counted one by one. Values that are not numbers are
// skipped rather than read as 0, which would make min of a missing field 0.
func extreme(args []any, smallest bool) any {
	var best float64
	found := false
	consider := func(v any) {
		f, ok := ToFloat64(v)
		if !ok || math.IsNaN(f) {
			return
		}
		if !found || (smallest && f < best) || (!smallest && f > best) {
			best, found = f, true
		}
	}
	for _, arg := range args {
		if list, ok := arg.([]any); ok {
			for _, item := range list {
				consider(item)
			}
			continue
		}
		consider(arg)
	}
	if !found {
		return nil
	}
	return finiteNumber(best)
}

// clamp is clamp(x, lo, hi). A range whose bounds are upside down is nil
// rather than a guess at which bound was meant.
func clamp(args []any) any {
	if len(args) < 3 {
		return nil
	}
	x, _ := ToFloat64(args[0])
	lo, _ := ToFloat64(args[1])
	hi, _ := ToFloat64(args[2])
	if lo > hi {
		return nil
	}
	return finiteNumber(math.Max(lo, math.Min(x, hi)))
}

// numberSeparators are format_number's locales: the thousands separator, then
// the decimal mark. fr groups with U+202F, the narrow no-break space CLDR gives
// it, so a number is never split across two lines.
var numberSeparators = map[string][2]string{
	"en": {",", "."},
	"de": {".", ","},
	"fr": {" ", ","},
	"id": {".", ","},
	"it": {".", ","},
	"nl": {".", ","},
}

// formatNumber is format_number(x, [decimals], [locale]): x rounded half away
// from zero, as round() rounds, and written with the locale's separators. A
// locale is read by its language, so en-US and de_DE are en and de; one not in
// numberSeparators is nil rather than silently English.
func formatNumber(args []any) any {
	if len(args) == 0 {
		return nil
	}
	x, ok := ToFloat64(args[0])
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	decimals := 0
	if len(args) >= 2 {
		if decimals, ok = wholeNumber(args[1], 0, 20); !ok {
			return nil
		}
	}
	locale := "en"
	if len(args) >= 3 {
		if l := strings.ToLower(strings.TrimSpace(stringify(args[2]))); l != "" {
			locale, _, _ = strings.Cut(strings.ReplaceAll(l, "_", "-"), "-")
		}
	}
	seps, ok := numberSeparators[locale]
	if !ok {
		return nil
	}

	ratio := math.Pow(10, float64(decimals))
	if r := math.Round(x*ratio) / ratio; !math.IsInf(r, 0) && !math.IsNaN(r) {
		// A number too large to scale has no fraction to round.
		x = r
	}
	return groupDigits(x, decimals, seps[0], seps[1])
}

// groupDigits writes x with decimals places, thousands between every three
// digits of its whole part and point before its fraction.
func groupDigits(x float64, decimals int, thousands, point string) string {
	digits := strconv.FormatFloat(math.Abs(x), 'f', decimals, 64)
	whole, frac, _ := strings.Cut(digits, ".")

	var sb strings.Builder
	if x < 0 {
		sb.WriteByte('-')
	}
	for i, d := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			sb.WriteString(thousands)
		}
		sb.WriteRune(d)
	}
	if frac != "" {
		sb.WriteString(point)
		sb.WriteString(frac)
	}
	return sb.String()
}

// readDate reads a date argument as toDate does. A number of seconds since
// 1970 has no zone of its own, so it is UTC rather than the server's.
func readDate(v any) (time.Time, bool) {
	if v == nil {
		return time.Time{}, false
	}
	t, ok := ToTime(v)
	if !ok {
		return time.Time{}, false
	}
	switch v.(type) {
	case string, time.Time:
		return t, true
	}
	return t.UTC(), true
}

// formatDate is how a date function answers: ISO 8601 in the date's own
// offset. It is toDate's format, plus fractions of a second when there are
// any, which toDate's drops.
func formatDate(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// parseFlexDuration reads date_add's duration: a Go duration, in which d is
// also a day of 24 hours and w a week of 7. 1d12h, -1w and 1.5d are durations;
// 7 days and 1y are not.
func parseFlexDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	negative := false
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		negative, s = true, rest
	} else {
		s = strings.TrimPrefix(s, "+")
	}
	if s == "" {
		return 0, false
	}

	// Days and weeks are summed here; everything else is left for
	// time.ParseDuration, which knows the rest of the units.
	var days float64
	var rest strings.Builder
	for i := 0; i < len(s); {
		number, unit, next := durationTerm(s, i)
		if number == "" {
			return 0, false
		}
		i = next
		if unit != "d" && unit != "w" {
			rest.WriteString(number)
			rest.WriteString(unit)
			continue
		}
		n, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, false
		}
		if unit == "w" {
			n *= 7
		}
		days += n
	}

	var d time.Duration
	if rest.Len() > 0 {
		var err error
		if d, err = time.ParseDuration(rest.String()); err != nil {
			return 0, false
		}
	}
	total := float64(d) + days*float64(24*time.Hour)
	if total >= math.MaxInt64 {
		return 0, false
	}
	if negative {
		total = -total
	}
	return time.Duration(total), true
}

// durationTerm reads the term of a duration that starts at s[i] -- a number,
// then its unit -- and where the next one starts.
func durationTerm(s string, i int) (number, unit string, next int) {
	isNumber := func(c byte) bool { return c >= '0' && c <= '9' || c == '.' }
	start := i
	for i < len(s) && isNumber(s[i]) {
		i++
	}
	unitStart := i
	for i < len(s) && !isNumber(s[i]) {
		i++
	}
	return s[start:unitStart], s[unitStart:i], i
}

// dateAdd is date_add(date, duration).
func dateAdd(args []any) any {
	if len(args) < 2 {
		return nil
	}
	t, ok := readDate(args[0])
	if !ok {
		return nil
	}
	d, ok := parseFlexDuration(stringify(args[1]))
	if !ok {
		return nil
	}
	return formatDate(t.Add(d))
}

// dateUnits are date_diff's fixed-length units. m is a minute, as it is in a
// Go duration; a month is only month or months.
var dateUnits = map[string]time.Duration{
	"ms": time.Millisecond, "millisecond": time.Millisecond, "milliseconds": time.Millisecond,
	"s": time.Second, "sec": time.Second, "second": time.Second, "seconds": time.Second,
	"m": time.Minute, "min": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"h": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
}

// dateDiff is date_diff(a, b, [unit]): a minus b in whole units, cut towards
// zero, in days unless told otherwise.
func dateDiff(args []any) any {
	if len(args) < 2 {
		return nil
	}
	a, ok := readDate(args[0])
	if !ok {
		return nil
	}
	b, ok := readDate(args[1])
	if !ok {
		return nil
	}
	unit := "day"
	if len(args) >= 3 {
		unit = strings.ToLower(strings.TrimSpace(stringify(args[2])))
	}
	switch unit {
	case "month", "months":
		return calendarMonths(a, b)
	case "year", "years":
		return calendarMonths(a, b) / 12
	}
	size, ok := dateUnits[unit]
	if !ok {
		return nil
	}
	return int64(a.Sub(b) / size)
}

// calendarMonths is how many whole calendar months a is after b, negative when
// it is before: 9 March to 8 May is one month, to 9 May two. b is read in a's
// zone, so the two calendars agree.
func calendarMonths(a, b time.Time) int64 {
	b = b.In(a.Location())
	months := int64(a.Year()-b.Year())*12 + int64(a.Month()-b.Month())
	// Where each date falls within its month, to the nanosecond.
	within := func(t time.Time) int64 {
		return int64(t.Day())*int64(24*time.Hour) + int64(t.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())))
	}
	switch {
	case months > 0 && within(a) < within(b):
		months--
	case months < 0 && within(a) > within(b):
		months++
	}
	return months
}

// dateTrunc is date_trunc(date, unit): the start of the second, minute, hour,
// day, week (from Monday), month or year the date falls in, in its own zone.
func dateTrunc(args []any) any {
	if len(args) < 2 {
		return nil
	}
	t, ok := readDate(args[0])
	if !ok {
		return nil
	}
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	loc := t.Location()
	var out time.Time
	switch strings.ToLower(strings.TrimSpace(stringify(args[1]))) {
	case "second":
		out = time.Date(y, mo, d, h, mi, s, 0, loc)
	case "minute":
		out = time.Date(y, mo, d, h, mi, 0, 0, loc)
	case "hour":
		out = time.Date(y, mo, d, h, 0, 0, 0, loc)
	case "day":
		out = time.Date(y, mo, d, 0, 0, 0, 0, loc)
	case "week":
		sinceMonday := (int(t.Weekday()) + 6) % 7
		out = time.Date(y, mo, d-sinceMonday, 0, 0, 0, 0, loc)
	case "month":
		out = time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	case "year":
		out = time.Date(y, time.January, 1, 0, 0, 0, 0, loc)
	default:
		return nil
	}
	return formatDate(out)
}

// maxCachedLocations bounds the zones to_timezone keeps loaded. Only a name
// that loads is kept, and there are some six hundred of those, so this is a
// backstop rather than an eviction policy.
const maxCachedLocations = 1024

var loadedLocations = struct {
	mu sync.RWMutex
	m  map[string]*time.Location
}{m: make(map[string]*time.Location)}

// loadLocation is time.LoadLocation, kept: loading a zone reads and parses its
// file, and to_timezone runs once per record. "" and "Local" are refused --
// LoadLocation reads them as UTC and as the server's own zone, neither of
// which is a zone anyone asked for by name.
func loadLocation(name string) (*time.Location, bool) {
	if name == "" || name == "Local" {
		return nil, false
	}
	loadedLocations.mu.RLock()
	loc, ok := loadedLocations.m[name]
	loadedLocations.mu.RUnlock()
	if ok {
		return loc, true
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	loadedLocations.mu.Lock()
	if len(loadedLocations.m) < maxCachedLocations {
		loadedLocations.m[name] = loc
	}
	loadedLocations.mu.Unlock()
	return loc, true
}

// toTimezone is to_timezone(date, zone): the same instant on an IANA zone's
// clock.
func toTimezone(args []any) any {
	if len(args) < 2 {
		return nil
	}
	t, ok := readDate(args[0])
	if !ok {
		return nil
	}
	loc, ok := loadLocation(strings.TrimSpace(stringify(args[1])))
	if !ok {
		return nil
	}
	return formatDate(t.In(loc))
}

// parseDate is parse_date(text, layout): text read with a Go layout, which
// shows how 2 Jan 2006 15:04:05 would be written.
func parseDate(args []any) any {
	if len(args) < 2 || args[0] == nil {
		return nil
	}
	t, err := time.Parse(stringify(args[1]), strings.TrimSpace(stringify(args[0])))
	if err != nil {
		return nil
	}
	return formatDate(t)
}

// isoWeekday is weekday: Monday 1 to Sunday 7, on the date's own calendar.
func isoWeekday(args []any) any {
	if len(args) == 0 {
		return nil
	}
	t, ok := readDate(args[0])
	if !ok {
		return nil
	}
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}
	return 7
}

// epochMillis is epoch_ms: milliseconds since 1970 UTC.
func epochMillis(args []any) any {
	if len(args) == 0 {
		return nil
	}
	t, ok := readDate(args[0])
	if !ok {
		return nil
	}
	return t.UnixMilli()
}

// jsonPathStep is one step of a json_get path: a key, or an item index.
type jsonPathStep struct {
	key     string
	index   int
	isIndex bool
}

// parseJSONGetPath reads a json_get path: keys between dots and [n] items,
// a.b[0].c or [2].x. An empty path is the value itself. An empty key, an
// unclosed bracket or an index that is not a whole number is no path at all.
func parseJSONGetPath(p string) ([]jsonPathStep, bool) {
	var steps []jsonPathStep
	for i := 0; i < len(p); {
		var (
			step jsonPathStep
			ok   bool
		)
		if step, i, ok = jsonPathStepAt(p, i); !ok {
			return nil, false
		}
		steps = append(steps, step)
		if i < len(p) && p[i] == '.' {
			if i++; i == len(p) {
				return nil, false
			}
		}
	}
	return steps, true
}

// jsonPathStepAt reads the step of a json_get path that starts at p[i], and
// where the text after it starts.
func jsonPathStepAt(p string, i int) (jsonPathStep, int, bool) {
	switch p[i] {
	case '[':
		end := strings.IndexByte(p[i:], ']')
		if end < 0 {
			return jsonPathStep{}, 0, false
		}
		n, err := strconv.Atoi(p[i+1 : i+end])
		if err != nil {
			return jsonPathStep{}, 0, false
		}
		next := i + end + 1
		// a[0]b is not a path: an item is followed by a dot, another item, or
		// nothing.
		if next < len(p) && p[next] != '.' && p[next] != '[' {
			return jsonPathStep{}, 0, false
		}
		return jsonPathStep{index: n, isIndex: true}, next, true
	case '.':
		return jsonPathStep{}, 0, false
	}
	end := strings.IndexAny(p[i:], ".[")
	if end < 0 {
		end = len(p) - i
	}
	return jsonPathStep{key: p[i : i+end]}, i + end, true
}

// stepInto takes one json_get step into cur: a key of an object, or an item of
// a list. A step that does not fit what cur is leads nowhere.
func stepInto(cur any, step jsonPathStep) (any, bool) {
	switch node := cur.(type) {
	case map[string]any:
		if step.isIndex {
			return nil, false
		}
		return node[step.key], true
	case []any:
		if !step.isIndex {
			return nil, false
		}
		i := step.index
		if i < 0 {
			i += len(node)
		}
		if i < 0 || i >= len(node) {
			return nil, false
		}
		return node[i], true
	}
	return nil, false
}

// decodeJSONText reads text holding JSON into the shape every other value has:
// objects as map[string]any, lists as []any, numbers as float64.
func decodeJSONText(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

// jsonGet is json_get(value, path), in an object, a list, or text holding
// JSON.
func jsonGet(args []any) any {
	if len(args) < 2 {
		return nil
	}
	cur := args[0]
	if s, ok := cur.(string); ok {
		if cur, ok = decodeJSONText(s); !ok {
			return nil
		}
	}
	steps, ok := parseJSONGetPath(stringify(args[1]))
	if !ok {
		return nil
	}
	for _, step := range steps {
		if cur, ok = stepInto(cur, step); !ok {
			return nil
		}
	}
	return cur
}

// jsonParse is json_parse: JSON text read into a value. A value that is not
// text is already what parsing would make of it, and is returned as it is.
func jsonParse(args []any) any {
	if len(args) == 0 {
		return nil
	}
	s, ok := args[0].(string)
	if !ok {
		return args[0]
	}
	v, ok := decodeJSONText(s)
	if !ok {
		return nil
	}
	return v
}

// jsonStringify is json_stringify: a value as JSON text, keys sorted, with <, >
// and & written as they are rather than escaped for HTML.
func jsonStringify(args []any) any {
	if len(args) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(args[0]); err != nil {
		return nil
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// asList reads a value as a list: a list, or text holding a JSON list, which
// is what a text column or a body that arrived as a string carries.
func asList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case string:
		if s := strings.TrimSpace(t); strings.HasPrefix(s, "[") {
			var list []any
			if err := json.Unmarshal([]byte(s), &list); err == nil {
				return list, true
			}
		}
	}
	return nil, false
}

// listFunction is array_len, array_join, array_contains, first and last: each
// is nil for a value that is not a list.
func listFunction(name string, args []any) any {
	if len(args) == 0 {
		return nil
	}
	list, ok := asList(args[0])
	if !ok {
		return nil
	}
	switch name {
	case "array_len":
		return len(list)
	case "array_join":
		sep := ","
		if len(args) >= 2 {
			sep = stringify(args[1])
		}
		parts := make([]string, len(list))
		for i, item := range list {
			parts[i] = stringify(item)
		}
		return strings.Join(parts, sep)
	case "array_contains":
		if len(args) < 2 {
			return nil
		}
		want := stringify(args[1])
		for _, item := range list {
			if stringify(item) == want {
				return true
			}
		}
		return false
	case "first":
		if len(list) > 0 {
			return list[0]
		}
	case "last":
		if len(list) > 0 {
			return list[len(list)-1]
		}
	}
	return nil
}

// base64Decode is base64_decode: standard or URL-safe base64, padded or not.
// Bytes that are not UTF-8 text are nil: an expression's value is text, and
// writing them as text would replace each bad byte with U+FFFD unnoticed.
func base64Decode(args []any) any {
	if len(args) == 0 || args[0] == nil {
		return nil
	}
	s := strings.TrimSpace(stringify(args[0]))
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if !utf8.Valid(b) {
				return nil
			}
			return string(b)
		}
	}
	return nil
}

// hmacSHA256 is hmac_sha256(text, secret_name) for vhost: the lowercase hex
// HMAC-SHA256 of text, keyed with the secret of that name.
//
// The key is only ever named. A key written into the expression would sit in
// the workflow's config, its exports and every preview of it, so the second
// argument is always looked up -- a key typed inline is a name no secret has,
// and signs nothing. The lookup is secret()'s: the vhost's secrets, then the
// global manager. A secret that is missing or empty is nil rather than a
// signature made with an empty key, which a receiver would reject without
// saying why.
func hmacSHA256(vhost string, args []any) any {
	if len(args) < 2 || args[0] == nil {
		return nil
	}
	name := strings.TrimSpace(stringify(args[1]))
	if name == "" {
		return nil
	}
	key := readSecret(vhost, name)
	if key == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(stringify(args[0])))
	return hex.EncodeToString(mac.Sum(nil))
}

// encodeText is base64_encode, url_encode and url_decode. A missing value is
// nil, as it is in hash.
func encodeText(name string, args []any) any {
	if len(args) == 0 || args[0] == nil {
		return nil
	}
	s := stringify(args[0])
	switch name {
	case "base64_encode":
		return base64.StdEncoding.EncodeToString([]byte(s))
	case "url_encode":
		return url.QueryEscape(s)
	case "url_decode":
		out, err := url.QueryUnescape(s)
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}
