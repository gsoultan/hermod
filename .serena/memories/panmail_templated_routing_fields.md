# panmail: templating a connection field, and what it costs

All four of the panmail sink's previously-verbatim settings are now Go templates
over the message: `base_url`, `api_key`, `provider_id`, `template_id`
(`pkg/comm/sink/panmail/panmail.go`). The seven that were already templated —
from, to/cc/bcc, subject, html, text — are unchanged.

## The two classes are not the same change

`provider_id` and `template_id` are per-message fields on `sdk.Message`, so
templating them is wiring. The only trap is that they must be **refused when they
render empty**, not sent: `<no value>` as a provider sends from whatever the
gateway picks, and an empty template id used to fall through to `bodyFallback`,
which mails the formatter's output to a real person. `renderRequired` is the
shared guard; `bodyFallback` now takes the *rendered* template id, not
`s.cfg.TemplateID`.

`base_url` and `api_key` are different in kind, and the difference is the lesson:

**A connection field consumed at construction becomes a per-message resource the
moment you template it.** `sdk.New(baseURL, apiKey, opts...)` builds the client
once. Templated, there is one client per distinct rendered pair — and a client
per *message* would leak an `http.Client` connection pool per message, so they
have to be cached.

**That cache is then keyed on attacker-supplied input.** Both halves come from
row data. `AllowedHosts` bounds which *hosts* are reachable, not how many: one
`*.mail.example.com` rule admits unlimited subdomains, and nothing bounds the api
key at all. Bounded at `maxCachedClients = 32` with LRU eviction. The mutation
test that proves it: break the eviction loop and the bound test reports 96.

## The allowlist is the security bound, and it is mandatory

`New` refuses to start when `base_url` or `api_key` is templated and
`allowed_hosts` is empty — otherwise any row could name its own gateway and be
handed the tenant key. Exact hostname match, or a leading `*.` over a domain with
at least two labels (`*.com` is refused; it bounds nothing). The refusal error
names the host and never the key, because it gets logged. The host is checked
*before* the client is built and before the idempotency claim is taken, so an
unroutable message does not leave a claim that suppresses its own retry.

The SDK already sets `CheckRedirect = refuseRedirect`, so a gateway cannot 302
the key somewhere else.

## Idempotency: templated gateway in, static gateway out

A templated gateway is hashed into the derived key — the same mail to two
gateways is two sends, and hashing them alike suppresses the second. A **static**
gateway is deliberately left out: folding it in would change every key already
derived, orphaning the claims in the store, and a message whose claim went
missing is a message mailed twice. Two tests hold both halves. The api key is
never hashed: it is a credential and the digest is written to a database.

## Where the keys are mirrored

`allowed_hosts` is hand-mirrored in three places, the shape
[sink_form_fallthrough_and_panmail](sink_form_fallthrough_and_panmail.md) warns
about: `internal/factory/factory.go` (the panmail case), `PanmailSinkConfig.tsx`
(shown only when either field contains `{{`), and `connectorRequirements.ts` (a
`when`-gated requirement, so the wizard's Next disables). `internal/factory/
panmail_test.go` is the test that starts from the config keys the UI writes.

Note the UI label is "Gateway URL"; the config key is and always was `base_url`.

Related: [connector_conformance_suite](connector_conformance_suite.md),
[reachability_tests](reachability_tests.md).
