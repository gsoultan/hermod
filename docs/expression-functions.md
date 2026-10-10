# Expression functions

A Set Fields or Formulas value, a condition's field, and a `{{ }}` token in a
template are expressions: `source.email`, a literal, or a call such as
`lower(source.email)`. The editor's **Insert function** button lists every
function with an example and the value the engine answers for it; that list is
`ui/src/lib/expressionFunctions.json`, and
`pkg/infra/evaluator/function_catalog_test.go` fails the build when it and the
engine disagree.

This page covers the rules every function follows and the functions added in
this release. Each function's exact behaviour, bad inputs included, is pinned by
the fixtures in `pkg/infra/evaluator/testdata/functions/`.

## Rules every function follows

- **Names are not case-sensitive.** `toInt` and `toint` are one function.
- **A call that cannot be answered is null**, never an error: too few
  arguments, a date that cannot be read, a pattern that does not compile, a
  unit or a time zone that does not exist. Wrap it in `default()` or
  `coalesce()` to supply a fallback; the preview shows the null.
- **Text is read as the sample panel shows it.** A number is its digits
  (`1704207845`, not `1.704207845e+09`), an object or a list is its JSON, and a
  missing field is empty text.
- **Arithmetic reads what is not a number as 0**, as `add` always has. `min`,
  `max` and `format_number` are the exceptions: they skip it or answer null.
- **A quoted literal has no escapes.** `'\d+'` is the three characters `\`, `d`,
  `+`, which is what a regular expression wants. A literal cannot hold its own
  quote character; use the other one.

## Null and default

| Function | Answers |
| :--- | :--- |
| `default(value, fallback)` | `value`, or `fallback` when it is missing or empty text. `0` and `false` are values. |
| `nullif(value, other)` | null when the two are equal as `eq` compares them (as text, so `0` equals `'0'`), otherwise `value`. |
| `is_null(value)` | `true` only for a missing value. |
| `is_empty(value)` | `true` for a missing value, text holding only whitespace, or an empty list or object. |

## Text

| Function | Answers |
| :--- | :--- |
| `len(value)` | Characters, not bytes. A list's item count, an object's key count. |
| `starts_with(text, prefix)`, `ends_with(text, suffix)` | `true` or `false`; case-sensitive. |
| `pad_left(text, width, [pad])`, `pad_right(...)` | Text widened to `width` characters with `pad` (a space by default), repeated and cut to fit. Never shortens. `width` must be a whole number from 0 to 10000. |
| `regex_extract(text, pattern, [group])` | The first match, or its group by number or by name (`(?P<name>...)`). Null when nothing matches. |
| `regex_replace(text, pattern, with)` | Every match replaced; `$1` or `${name}` in `with` is a group. |
| `title(text)` | Each word capitalised, the rest lowercase. A hyphen starts a word, an apostrophe does not: `Jean-Luc O'neil`. |
| `slug(text)` | Lowercase letters and digits, accents dropped, every other run one hyphen: `cafe-creme-sao-paulo`. |
| `normalize_space(text)` | Runs of whitespace made one space, trimmed. |

Patterns use Go's RE2 syntax, which matches in time linear in the text — there
is no catastrophic backtracking. A pattern is at most 1024 bytes; compiled
patterns share the condition operators' bounded cache.

## Numbers

| Function | Answers |
| :--- | :--- |
| `floor(n)`, `ceil(n)` | Rounded down, up. |
| `mod(a, b)` | The remainder, with the sign of `a`. Null when `b` is 0. |
| `pow(base, exponent)` | Null when there is no finite answer (`pow(-8, 0.5)`, `pow(10, 400)`). |
| `min(a, b, ...)`, `max(...)` | Over the values that are numbers, a list's items included. Null when there are none. |
| `clamp(n, low, high)` | `n` kept between the bounds. Null when `low > high`. |
| `format_number(n, [decimals], [locale])` | Text with separators, rounded half away from zero as `round()` rounds. |

`format_number` locales, read by language so `de-DE` and `de_DE` are `de`:

