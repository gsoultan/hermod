# api_lookup request bodies are resolved inside the JSON

`api_lookup` resolves its body with `evaluator.ResolveJSONTemplateMsg`
(`pkg/infra/evaluator/evaluator.go`). It falls back to `ResolveTemplateMsg`
(raw text) only when the body template is not valid JSON before resolution,
for example an unquoted `{{.after.profile}}`.

## The rule

- **Only JSON string tokens are touched.** Numbers, layout and key order are
  copied byte for byte, so `604800000000000` or a 2^53+ id is never re-encoded
  through a float.
- **A string that is exactly one whole `{{ }}` token and resolves to an object or
  array is replaced by that value.** This is how a jsonb column goes out as
  itself.
- **Every other resolved string is JSON-escaped**, and a scalar in quotes stays a
  string. `"{{.after.count}}"` with 42 still sends `"42"`, so existing bodies
  keep their types. Changing that would break APIs that expect strings.
- **Object keys holding tokens resolve as text.** `env.` resolves to nothing.
  Resolution is one forward pass: a resolved value is never re-scanned.

`Content-Type: application/json` is added when the resolved body is JSON and
the node's headers set no Content-Type (the check is case-insensitive via
`Header.Get`). A non-2xx error carries up to 256 characters of the reply
(`responseExcerpt`).

## Why

The operator's session API refused every call with a bare
`api lookup returned status 415`. There were three causes:
- no Content-Type was sent;
- a quoted jsonb field arrived as `"profile": "{"name":…}"`, which is not JSON;
- a value holding `"` broke the body the same way.

#171 had already fixed the tokens resolving to empty. Its test's mock recorded
only the body, so the header that actually failed was never asserted.

## Not covered

Other nodes that template a JSON body as text, such as HTTP/webhook sinks, have
not been audited. Grep for `ResolveTemplateMsg(` on a body.

Related: [[template_sample_shape_differs_by_path]], [[lookup_cache_fast_path]]
(the cache key digests the *resolved* body, so it follows this rule unchanged).
