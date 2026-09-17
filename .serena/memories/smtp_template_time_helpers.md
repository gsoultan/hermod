# SMTP templates: dates, zones, and the two shapes a column arrives in

`pkg/comm/sink/smtp/template.go` gives every SMTP template a time vocabulary:
`{{ .created_at.Format "2006-01-02" }}`, `{{ .start_at.In "Asia/Jakarta" }}`,
`{{ .start_at.In (time.LoadLocation "Asia/Jakarta") }}`, plus the functions
`time.*`, `now`, `date`, `dateInZone`, `toDate`.

## The shape problem is the reason it is two types

The same column reaches a sink two ways. PostgreSQL's logical stream sends
**text** — `decodeColumnText` in `pkg/comm/source/postgres/tuple_decode.go`
returns `string(raw)` for everything but json/jsonb — while a query, sample or
polling path hands over pgx's **`time.Time`**. An operator writes one template
for both, so both had to grow the same methods:

- `Timestamp` is a **named string type**. It holds the exact text the row
  carried, so `{{.created_at}}`, `eq`, `len`, `slice` and `printf "%s"` render
  what they always rendered — wrapping in a struct would have broken every
  template that treats the column as text. Methods come on top.
- `TimeValue` wraps a driver's `time.Time` by embedding it, so it prints and
  behaves as one. Only the methods taking a zone or another time are replaced:
  `In`, `Sub`, `Before`, `After`, `Equal` accept either shape.

Recognition is layout-based and screened by `looksLikeTimestamp` (a `YYYY-MM-DD`
prefix) so a wide row does not pay a layout sweep per text column. The layouts
are pinned by `TestTemplate_PostgresTextShapes` against what a live PG 18
actually emits: a whole-hour zone writes `+07`, a staggered one `+05:30` — two
Go layouts (`Z07` and `Z07:00`) for one column type. A time-only column
(`09:30:00`) is deliberately left as text.

## Rendering: why the body no longer goes through gsmail

`gsmail@v0.4.0` parses with no function map, so `{{ time.LoadLocation ... }}` is
a *parse* error there. `setInlineBody` renders the inline body in Hermod and
keeps gsmail's `IsHTML` sniff, its html/template escaping and `ToOutlookHTML`,
so a body that uses no function is byte-identical to before. A body fetched from
**URL or S3 is still rendered by gsmail** and has the methods (they ride on the
data) but not the functions. `gsmail` v0.9.x has `Email.HTMLFuncs`/`TextFuncs`
which would close that gap, but its `SetBody` also moved HTML into `HTMLBody`
instead of `Body`, which the idempotency key hashes — the upgrade is a
migration, not a bump.

`time/tzdata` is blank-imported here: the runtime image is
`gcr.io/distroless/static-debian12`, and a zone lookup that fails at send time
fails on a machine nobody can open a shell on.

The gosec `//nolint` on the two `Parse` calls is G708 (template injection):
template text is sink configuration written by an editor, message data only ever
arrives as data, and the URL a remote template is fetched from is never itself
templated.

## The preview renders through the sink, not beside it

`POST /api/sinks/smtp/preview` (`internal/sink/transport/http/sink.go`) builds
the sink through `factory.CreateSinkForPreview` — undecorated, because the
tracing and retry wrappers hide the concrete type — and calls
`SmtpSink.BuildEmail`, the render half of `Write`. `Write` is now `buildEmail` +
the idempotency claim + the send, so there is one renderer and a preview cannot
drift from a send.

Two deliberate limits:

- **Inline templates only.** A URL or S3 template is fetched by the server and
  the preview returns what came out, so previewing one would hand anyone with
  the editor role the contents of any address the worker can reach. Refused with
  that stated.
- **The type is checked before the sink is built.** An SMTP sink connects to
  nothing until it sends; a database sink opens a pool as it is constructed.

With no sample the handler renders against `exampleRow()` and returns it, so the
modal can show what it rendered and let the operator edit it into a real row.

## The S3 template keys

The form writes `template_s3_*` and the factory used to read `s3_*`, so the S3
tab was inert: an empty `S3Config` asked S3 for bucket `""`. `smtpTemplateS3Config`
(`internal/factory/factory.go`) prefers `template_s3_*` and falls back to the
bare names. `TestCreateSink_SmtpFetchesTheTemplateTheFormPointsAt` proves the
sink uses the mapping by serving the template from an `httptest` stand-in for S3
(`Endpoint` + path style) and asserting the path that was requested — a test of
the mapping alone could not tell.

## The sink forms around it, and what was wrong with them

Fixed in the same pass, all one defect family — a key or a route that two sides name
differently, with nothing that fails when they disagree:

- `SinkForm` declared `incomingPayload` and never destructured it, so the row
  the editor already had stopped at the form's signature. It reaches the sink
  forms now, and the SMTP preview opens on it.
- `NotificationSinkConfig` served telegram, discord and slack with a branch for
  telegram and `default: return null`: **both chat forms rendered nothing**, and
  no required key gated the save. It also carried an unreachable second copy of
  the SMTP form (SinkForm maps `smtp` to `SMTPSinkConfig`), now deleted. The
  default renders a visible "no form for this sink type"; the test derives its
  type list from `configComponents` so a fourth type mapped there fails.
- Telegram's form wrote `bot_token` and the factory read `token`
  (`chatBotToken` reads either now).
- `GET /api/sinks/{id}/workflows` was called by `useSinkForm` and never
  registered: a "Request Failed — Not Found" toast on every sink edit page, and
  the "sink is in use, stop these workflows first" warning could never appear.
  The source side had it all along.

`QueueSinkConfig` has the same `default: return null`, but every type routed to
it has a branch today. It is the next one to bite.

## Two traps when verifying this live

`/sinks/{id}` is a 404; the edit route is `/sinks/{id}/edit`. And
`perf_guards_e2e.spec.ts` ("an open transformation node does not poll the
preview endpoint") fails with `Received: 0` when the dev database has
accumulated state — it passed on a clean `./scripts/dev.sh --sqlite --reset` and
failed before it, with the UI reverted to HEAD, so it is the data and not the
diff. Reset before believing that one.
