package sqlutil

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// TemplateBinding is the result of turning a SQL template into a parameterized
// statement: the rewritten SQL, the bound arguments in placeholder order, and
// the token paths that resolved to nothing.
type TemplateBinding struct {
	SQL  string
	Args []any
	// Unresolved holds the paths of tokens that resolved to nothing. Those
	// tokens are still bound -- as NULL -- because an optional message field is
	// a legitimate reason for a path to be empty. Callers whose variables come
	// from configuration rather than from a message should treat a non-empty
	// Unresolved as an error instead: binding NULL there turns a typo into a
	// query that silently matches no rows.
	Unresolved []string
	// Err is set when the template could not be turned into a statement at all
	// -- currently only when a list is too long to expand. The SQL and Args are
	// not usable when it is set.
	Err error
}

// MaxListExpansion bounds how many placeholders one token may expand into.
//
// The element count comes from message data, which nothing upstream bounds, and
// each element costs a placeholder in the statement text and an argument in the
// bind list. PostgreSQL's wire protocol stops at 65535 parameters and SQL Server
// at 2100, so a list beyond this never reaches a server that would accept it --
// but without the cap it is built in this process first, which is where a
// pathological array does its damage.
const MaxListExpansion = 65535

// ParameterizeTemplate replaces all {{ ... }} tokens in the SQL template with driver-specific placeholders
// and returns the parameterized SQL text and a corresponding args slice.
// Token content should be either a path like `source.foo` or a quoted literal. Paths are resolved against `data`.
func ParameterizeTemplate(driver, tpl string, data map[string]any) (string, []any) {
	b := ParameterizeTemplateEx(driver, tpl, data)
	return b.SQL, b.Args
}

// Resolver answers what one token's path resolves to.
//
// It exists because a SQL template used to be strictly weaker than every other
// template in Hermod. Tokens here resolved through GetFromMapPath, a literal
// walk of the data map, while a condition, a sink mapping or a db_lookup
// keyField resolved through evaluator.GetMsgValByPath, which also answers the
// CDC envelope (after., before.), the virtual fields (operation, table, schema)
// and meta.. The data map of a CDC message *is* its after-image, so `after.x`
// had nothing to walk and bound NULL -- while the editor's SQL builder, whose
// sample still carries the envelope, ran the same text and returned rows.
//
// The rule is supplied by the caller rather than imported, because sqlutil sits
// deliberately below the transformer packages so that sources and sinks can use
// it; importing the evaluator here would invert that.
type Resolver func(path string) any

// MapResolver resolves paths as a dotted walk of a plain map -- the behaviour
// every map-taking entry point below still has. It is the right rule for a
// caller that genuinely has no message: batch_sql binds from a `parameters`
// object on the source config, where an envelope path would mean nothing.
func MapResolver(data map[string]any) Resolver {
	return func(path string) any { return GetFromMapPath(data, path) }
}

// ParameterizeTemplateWith is ParameterizeTemplateEx with the path resolution
// rule supplied by the caller. Every entry point above reaches the statement
// through here, so this is where the list rule lives.
//
// A token whose value is a slice and which sits directly in an `IN (...)` list
// expands to one placeholder per element. Binding a slice to a single
// placeholder is not a list -- it is an encoding error on every driver
// ("unsupported type []interface {}, a slice of interface" through
// database/sql, "cannot find encode plan" through pgx) -- so `id IN ({{.ids}})`
// had no working form before this.
//
// The expansion is deliberately confined to IN lists. `= ANY({{.ids}})` is the
// native Postgres array form and already works by binding the slice whole;
// expanding it would produce `= ANY($1, $2)`, a syntax error.
func ParameterizeTemplateWith(driver, tpl string, resolve Resolver) TemplateBinding {
	p := templateParser{driver: driver, resolve: resolve, nextIdx: 1}
	return p.parse(tpl)
}

