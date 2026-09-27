# A failed request's error quotes its URL

net/http reports a failed request as a `*url.Error` whose text is
`<Op> "<full URL>": <cause>`. It strips a userinfo password (`stripPassword`) and
nothing else. `url.Parse` failures are `*url.Error` too, and quote the raw URL —
which is how a token pasted with its trailing newline leaked before anything was
sent.

## Why it mattered here

An error does not stay in the connector. Traced 2026-09-27:

- a failed pre-flight `Ping` becomes the workflow status, `setStatus("Error: " +
  err.Error())` in `pkg/engine/runner.go`, and the registry sends that status in
  the "Workflow Error" alert to **every notification channel**
  (`registry_workflow.go`, `notificationService.Notify`);
- a source read error is logged to the log table (`runner.go`, "Source read
  error"); a sink write error goes to the log table and the OTel span;
- Test Connection returns it to the browser, and Settings → Test returns each
  alert channel's error.

So a Telegram sink whose Ping hit a DNS failure published its bot token to Slack,
Discord, email and the webhook channel at once.

## The rule

A connector whose URL carries a credential passes **both** the
`http.NewRequestWithContext` error and the `Do` error through
`httpclient.RedactURLError`. It cuts the URL to `scheme://host/[redacted]`, keeps
`Op` and the cause (so `errors.Is(err, context.DeadlineExceeded)` and `Timeout()`
still answer), and leaves every other error alone. Call it on the error net/http
returned: a wrapper has already copied the URL into its own text, so a wrapped
error comes back as the redacted `*url.Error` alone.

Who needed it: Telegram sink (token in the path), Facebook and Instagram sinks and
sources (`access_token` query; the Facebook sink's URL also carried the post text,
i.e. row data), Slack and Discord sinks in webhook mode, and all four
webhook-style alert channels in `internal/notification`. The TikTok source sent
its token as a query parameter *and* a Bearer header; the query copy is gone.

A credential in a **header** is already safe: Go's transport refuses an invalid
header value with `net/http: invalid header field value for "Authorization"` and
deliberately omits the value.

## Guards

- `pkg/comm/conformance/credentials_test.go` —
  `TestConnectorErrorsDoNotCarryCredentials` runs every social connector, every
  mode, against a dead address with a sentinel secret, typed and pasted with a
  newline. Header-auth connectors are in it on purpose: it is what stops one
  moving its token into the URL.
- `internal/factory/credential_redaction_reachability_test.go` — the same from
  stored config through `CreateSink`/`CreateSource` and the decorators. Sinks fail
  via a swapped `httpclient.DataClient.Transport`; sources, whose clients are their
  own, via the pasted-newline parse failure.
- `internal/notification/credentials_test.go` — through `NotificationSettings.Test`.

## Not covered yet

- Operator-supplied URLs (http sink/source, graphql, api_lookup, generic
  webhook source) can carry a `?key=`; the same helper applies.
- The Graph API connectors still *send* the token in the query, where proxies
  log it. Moving it to a header needs a live test against Meta first.
- Errors already stored before the fix are not scrubbed — rotation is the remedy.

## The rest of the 2026-09-27 social-connector audit

None of the social connectors had ever been run against the real API. Verified
then, and still open unless a later memory says otherwise: the UI's generic
source form writes `account_id`/`access_token` while the factory reads
`page_id`/`ig_user_id`/`person_urn` (and `token`+`query` for twitter); X posting
needs a user token that expires in 2h with no refresh; TikTok's post body lacks
the required `post_info.privacy_level`; LinkedIn's `/v2/me` Ping is
partner-only; the Facebook feed cursor parses Meta's `+0000` offset with
`time.RFC3339` and so never advances; `ackwatermark` assumes emission order is
cursor order, which newest-first APIs (X, LinkedIn; Discord per its docs) break;
the Slack source ignores `has_more`.
