# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.0.0. Everything published before it was withdrawn — see
[The releases before this one are gone](#the-releases-before-this-one-are-gone).

## [Unreleased]

### Added — detect decryption settings from a sample value

The decrypt node's settings interact: the key format decides the key bytes, the
encoding decides the payload bytes, the nonce length decides where the
ciphertext starts, the tag position decides which end the tag is on, and the AAD
decides whether authentication can succeed at all. One wrong setting is
indistinguishable from all of them wrong, so matching an external system meant
searching that space by hand with nothing to search by. 1.2.0 made every
combination expressible; it did not make the right one findable.

**Detect settings** in the decrypt editor takes one encrypted value and the key
and reports the configurations that actually read it, each with a truncated
preview and an Apply button. `POST /api/transformations/detect-decryption` is
the same thing for scripting.

Confidence is load-bearing rather than decorative. `certain` means an
authenticated algorithm verified its tag, so the configuration is not a guess —
nothing else could have produced that value. `likely` means an unauthenticated
mode produced plausible-looking text, which on a short value can be coincidence,
and the UI says so at the point of applying it. Ranking puts proven candidates
first, and candidates differing only between the base64 and base64url alphabets
are collapsed, since offering a choice that is not a choice is noise.

Two things are deliberately not searched, and the failure reason says so instead
of leaving them to be discovered. PBKDF2 and scrypt take a salt and cost
parameters that are inputs rather than properties of the ciphertext — guessing a
salt is a dictionary attack, not a search. A fixed IV has nothing in the payload
to recover it from.

The endpoint is editor-only and grants no capability its caller lacked: it needs
the key, so anyone able to call it could already decrypt. It never echoes the key
back, sends `Cache-Control: no-store`, and truncates the preview so it cannot be
used to drain a column.

### Fixed — a source payload that was not a JSON object was silently dropped

Sources are free to deliver a bare string, a number, or bytes that are not JSON
at all: a RabbitMQ queue carrying plain text, a Kafka topic of CSV lines, a file
read in `raw` mode. Only JSON *objects* survived. Anything else reached the sink
as `{"id":"…","metadata":{…}}` — the body gone, no error logged, nothing in the
trace. `MarshalJSON` unmarshalled the payload into the output map and discarded
the error, so a payload with no fields to merge simply left nothing behind.

A payload that is not a JSON object is now preserved under a `payload` field,
holding the decoded JSON value where there is one (`42` stays a number, `[1,2]`
stays an array) and the literal text otherwise. It is addressable by
transformations and by sink templates as `{{.payload}}`.

Three consequences of the same root cause are fixed together:

- **Bodies reaching the sink.** Any sink configured with `format: json` or
  `format: cdc` now emits the body. Sinks with no `format` set were never
  affected — they publish `Payload()` bytes directly and always passed strings
  through untouched.
- **The workflow editor and message traces.** `ToMap` carried the same ignored
  error, so a string payload rendered as an empty body in the test/preview panel
  and in traces. It now agrees with `MarshalJSON`, and a test pins them together.
- **CDC messages whose payload was not JSON.** These failed to marshal at all
  (`invalid character 'h' looking for beginning of value`), failing the sink
  write rather than losing the body quietly. The `before`/`after` envelope now
  wraps such bytes instead of rejecting them.

The S3 Parquet sink needed a matching change. It refused a record it could not
build a row from by testing `Data()` for emptiness, which is exactly the
invariant that moved: a non-object payload now decodes to one synthetic field,
so the check passed the record through to the writer and the batch failed in
`WriteStop` with `interface conversion: interface {} is nil, not string` --
naming neither the record nor the reason. The guard now measures a record
against the schema's own columns, which is what it always meant.

The fix is in the message layer, so every source benefits without connector
changes. JSON object payloads serialise exactly as before. Array payloads were
already exposed under `payload`, which is why that name was widened to cover
strings and scalars rather than a new one introduced: existing `{{.payload}}`
templates keep resolving. Arrays also gain determinism — whether an array
survived used to depend on whether a transformation had read the message first.

## [1.2.0] — 2026-09-10

The `encrypt` and `decrypt` transformations added in 1.1.0 gain an algorithm
picker, and `decrypt` stops silently doing nothing on data Hermod did not write.
`enc:v1:` values written by 1.1.0 keep decrypting unchanged under the default
configuration; a regression test pins that literal wire format.

Nothing in the public Go API changed and no dependency moved, so this carries no
security fix of its own — but the bug it fixes is a security-relevant one: a
decrypt node pointed at data another system encrypted reported success while
forwarding ciphertext to the sink. If you run one, check that it is actually
decrypting rather than passing values through.

### Fixed — `decrypt` silently did nothing on data Hermod had not encrypted

In 1.1.0 the node only touched values carrying its own `enc:v1:` prefix and
passed everything else through. That is right midway through a rollout, when a
column holds a mix of sealed and plain values — and it meant that pointing the
node at a column encrypted by *another* application matched nothing, changed
nothing, and reported success, with no error and nothing in the logs. The
pipeline looked healthy while forwarding ciphertext to the destination, which is
the same class of failure as an unknown transformation type resolving to a no-op.

Setting `format` to `raw` now drops the envelope: every listed field is
decrypted, and a value that will not decrypt is an error rather than a
pass-through. Together with `ivPlacement`, `encoding` and the key formats below,
that is enough to describe what an external system actually wrote — verified in
both directions against `openssl enc -aes-256-cbc`. For operators staying on the
envelope format, the new `onPlaintext` policy (`passthrough`, the default,
`fail`, or `null`) turns the silence into a stopped pipeline once a rollout is
complete.

### Added — an algorithm, key-derivation and encoding picker

- **Fourteen algorithms.** AES-128/192/256 in GCM, CBC, CTR and CFB, plus
  ChaCha20-Poly1305 and XChaCha20-Poly1305. AES-256-GCM stays the default. GCM
  and Poly1305 are authenticated and detect a modified ciphertext; CBC, CTR and
  CFB cannot, and are offered so that data another system already wrote in them
  can be read at all. The editor says which is which at the point of choosing,
  and a Go test fails the build if the two lists ever drift apart.
- **Key derivation is selectable.** `passphrase` (SHA-256 of the text — 1.1.0's
  behaviour, and still the default), `raw`, `hex` and `base64` for key material
  of exactly the algorithm's length, and `pbkdf2` or `scrypt` for a human-chosen
  password. A raw key of the wrong length is an error rather than padded or
  truncated: padding leaves the remaining bytes known to an attacker, and
  truncating means two keys sharing a prefix encrypt identically, so an operator
  rotating between them would see success and get no rotation. PBKDF2 and scrypt
  require a salt and have no default for it.
- **Payload encoding is selectable** — `base64`, `base64url` or `hex` — and
  decoding accepts either base64 alphabet with or without padding, because
  external systems disagree about both and the difference otherwise looks
  exactly like a wrong key.
- **`enc:v2:` names its algorithm.** A value can then be read without the node
  being told how it was written, and a node explicitly configured for a
  different algorithm reports the conflict instead of failing with a generic
  authentication error. A node configured the way 1.1.0 behaved still emits
  `enc:v1:`, so a mixed-version fleet keeps working during a rollout.
- **Optional `aad`** binds additional authenticated data to the ciphertext for
  the GCM and Poly1305 modes. It is rejected for the unauthenticated modes
  rather than accepted and dropped, which would suggest a binding that does not
  exist.

Three limits are worth knowing before leaving the defaults. In `raw` format
nothing marks a value as encrypted, so encrypting into it is not idempotent —
running the same workflow twice encrypts the column twice. The optional fixed IV
exists only to match external systems that use one: it makes identical inputs
produce identical ciphertext, and under GCM or CTR reusing an IV with one key
exposes the XOR of the two plaintexts and, for GCM, the authentication key. And
CBC, CTR and CFB cannot detect tampering at all — a wrong key yields plausible
garbage rather than an error, except where CBC's padding check happens to catch
it.

### Added — decrypted JSON can become an object

A column often holds one JSON document rather than a scalar, and decrypting it
returned a *string* that happened to contain JSON. That is not the same as an
object: no downstream node could address into it with a dotted path, and the live
preview rendered it as a single escaped line instead of a tree.

`decrypt` now takes **`parseJson`** — `off` (the default), `objects`, or
`strict`. `objects` parses a value that starts with `{` or `[` and leaves
everything else as text; `strict` treats the whole value as a JSON document,
scalars included, and routes a parse failure through the existing `onError`
policy. Parsing is opt-in because it changes a field's type, and doing that
silently would reshape every message flowing through an existing node.

The split between the two modes is deliberate. A decrypted `"12345"` is valid
JSON, so a single "parse if you can" mode would quietly turn an account number
into a float — a schema change downstream that nobody asked for. `objects` never
does that; `strict` is how an operator asks for it, and is also what complains
when a column declared to be JSON is not.

`encrypt` gained the inverse, **`serializeJson`**. It previously refused a field
holding an object, because rendering a map with `%v` produces Go syntax that
decrypt would hand back as a literal string. With the opt-in the subtree is
marshalled and sealed as one document, so an object survives a full round trip
and a later node can mask or map `payload.contact.email` directly. Without it the
refusal stands, and the error now names the option that lifts it.


### Added — an explicit AAD mode, and a diagnosis for authentication failures

An authenticated algorithm reports a wrong key, a wrong AAD and a tampered
ciphertext identically. That is correct — GCM cannot distinguish them — and it
means one opaque error covers every setting on the node. Configuring a decrypt
node against a system you do not control turns into guesswork, and the guess
people reach for first is "the key must be wrong", which it usually is not.

`diagnose` on the decrypt node decrypts once **without** checking the tag and
reports which half is wrong: either the key and framing are right and the AAD is
the difference, or the trial produced nothing sensible and the AAD is not worth
looking at. The trial result is never returned or logged — only the
classification — because handing back unauthenticated plaintext is exactly what
the tag exists to prevent. It is off by default, both for that reason and
because reporting whether forged input decrypts to something plausible is a
small oracle to expose on a hot path. The check reads "plausible" as well-formed
text, so a binary plaintext reads as "key wrong" even when the key is right; the
message says so rather than overstating what it knows.

Alongside it, **`aadMode`** makes the AAD an explicit choice — `none` (the
default), `value`, or `key` — instead of inferring it from whether a text box is
empty. An empty box could equally mean "no AAD" or "not filled in yet", and the
two produce ciphertext that cannot be told apart until it fails to open;
selecting `none` now also genuinely drops a value left behind in the field
rather than leaving it quietly in effect. Nodes that set only `aad`, from before
the mode existed, keep working unchanged.

`aadMode: "key"` covers systems that pass the encryption key itself as the AAD.
It buys nothing — the key is already bound to the ciphertext by construction —
but it is what their data requires, and without the preset there is nothing to
discover it from, because the failure is identical to a wrong key.


### Added — tag placement and nonce length for AES-GCM

The last two ways an external AES-GCM value can be framed differently from Go's.
`gcm.Seal` appends the authentication tag, so a Go-written value is
nonce||ciphertext||tag with a 12-byte nonce. Node's crypto, Java's Cipher and
.NET's AesGcm all return the tag *separately*, which leaves whoever wrote the
storage code to choose where it goes — and putting it in front of the ciphertext
is a common choice. 16-byte nonces appear for the same reason: nothing stopped
them.

Neither is recoverable from the bytes. Both layouts are the same length and both
fail authentication in the same way, so `tagPlacement` (`suffix`, the default,
or `prefix`) and `nonceSize` have to be told rather than inferred. Both work for
writing as well as reading, so a pipeline can produce data a partner system
consumes instead of only consuming theirs.

`nonceSize` is GCM-only: the Poly1305 constructions fix their nonce as part of
the construction and the block modes take a full 16-byte IV, so setting it there
is rejected rather than silently ignored. It is also read by presence rather
than by value — an explicit `nonceSize: 0` is a configuration error, and a
sentinel of 0 would have quietly accepted it as "unset". The nonce length is
part of the derived-cipher cache key, because it changes the constructed AEAD
and two nodes sharing a passphrase must not share an entry across it.

Fixtures for both come from Node's crypto module rather than from this package,
so they test interoperability instead of self-consistency.


## [1.1.0] — 2026-09-09

One new capability and a set of connector-wizard fixes. Nothing in the public Go
API changed, and no dependency moved, so this carries no security fix of its own.

### Added — field-level encryption and decryption

Two transformations, `encrypt` and `decrypt`, seal and unseal named fields with
AES-256-GCM. The configured key is hashed to 256 bits, so any length works, and
encrypt and decrypt nodes must be given the same one.

Three decisions are worth stating, because each one closes a failure that the
obvious implementation leaves open:

- **Ciphertext is tagged `enc:v1:`.** Without a marker the two transformations
  cannot tell ciphertext from plaintext. Re-running a workflow would encrypt an
  already-encrypted column a second time, and decrypt could not distinguish
  "never encrypted" from "corrupt". With it, encrypt skips sealed values and
  decrypt passes untagged ones through — which is also what a column holds
  midway through a rollout. The version segment leaves room for a second scheme
  that does not strand data written under this one.
- **Every value gets a fresh random nonce.** Identical plaintexts therefore
  encrypt differently, which is the safe default and the reason an encrypted
  field cannot be used as a join or lookup key downstream.
- **Both fail closed.** A missing key or an empty field list is an error rather
  than a silent pass-through: a step asked to encrypt that quietly forwards
  plaintext is the whole failure. Decryption failures — a wrong key or an
  altered ciphertext, which GCM reports identically — fail the message by
  default; `onError` can relax that to `skip` or `null`.

There is deliberately no `*` wildcard. Mask has one, but masking every field
degrades a message where encrypting every field destroys it, keys and routing
columns included. Only scalar values can be encrypted: rendering a map with `%v`
gives Go syntax, and decrypt would hand that literal string back in place of the
object, so a field naming an object is refused rather than silently mangled.

Two operational limits worth knowing. The nonce is 96 random bits, and NIST
SP 800-38D caps a key used that way at 2^32 encryptions — reachable on a busy
pipeline, and rotation is what resets it. Values are read through the same JSON
path every other node uses, so a `[]byte` field is already its base64 form by
the time it is encrypted, and round-trips as base64.

The key is set on the node, so it is stored with the workflow definition and is
readable by anyone who can read or export that workflow. Rotating it does not
re-encrypt data already written under the old key.

### Fixed — connector wizards could not be completed

The connection step's **Next** button read config keys that nothing produced.
For a `rabbitmq_queue` source the gate required `url` and `queue`, while the
form writes `host`, `port`, `username`, `password`, `dbname` and `queue_name`
and hides the URL input once a host is set. Neither key was reachable, so the
button was dead for every RabbitMQ queue source and sink.

Test Connection succeeded at the same moment, which is what made it baffling:
`BuildConnectionString` assembles the AMQP URL from the host fields and the
factory reads `queue_name` separately, so the step genuinely worked while the
gate that guarded it asked for fields the user could not fill.

Two connectors had drifted the same way. `mqtt` required `broker` where the form
writes `broker_url`, giving the same dead button; it now also requires a topic,
because the source refuses to start without one. The `snowflake` sink required
`dsn` where both the form and the factory use `connection_string`, so its
"Required:" message named a field the form does not show.

Requirements gained an alias list, so "a server" is one requirement satisfied by
a host *or* a whole connection string, mirroring `BuildConnectionString`'s own
precedence instead of keeping a second copy of it that can drift.

Two further gaps in the same area closed with it:

- **A pasted connection string satisfied every requirement, not just the ones it
  replaces.** Any `uri` or `connection_string` cleared the whole step, so a
  MongoDB source with a URI but no database or collection advanced and failed
  later — at a screen that no longer showed the fields, which is the failure the
  gate exists to prevent. A pasted string now satisfies the host-shaped fields
  only; the factory reads database and collection from their own keys and cannot
  derive either.
- **A connection URL could override the host fields while invisible.**
  `BuildConnectionString` prefers `url` over host and port, but the RabbitMQ
  forms rendered that input only while the host was empty. A URL entered first
  kept winning from behind a field that had disappeared, so editing the host
  changed nothing and Test Connection reported on whichever server the hidden
  URL named. It now stays on screen whenever it holds a value, and its label
  says that it overrides.

### Fixed — palette categories could render under the wrong heading

Category titles are unique only within a group: "Databases", "Messaging &
Streams" and "Social Media" each name both a source and a sink group. The
palette's combined tab keyed one list by title, so three pairs collided and
React warned twelve times per render of the workflow panel. Duplicate keys let
React reuse or drop the wrong child, meaning a category's contents could appear
under another category's heading. Keys now pair the group with the title.

## [1.0.0] — 2026-09-07

The first generally available release. It is `1.0.0-rc.2` plus one concurrency
fix; no dependency changed, so it carries no security fix of its own and
`1.0.0-rc.1`'s advisory remains the current one.

### Fixed — a health pass could clear a stall it raced

`checkHealth` publishes `"running"` whenever the source is up and every sink
answers `Ping`, and a wedged sink does answer `Ping`: it accepts the connection
and never completes a write. So the stall watchdog and the health pass both
write the engine status, and the watchdog has to win.

An earlier fix guarded the write with `engStatus != "stalled"`, which closed the
case of a health tick arriving *after* a stall. It could not close the case
inside a single tick: `engStatus` came from an earlier `GetStatus`, and the
write was a separate lock acquisition, so a watchdog setting `"stalled"` between
the two was overwritten by a guard that had already decided the pipeline was
fine. A supervisor was told the workflow had stalled while the status the UI
reads said it was healthy.

The exclusion moved into the write. `StatusTracker.SetEngineStatusUnless`
decides and publishes under one lock and reports whether it wrote.

The window was only as wide as the gap between the two calls, so it never
reproduced on a developer machine and surfaced instead as an intermittent CI
failure. Both halves of the fix now have a test that fails without it: the
tracker races 2000 pairs of writers, and the engine-level test races the
watchdog against `checkHealth` 300 times.

### The version number, and what it does not buy you

`1.0.0` is the release you can run: the container image, the Helm chart, the
binaries and the git tag were all free at this number.

It is **not** installable with `go get`, and no future release can make it so.
`proxy.golang.org` is immutable and permanently maps `v1.0.0` to the February
commit that carried that tag, under `module github.com/user/hermod` — a path
matching no repository. That is unchanged from
[`go get` does not work at this version, by choice](#go-get-does-not-work-at-this-version-by-choice),
and the `retract` block in `go.mod` deliberately still covers `v1.0.0`:
narrowing it would un-retract the February commit without making this one
reachable, and would break the plain `go get` that currently resolves cleanly to
the newest candidate.

Consume this release as an image, a chart or a binary.

### Known gaps

Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it was
addressed here. The five social connectors that advance their cursor on read —
Twitter/X, LinkedIn, Facebook, Instagram and TikTok — remain the most
significant: treat a restart as potentially lossy for those.

## [1.0.0-rc.2] — 2026-09-07

The second release candidate. Almost all of it is the editor UI: a measured pass
over rendering, navigation and forms, plus the first-run defect that made a
fresh install unable to start a workflow without a restart. No dependency
changed, so nothing here carries a security fix; `1.0.0-rc.1`'s advisory
remains the current one.

### Fixed — a fresh install could not run anything until you restarted it

A first run has no database, so `shouldStartWorker`
(`cmd/hermod/worker_util.go`) was false at process start and `main` built no
worker. Setup then opened the database the admin chose but left the registry
holding `nil`, so every toggle failed with `registry storage is not
initialized`, with nothing on screen to say a restart was needed. Every
`dev.sh --reset` stack and CI's E2E job ran in that state.

Setup now announces the database it opened and `main` answers by starting a
worker — last, after every setup step has succeeded, and behind a `sync.Once`
so a second call cannot put two workers on the same workflows.

Two data races surfaced alongside it, both on state that is now swapped while
the process runs. `Registry.SetStorage` took `r.mu`, but 57 of the 70 reads of
`r.storage`/`r.logStorage` took nothing, on request paths and on the stats and
retention tickers. `r.mu` could not be the fix — `StartWorkflow` holds it and
calls `ValidateWorkflow`, which reads storage, and `sync.RWMutex` is not
reentrant — so the two fields moved to their own `storeMu` behind `store()` and
`logStore()`. `Handler.Worker` gained the same treatment.

### Fixed — the editor did work on every keystroke and every render

- **Preview ran once a second while idle.** `usePreviewTransformation` spread a
  mutation result into a fresh object each render, so the effect debouncing it
  re-armed its timer every render and the 1s debounce behaved as a 1s poll.
  Measured at 4 requests in 5.6s with no user input.
- **Column discovery queried the sink's own database on every keystroke of a
  table name.** The `mappings.length === 0` guard only closes once a discovery
  succeeds, and a partial table name is not a table, so typing `orders` issued
  six live queries.
- **Raw-JSON panes discarded what you were typing.** They were controlled off
  the node config, so half-typed JSON failed to parse, committed nothing, said
  nothing, and the next unrelated re-render replaced the text with the last
  serialised config. A keystroke that did parse came back reformatted, moving
  the caret to the end.
- **Lists blanked between keystrokes.** No query set `placeholderData` and only
  `WorkflowDetailPage` debounced, so each character minted a query key, data
  dropped to `undefined`, and the table emptied and refilled. Logs did this
  while polling every 5s. Now debounced at 300ms with the previous result held,
  across Workflows, Sources, Sinks, Users, Logs and Audit Logs.
- **`FlowCanvas` merged edges with `JSON.parse(JSON.stringify(...))`.** It read
  as a deep-equality guard and did the opposite: a new object per edge per
  change, so React Flow's memoisation never held and every edge re-rendered
  whenever any one did — several times a second under live telemetry. It also
  silently mangled anything JSON cannot carry.
- **`SinkForm` fetched workers with a bare `useEffect`** — no cache, no dedupe,
  no abort, a request per mount, and a `setState` after unmount if the form
  closed mid-flight. `SourceForm` already read the same list through React
  Query, so one resource had two mechanisms.
- **Seven transformation types rendered their configuration twice.** Config
  moved to a registry but the inline blocks it replaced were never deleted, so
  set, advanced, pipeline, lua, wasm, foreach and aggregate showed their
  settings under both Configuration and Advanced, with stale labels the second
  time. 288 inline lines removed.
- **Forms hydrated over your edits.** `useSourceForm` re-parsed `initialData`
  on every keystroke, restoring a sample `updateConfig` had just cleared;
  `UserForm` and `VHostForm` re-hydrated on every `initialData` identity, so a
  refetch after their own save overwrote in-progress edits.
- Routing nodes previewed the `{branch, result}` envelope instead of the
  message, and `WorkflowsPage` crashed on a non-array `/api/workspaces`
  response.

### Changed — navigation, first paint and layout

- **Cold loads no longer flash white.** The app renders dark by default but the
  theme was applied only after hydration, and `index.html` set no background.
  It now inlines Mantine's own `ColorSchemeScript` algorithm, a `color-scheme`
  meta and matching `theme-color`, and `#root` holds a shell placeholder. First
  paint measured at `rgb(26,27,30)`.
- **Navigation stopped paying a fixed cost.** Router `defaultPreload: 'intent'`
  warms a route's lazy chunk on hover instead of fetching chunk then data in
  series; `defaultPendingMinMs` drops 500 → 0 and `defaultPendingMs` rises
  0 → 300, so a warm route that paints in 20ms no longer costs a pinned 500ms
  full-viewport spinner.
- **The manual chunk buckets are gone**, measured rather than assumed. The
  `reactflow` rule matched a package renamed to `@xyflow/react` long ago, so it
  only ever caught dagre and d3 — and fixing the name made it worse, promoting
  the graph library and drag-and-drop kit onto the login screen's critical path
  at 1.45MB. Rolldown's own splitting tracks the eager/lazy boundary properly.
- **One width grammar for every form.** A measured audit found five different
  grammars across six forms. `<Group grow>` was the broken one — a flex row
  that never wraps, squeezing host/port/user/password to ~80px on a narrow
  viewport instead of stacking. 72 of these became `FormRow`, a `SimpleGrid`
  that does collapse and bottom-aligns, replacing a global `min-height: 1.2em`
  that only ever reserved one line.
- The editor toolbar at 390px was a non-wrapping flex row; it wraps now.
- Two theme blocks were silently dead: Mantine `styles` become inline styles,
  so nested selectors in them were not CSS rules — React rejected them and
  logged an error every render. Active nav links rendered at weight 400 instead
  of 600 and table headers had no background in either scheme.

### Added

- **Search in the workflow palette.** It lists well over a hundred sources,
  sinks and transformations across three tabs and a dozen categories, and the
  only way to find one was to scroll. Labels, sub-types and descriptions are
  all searched — so "drop records" finds Filter — ignoring spaces, underscores
  and slashes, because the catalogue is not consistent about them. A tabbed
  palette hides matches by design, so an empty tab reports where they are, and
  says so rather than offering a link when the target tab is locked.
- **The PWA is finished**: an explicit manifest `id` (it was derived from
  `start_url`, so changing that later would have orphaned every install),
  192/512 PNG icons, a maskable icon inside the inner 80% safe zone, and an
  `apple-touch-icon`, which iOS needs because it ignores the manifest for Add
  to Home Screen.
- **Accessible names that describe the right action.** A bulk find-and-replace
  had given icon buttons the wrong ones: copy-to-clipboard announced "Confirm",
  generate-password announced "Refresh", clear-stream announced "Delete", and
  ten delete buttons in a table all announced "Delete" with nothing to say
  which row. 26 corrected.
- **A bundle budget in CI.** `check-bundle-budget.mjs` sums every script and
  stylesheet `index.html` references and fails past 781kB; measured 736kB. One
  eager import of a page-only library puts it back to the 1,092kB it was before
  the buckets came out, with nothing else in CI noticing.
- Editor measurement scripts (`measure-editor.mjs`, `profile-editor-cpu.mjs`,
  `visual-sweep.mjs`) used to settle the memory and worker-offload questions
  rather than argue them. The apparent +17MB peak on the new code was GC sample
  timing: 4.55 vs 4.35MB sampled over 30s, identical composition.

### Developer experience

- `scripts/dev.sh` chooses its ports rather than assuming they are free. It
  prefers 4005 (API), 50051 (gRPC) and 5175 (UI) and steps up when one is
  taken, so a second stack no longer fails with `address already in use`. The
  gRPC port was previously unmanaged, and a failed bind on it is fatal for the
  whole process.

### Known gaps

- Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it
  was addressed here.
- The 239kB stylesheet costs 36.8kB over the wire with FCP at 44ms on the
  production build. Splitting it was measured and judged not worth the work,
  and is recorded rather than done.

## [1.0.0-rc.1] — 2026-09-03

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.0.0` follows once it has.

### Security

- **`golang.org/x/crypto` 0.54.0 → 0.56.0**, for [GO-2026-6354] and
  [GO-2026-6355] — two denial-of-service defects in `golang.org/x/crypto/ssh`
  where a channel can deadlock on an established connection. Both are reachable
  from the SFTP file source, at `pkg/comm/source/file/generic.go:736`, where
  `sshDialContext` calls `ssh.NewClientConn`.

  The connection is outbound, so reaching it means Hermod has been configured to
  poll an SFTP server that is hostile or has been compromised — not an exposure
  anyone can reach unprompted. It is still reachable, which is why this is an
  upgrade rather than an exemption.

  The handshake was already bounded by a deadline, cleared immediately
  afterwards so transfers are not cut short. That is exactly the window these
  advisories describe, so the timeout does not mitigate them.

  `golang.org/x/text` (0.40.0 → 0.41.0) and three indirect `golang.org/x`
  modules moved with it.

[GO-2026-6354]: https://pkg.go.dev/vuln/GO-2026-6354
[GO-2026-6355]: https://pkg.go.dev/vuln/GO-2026-6355

### The releases before this one are gone

Every earlier release has been deleted: 52 GitHub releases, all 55 tags, and
both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-03 is available any more, and none of it should be
treated as a supported upgrade path to this release.

**Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
`:latest`, and the matching `charts/hermod` versions no longer resolve. If you
are running one, keep your local copy until you have moved to `1.0.0-rc.1`; it
cannot be pulled again.

### `go get` does not work at this version, by choice

The version numbering restarts here, and that is possible everywhere except one
place: `proxy.golang.org` is immutable, and it still maps `v1.0.0` to the commit
that carried that tag in February, under the old `module github.com/user/hermod`
which matched no repository.

```
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.info
{"Version":"v1.0.0","Time":"2026-02-09T07:38:40Z","Hash":"915f5346..."}
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.mod
module github.com/user/hermod
```

Re-tagging does not change what the proxy serves. Every number from `v1.0.0` to
`v1.7.4` is spent for this module path, and `v1.0.0` has to stay retracted — if
it did not, `go get` would resolve to that February commit and fail on the
module path anyway.

The consequence, stated plainly: **Hermod is not currently consumable as a Go
module.** `go get github.com/gsoultan/hermod` and
`go install github.com/gsoultan/hermod/cmd/hermod@…` do not work at `1.0.0`, and
will not until the line passes `v1.7.4`. The container image, Helm chart,
GitHub release and packaged binaries are unaffected and are the supported ways
to run it. Building from a checkout also works.

This was a deliberate trade: version numbers that read correctly everywhere a
user actually installs Hermod, against a `go get` path that had never worked in
any published version anyway — the module path was a placeholder until
2026-09-02, so no release before this one could be imported either.

### Why the withdrawn versions are retracted

Two tombstones exist: `v1.7.4` and `v1.8.0`. A tombstone is a tag holding a
`retract` directive and nothing else — no code, no image, no chart. Both are
listed in `.github/tombstones`, which is how the release workflow knows to
publish no artifacts for them.

They exist because Go reads retractions from the go.mod of the **highest release
version**, which makes retracting anything require publishing something above
it. `v1.7.4` was cut to retract the withdrawn `1.x` line. `v1.8.0` was cut
because that turned out not to be enough:

- `v1.8.0-rc.1`, an intermediate candidate withdrawn for the `x/crypto` SSH
  defects, sorts *above* `v1.7.4`, so no directive in `v1.7.4` could reach it.
  `go get github.com/gsoultan/hermod` selected it and installed the vulnerable
  build without complaint.
- `v1.0.0-rc.1` sorts *below* `v1.0.0`, since a pre-release precedes its
  release, so the `[v1.0.0, v1.7.4]` range never covered it either. It needs a
  directive naming it, and that directive has to live in the highest release —
  not in `v1.0.0-rc.1` itself, which is a pre-release and therefore not where Go
  looks.

The retraction is now `[v1.0.0, v1.8.0]` plus `v1.0.0-rc.1` by name, carried by
the `v1.8.0` tombstone. Between them they cover every version the proxy can
serve for this module path.

`v1.0.0-rc.1` — this release — is itself retracted, which is unusual and
deliberate. The proxy holds that version string against an older commit from an
earlier attempt at this reset, and re-tagging cannot displace it, so a Go user
asking for it would receive code without the SSH denial-of-service fix above.
Failing loudly is the better outcome. The image, chart and binaries are freshly
built from this commit, carry no such history, and are published normally.

### Breaking

- **The module path is now `github.com/gsoultan/hermod`.** It was
  `github.com/user/hermod`, a placeholder that matched no repository, so
  `go get`, `go install` and importing Hermod as a library all failed with a
  path mismatch regardless of which version you asked for. Nothing could import
  Hermod at any version, so there was no importer for the change to break —
  which is why it happens here rather than being carried forward.
- **Go 1.27 is required to build.** `go.mod` declares `go 1.27.0`.
- **`hermod.TwoPhaseCommit.Prepare` takes a transaction ID:**
  `Prepare(ctx context.Context, txID string) (string, error)`. The coordinator
  now names a transaction and records the name *before* a participant is asked
  to hold it, so a crash between those two steps leaves a name that recovery can
  look for. Previously the participant chose the name and returned it, and a
  crash in that window left a prepared transaction pinned in the database with
  nothing on record pointing at it. In-tree this affects only the PostgreSQL
  sink; any out-of-tree implementation needs the new parameter.
- **Pebble is refused as a metadata store.** `--db-type=pebble` now exits with an
  explanation instead of starting. It never satisfied the metadata store's
  requirements, and previously reported itself as configured while failing to be
  one.

### Changed behaviour you will notice in production

These are corrections, but each one changes something an operator can see. None
of them need action; all of them are worth reading before you deploy.

- **A failed dead-letter park no longer counts as a delivery.** When a message
  could not be delivered *and* could not be written to the DLQ, the engine
  acknowledged it anyway and the record was gone. It is now retained. On a
  deployment with a misconfigured or unreachable DLQ this appears as replication
  slot or queue growth that was not there before — that growth is the data the
  previous behaviour was discarding.
- **Resume cursors advance on acknowledgement, not on read**, across thirteen
  database and OData sources (including SQLite, Oracle, MongoDB, Dynamics 365 and
  SAP). A crash between reading a batch and the sinks writing it no longer skips
  those rows on restart. The trade is at-least-once behaviour where the previous
  code was accidentally at-most-once: a restart may now redeliver a batch that
  was already written. Sinks with upsert semantics absorb this; append-only sinks
  may see duplicates.
- **Outbound HTTP requests time out.** Requests that previously used a client
  with no timeout are now bounded, and WebAssembly modules are fetched through
  the client that carries the SSRF guard. A remote host that accepts a connection
  and never answers now fails the request instead of holding a worker forever.
- **Per-message delivery logging moved from `Info` to `Debug`.** Steady-state log
  volume drops sharply. Raise the level if you were counting those lines.

### Hardening

- The HTTP API server has read-header, idle and header-size limits; it had none,
  and was answerable to a slowloris client.
- The gRPC server has a concurrent-stream ceiling, a receive-size limit, an idle
  timeout and a keepalive enforcement policy; it had none.
- Oracle and Snowflake identifiers are quoted the way those dialects fold case
  (upper), rather than the way PostgreSQL does (lower).
- `scripts/security-check.sh` fails when a `-run` pattern matches no tests, so a
  renamed test can no longer turn a security claim into a green tick that checks
  nothing.
- CI runs the browser security specs, the race detector within the runner's
  memory budget, and `govulncheck` in a job with the swap it needs.

### Added

- MQTT source, tested against a real broker and promoted to GA.
- Live-server integration tests for the Oracle sink and source, and the MSSQL
  sink against Azure SQL Edge.
- The UI moved to Tailwind v4; a pasted URL can configure a database connector,
  and transformation nodes explain themselves in the form.

### Known gaps

Stated here rather than discovered later. All three are also in `README.md` or
`SECURITY.md`.

- **Snowflake is the one identifier fix never watched failing against a real
  server.** No warehouse is reachable from CI, so the Snowflake half of the
  case-folding fix is inference from documented behaviour rather than
  observation. See `SECURITY.md`.
- **The MSSQL source lacks coverage, not capability.** It reads `CHANGETABLE`
  and emits updates and deletes, but no live SQL Server runs in CI.
- **Five social connectors still advance their cursor on read** — Twitter/X,
  LinkedIn, Facebook, Instagram and TikTok. Each drives a vendor pagination token
  whose semantics differ per API, and no test here can exercise them, so they
  were left alone rather than changed mechanically. Treat a restart as
  potentially lossy for these. They are Experimental in `README.md`.

[1.1.0]: https://github.com/gsoultan/hermod/releases/tag/v1.1.0
[1.0.0]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0
[1.0.0-rc.2]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.2
[1.0.0-rc.1]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.1