// TemplateArgsWith is TemplateArgs with the path resolution rule supplied by
// the caller.
//
// Anything deriving a cache key from a template has to use the same rule the
// statement is built with, or the key can describe a different query from the
// one that runs.
func TemplateArgsWith(tpl string, resolve Resolver) []any {
	return ParameterizeTemplateWith("", tpl, resolve).Args
}

// parenCtx records what introduced an open parenthesis, so a token inside it
// can tell whether it sits in an IN list.
type parenCtx struct {
	// keyword is the identifier immediately before the '(' , upper-cased.
	keyword string
	// sawWord is set once a letter has been written directly inside this
	// parenthesis, outside any string literal. A list body holds only
	// placeholders, literals, commas and whitespace, so a bare word means this
	// is a subquery or an expression rather than a value list.
	sawWord bool
}

// ParameterizeTemplateEx is ParameterizeTemplate with the full binding result,
// resolving paths as a walk of data. See ParameterizeTemplateWith for how a
// list token is expanded.
func ParameterizeTemplateEx(driver, tpl string, data map[string]any) TemplateBinding {
	return ParameterizeTemplateWith(driver, tpl, MapResolver(data))
}

// TemplateArgs returns the values a template's {{ ... }} tokens bind to, in
// placeholder order, resolved against data.
//
// It is the same walk ParameterizeTemplateEx performs, minus the statement
// text, so a caller that needs to know "what does this template depend on in
// this message" cannot drift from what the executed query actually binds. The
// driver only decides placeholder syntax, which no such caller cares about, so
// there is no driver argument.
//
// The use that motivated it is cache identity: a templated lookup's per-message
// input lives entirely in these values, and a cache keyed on the template text
// alone is the same key for every message.
func TemplateArgs(tpl string, data map[string]any) []any {
	return ParameterizeTemplateEx("", tpl, data).Args
}

// templateParser walks a SQL template once, emitting placeholders for {{ }}
// tokens and tracking just enough SQL structure -- string literals and the
// keyword that opened each parenthesis -- to know when a token is in a value
// list.
type templateParser struct {
	driver     string
	resolve    Resolver
	out        strings.Builder
	args       []any
	unresolved []string
	parens     []parenCtx
	quote      byte
	nextIdx    int
}

func (p *templateParser) parse(tpl string) TemplateBinding {
	if b, failed := p.walk(tpl); failed {
		return b
	}
	return TemplateBinding{SQL: p.out.String(), Args: p.args, Unresolved: p.unresolved}
}

// walk consumes the template, reporting whether it stopped early.
func (p *templateParser) walk(tpl string) (TemplateBinding, bool) {
	for i := 0; i < len(tpl); {
		if !isTokenStart(tpl, i) {
			p.scan(tpl, i)
			i++
			continue
		}
		end, ok := tokenEnd(tpl, i)
		if !ok {
			// No closing braces: the rest is not a token, so it is text.
			p.out.WriteString(tpl[i:])
			break
		}
		if err := p.bind(strings.TrimSpace(tpl[i+2 : end])); err != nil {
			return TemplateBinding{Err: err}, true
		}
		i = end + 2
	}
	return TemplateBinding{}, false
}

func isTokenStart(tpl string, i int) bool {
	return i+1 < len(tpl) && tpl[i] == '{' && tpl[i+1] == '{'
}

// tokenEnd returns the index of the "}}" closing the token that opens at i.
func tokenEnd(tpl string, i int) (int, bool) {
	for j := i + 2; j+1 < len(tpl); j++ {
		if tpl[j] == '}' && tpl[j+1] == '}' {
			return j, true
		}
	}
	return 0, false
}

// resolveToken turns a token's contents into a value: a quoted literal as
// written, anything else as a path handed to the resolver.
func (p *templateParser) resolveToken(token string) any {
	switch {
	case strings.HasPrefix(token, "'") && strings.HasSuffix(token, "'"):
		return strings.Trim(token, "'")
	case strings.HasPrefix(token, `"`) && strings.HasSuffix(token, `"`):
		return strings.Trim(token, `"`)
	}
	// allow optional source. prefix or leading dot
	path := strings.TrimPrefix(strings.TrimPrefix(token, "source."), ".")
	val := p.resolve(path)
	if val == nil {
		p.unresolved = append(p.unresolved, path)
	}
	return val
}