| Locale | 1234567.891 with 2 decimals |
| :--- | :--- |
| `en` (default) | `1,234,567.89` |
| `de`, `id`, `it`, `nl` | `1.234.567,89` |
| `fr` | `1 234 567,89` (U+202F narrow no-break space) |

Any other locale is null rather than silently English.

## Dates and times

A date is read as `toDate` reads one: ISO 8601, `2006-01-02 15:04:05`,
`2006-01-02`, RFC 1123, or a number of seconds since 1970, which is UTC. A date
is answered as ISO 8601 in the date's own offset, with fractions of a second
when it has them.

| Function | Answers |
| :--- | :--- |
| `date_add(date, duration)` | The date moved by a Go duration (`90m`, `1h30m`, `-15s`) in which `d` is also a day and `w` a week: `7d`, `-1w`, `1d12h`, `1.5d`. A day is 24 hours. |
| `date_diff(a, b, [unit])` | `a` minus `b` in whole units, cut towards zero. Units: `ms`, `s`/`second`, `m`/`minute`, `h`/`hour`, `d`/`day` (default), `w`/`week`, `month`, `year`. Months and years are calendar months: 9 March to 8 May is 1 month. |
| `date_trunc(date, unit)` | The start of the `second`, `minute`, `hour`, `day`, `week` (Monday), `month` or `year`, in the date's own zone. |
| `to_timezone(date, zone)` | The same instant on an IANA zone's clock: `Asia/Jakarta`, `Europe/Berlin`. `''` and `Local` are refused. The zone database is built in. |
| `parse_date(text, layout)` | Text read with a Go layout — how 2 Jan 2006 15:04:05 would be written: `02/01/2006`. |
| `weekday(date)` | ISO: Monday 1 to Sunday 7. |
| `epoch_ms(date)` | Milliseconds since 1970 UTC. |

## JSON and lists

A list is a list, or text holding a JSON list — what a text column or a body
that arrived as a string carries. The list functions are null for anything
else.

| Function | Answers |
| :--- | :--- |
| `json_get(value, path)` | The value at `path` in an object, a list or JSON text. Keys between dots, `[n]` for an item, negative from the end: `address.city`, `phones[0]`, `[2].x`. Null when the path leads nowhere. |
| `json_parse(text)` | JSON text as a value. A value that is already an object, a list or a number is returned as it is. |
| `json_stringify(value)` | JSON text, keys sorted, `<`, `>` and `&` not escaped. A missing value is `null`. |
| `array_len(list)` | The item count. |
| `array_join(list, [separator])` | Items written as `concat` writes them, joined by `,` unless told otherwise. |
| `array_contains(list, value)` | Compared as `eq` compares, so `1` and `'1'` are one item. |
| `first(list)`, `last(list)` | Null for an empty list. |

## Encoding and signing

A missing value is null, as it is in `hash`.

| Function | Answers |
| :--- | :--- |
| `base64_encode(text)` | Standard base64. |
| `base64_decode(text)` | Reads standard and URL-safe, padded or not. Null when the bytes are not UTF-8 text. |
| `url_encode(text)` | A query-string value: a space is `+`. |
| `url_decode(text)` | `+` and `%20` are spaces. Null for a broken escape. |
| `hmac_sha256(text, secret_name)` | The lowercase hex HMAC-SHA256 of `text`, keyed with the **secret of that name**. |

`hmac_sha256` never takes a key, only a secret's name, looked up the way
`secret()` looks one up: the workflow's vhost secrets first, then the server's
secret manager. A key written into the expression would sit in the workflow's
config, its exports and every preview, so there is no way to pass one: the
second argument is always read as a name, and the right key typed inline is a
name no secret has, which answers null. So does a secret that is missing or
empty. To sign a webhook body:

```text
{{hmac_sha256(source.body, "WEBHOOK_SIGNING_KEY")}}
```

with `WEBHOOK_SIGNING_KEY` saved on the vhost's **Secrets** page.