func (p *templateParser) bind(token string) error {
	val := p.resolveToken(token)
	if arr, ok := AsSlice(val); ok && p.inValueList() {
		if len(arr) > MaxListExpansion {
			return fmt.Errorf(
				"list variable %q has %d elements, above the %d-placeholder limit for an IN list; filter it upstream or match on a joined table instead",
				token, len(arr), MaxListExpansion)
		}
		p.bindList(arr)
		return nil
	}
	p.placeholder()
	p.args = append(p.args, val)
	return nil
}

func (p *templateParser) bindList(arr []any) {
	if len(arr) == 0 {
		// IN () is a syntax error in every dialect. A bound NULL is valid and
		// matches nothing, which is what an empty set means.
		p.placeholder()
		p.args = append(p.args, nil)
		return
	}
	for n := range arr {
		if n > 0 {
			p.out.WriteString(", ")
		}
		p.placeholder()
	}
	p.args = append(p.args, arr...)
}

func (p *templateParser) placeholder() {
	p.out.WriteString(Placeholder(p.driver, p.nextIdx))
	p.nextIdx++
}

// inValueList reports whether the innermost open parenthesis is an IN list that
// so far holds only values.
func (p *templateParser) inValueList() bool {
	if len(p.parens) == 0 {
		return false
	}
	top := p.parens[len(p.parens)-1]
	return top.keyword == "IN" && !top.sawWord
}

// markWord records that the innermost parenthesis holds a bare word, which
// means it opened a subquery or an expression rather than a value list.
func (p *templateParser) markWord() {
	if len(p.parens) > 0 {
		p.parens[len(p.parens)-1].sawWord = true
	}
}

// scan advances the literal and parenthesis state over one ordinary character
// and copies it to the output.
func (p *templateParser) scan(tpl string, i int) {
	c := tpl[i]
	switch {
	case p.quote != 0:
		if c == p.quote {
			p.quote = 0
		}
	case c == '\'' || c == '"' || c == '`':
		p.quote = c
	case c == '(':
		p.parens = append(p.parens, parenCtx{keyword: prevWord(tpl, i)})
	case c == ')':
		if len(p.parens) > 0 {
			p.parens = p.parens[:len(p.parens)-1]
		}
	case isWordLetter(c):
		p.markWord()
	}
	p.out.WriteByte(c)
}

func isWordLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isWordChar(c byte) bool {
	return isWordLetter(c) || (c >= '0' && c <= '9')
}

// prevWord returns the identifier immediately preceding index i, upper-cased,
// skipping any whitespace between the two. "NOT IN (" yields "IN"; "checkin ("
// yields "CHECKIN".
func prevWord(s string, i int) string {
	j := i - 1
	for j >= 0 && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j--
	}
	end := j + 1
	for j >= 0 && isWordChar(s[j]) {
		j--
	}
	return strings.ToUpper(s[j+1 : end])
}

// AsSlice coerces v into a slice of any so it can be expanded into a SQL value
// list. It reports false for anything that is a scalar to a database driver,
// including []byte and json.RawMessage, which are bytea/blob and JSON column
// values rather than lists.
func AsSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	switch v.(type) {
	case []byte, json.RawMessage:
		return nil, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
	default:
		return nil, false
	}
	// A named []byte type (driver-specific raw column types) is still a scalar.
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	out := make([]any, rv.Len())
	for n := 0; n < rv.Len(); n++ {
		out[n] = rv.Index(n).Interface()
	}
	return out, true
}

// GetFromMapPath resolves a dotted path in a nested map[string]any.
func GetFromMapPath(m map[string]any, path string) any {
	if m == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		if mm, ok := cur.(map[string]any); ok {
			cur = mm[p]
		} else {
			return nil
		}
	}
	return cur
}
