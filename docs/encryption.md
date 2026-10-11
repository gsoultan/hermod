# Encryption

Credential encryption, master key rotation, and the field-level encrypt and decrypt transformations.

## Credential encryption and master key rotation

Connector credentials — passwords, API keys, DSNs, GCP service-account documents — are encrypted
with AES-256-GCM before they reach the metadata database, and decrypted on read. Which values count
as credentials is decided by shape rather than by an exact list (`internal/storage/configsecrets`),
so a connector added later with an `smtp_password` is covered without anyone remembering to add it.
Non-credentials that merely look like one — `s3_key` is an object path, `routing_key` is routing —
are listed explicitly as exceptions.

**Set a master key.** Without one, Hermod encrypts with a constant that is published in this
repository, which protects nobody. Set `crypto_master_key` in `db_config.yaml`, or rotate through
`PUT /api/config/crypto` (Admin only, minimum 16 characters).

**Rotation re-encrypts before it switches.** `PUT /api/config/crypto` rewrites every stored
credential under the new key first, in one transaction, and installs the key only once that has
committed. If anything cannot be re-encrypted the request fails and *nothing* is changed — including
the case where a value is already unreadable from an earlier bad rotation, because re-encrypting it
would mean writing back a blank and destroying a credential that the right key could still recover.

A value that cannot be decrypted reads back empty, never as raw ciphertext, and logs which key
failed. Handing ciphertext to a driver as though it were the password is what previously turned a
key mistake into unexplained authentication failures against the *destination* database.

Upgrades are safe: key derivation changed from truncate-and-zero-pad to SHA-256, and `Decrypt` falls
back to the old derivation, so existing data still opens and moves forward as it is rewritten.

## Decrypting data Hermod did not encrypt

The `decrypt` node has two formats, and picking the wrong one is the single most common way it
appears to do nothing.

**Envelope** (the default) only touches values an `encrypt` node wrote — the ones tagged `enc:v1:`
or `enc:v2:`. Anything untagged is passed through untouched, because midway through a rollout a
column genuinely holds a mix of sealed and plain values. The cost of that default is that a node
pointed at a column *another* system encrypted matches nothing, changes nothing, and still reports
success. Set `onPlaintext` to `fail` once a rollout is finished and that silence becomes a stopped
pipeline instead of a wrong one.

**Raw** has no envelope, so every listed field is decrypted and a value that will not decrypt is an
error. This is the format for a column owned by another application, and it requires describing that
application's scheme exactly:

| Setting | What it must match |
| :--- | :--- |
| `algorithm` | `aes-128/192/256-gcm\|cbc\|ctr\|cfb`, `chacha20-poly1305`, `xchacha20-poly1305` |
| `keyFormat` | `passphrase` (SHA-256 of the text), `raw`, `hex`, `base64`, `pbkdf2`, `scrypt` |
| `kdfSalt`, `kdfIterations`, `kdfHash` | PBKDF2 parameters — the salt is required and has no default |
| `scryptN`, `scryptR`, `scryptP` | scrypt parameters |
| `encoding` | `base64`, `base64url` or `hex` — how the column is written |
| `ivPlacement` | `prefix` when the IV/nonce is prepended to the ciphertext, `fixed` when it is a constant |
| `nonceSize` | GCM nonce length in bytes. 12 is standard; set it only to match a system using another |
| `tagPlacement` | `suffix` *(default)* when the auth tag follows the ciphertext, `prefix` when it sits between the nonce and the ciphertext |
| `aadMode` | `none` *(default)*, `value` (use `aad`), or `key` (the encryption key doubles as the AAD) |
| `aad` | The AAD string, when `aadMode` is `value`. GCM and Poly1305 modes only |

A worked example — a column written by `openssl enc -aes-256-cbc` with the IV prepended and the
whole thing base64-encoded:

```json
{
  "transType": "decrypt",
  "fields": ["ssn"],
  "algorithm": "aes-256-cbc",
  "format": "raw",
  "encoding": "base64",
  "keyFormat": "raw",
  "key": "0123456789abcdef0123456789abcdef",
  "ivPlacement": "prefix"
}
```

Two things to know before choosing raw. Encrypting into it is not idempotent — nothing marks the
value, so running the workflow twice encrypts the column twice. And CBC, CTR and CFB are
**unauthenticated**: they cannot tell an altered ciphertext from a genuine one, and a wrong key
yields plausible garbage rather than an error. Only the GCM and Poly1305 modes detect tampering.
Prefer them for data Hermod owns, and reach for the rest only to interoperate.

## Encrypting a whole JSON document

A column often holds one JSON document rather than a scalar. Decrypting it gives back a *string*
that happens to contain JSON, which is not the same as an object: no downstream node can address
into it with a dotted path, and the live preview renders it as a single escaped line instead of a
tree.

`decrypt` takes a **`parseJson`** mode:

| Mode | Behaviour |
| :--- | :--- |
| `off` *(default)* | The decrypted value stays a string, whatever it contains. |
| `objects` | A value starting with `{` or `[` that parses becomes a real object. Scalars and unparseable values are left as text. |
| `strict` | The whole value must be a JSON document, scalars included. A parse failure takes the `onError` policy. |

Parsing is opt-in because it changes a field's type, and doing that silently would reshape every
message flowing through an existing node. `objects` deliberately leaves scalars alone — a decrypted
`"12345"` is valid JSON, and turning it into a number is a schema change downstream that nobody
asked for. `strict` is the mode that *does* parse it, and the mode that complains when a column you
declared to be JSON is not.

Going the other way, `encrypt` refuses a field holding an object, because rendering a map with `%v`
produces Go syntax that decrypt would hand back as a literal string. Set **`serializeJson`** to
encode the subtree as JSON and seal it as one document:

```json
{ "transType": "encrypt", "fields": ["payload"], "key": "…", "serializeJson": true }
{ "transType": "decrypt", "fields": ["payload"], "key": "…", "parseJson": "objects" }
```

With that pair, `payload` survives a full round trip as an object, and a later node can mask or map
`payload.contact.email` directly.

## When decryption fails and you cannot tell why

An authenticated algorithm reports a wrong key, a wrong AAD and a tampered ciphertext *identically*.
That is correct — GCM genuinely cannot distinguish them — and unhelpful when you are configuring a
node against a system you do not control, because the one error covers every setting on the node.

Set **`diagnose`** on the decrypt node. On failure it decrypts once without checking the tag and
reports which half is wrong:

- *"the key is correct … but the authentication tag does not match, so this value was encrypted with
  additional authenticated data"* — the key, key format, algorithm, encoding and IV placement are all
  right. Set `aadMode`.
- *"a trial decryption did not produce plausible plaintext"* — the AAD is not the problem; check
  `key`, `keyFormat`, `algorithm`, `encoding` and `ivPlacement`.

Two limits. The trial result is **never** returned or logged — only the classification — because
handing back unauthenticated plaintext is precisely what the tag exists to prevent. And the check
decides "plausible" by looking for well-formed text, so a genuinely binary plaintext reads as
"key wrong" even when the key is right; the message says so.

Leave it off in production. It is a debugging aid, and reporting whether forged input decrypts to
something plausible is a small oracle to expose on a hot path.

### AAD, and systems that pass the key as AAD

`aadMode` makes the choice explicit rather than inferring it from whether a text box is empty, which
matters in both directions: an empty box could equally mean "no AAD" or "not filled in yet", and
switching to `none` now actually drops a value left in the field instead of leaving it quietly in
effect. Nodes written before `aadMode` existed, which set only `aad`, keep working unchanged.

`aadMode: "key"` exists because some systems pass the encryption key itself as the AAD. It buys
nothing — the key is already bound to the ciphertext by construction — but it is what their data
requires, and without the preset there is no way to discover it: the failure is identical to a wrong
key.

### Where the tag sits, and how long the nonce is

Go's `gcm.Seal` appends the authentication tag, so a Go-written value is
`nonce ‖ ciphertext ‖ tag` with a 12-byte nonce. Node's `crypto`, Java's `Cipher` and .NET's
`AesGcm` all return the tag *separately* from `getAuthTag()`, which leaves whoever wrote the storage
code to decide where it goes — and putting it in front of the ciphertext is a common choice. The
same accident produces 16-byte nonces: nothing stopped them.

Neither is detectable from the bytes. Both layouts are the same length, and both fail authentication
identically, so `tagPlacement` and `nonceSize` have to be told rather than guessed:

```json
{
  "transType": "decrypt",
  "fields": ["payload"],
  "algorithm": "aes-256-gcm",
  "format": "raw",
  "encoding": "base64",
  "keyFormat": "raw",
  "key": "…",
  "tagPlacement": "prefix",
  "nonceSize": 12
}
```

`nonceSize` applies to GCM only — the Poly1305 constructions fix their nonce as part of the
construction, and the block modes take a full 16-byte IV — and is rejected rather than ignored
elsewhere. Both settings work for writing as well as reading, so Hermod can produce data a partner
system consumes rather than only consuming theirs.

### Detecting the settings instead of guessing them

All of the above interacts: the key format decides the key bytes, the encoding decides the payload
bytes, the nonce length decides where the ciphertext starts, the tag position decides which end the
tag is on, and the AAD decides whether authentication can succeed at all. One wrong setting is
indistinguishable from all of them wrong, which makes matching an external system a search with no
signal to search by.

The decrypt node editor has a **Detect settings** panel. Paste one encrypted value, with the key
already filled in, and it tries the plausible configurations and reports the ones that actually read
it, each with a truncated preview and an **Apply** button:

```
certain   aes-256-gcm, base64 payload, raw key, 12-byte nonce, key used as AAD
          Decrypts to: {"user_id":"01a0411f-fd3…
```

Confidence is not decoration. **`certain`** means an authenticated algorithm verified its tag —
nothing else could have produced that value. **`likely`** means an unauthenticated algorithm (CBC,
CTR, CFB) produced something that looks like text, which on a short value can be coincidence; check
the preview really is your data before applying it.

The same thing is available over HTTP for scripting:

```bash
curl -sX POST localhost:8080/api/transformations/detect-decryption \
  -H 'Content-Type: application/json' \
  -d '{"sample":"<one encrypted value>","key":"<the key>"}'
```

Two limits, both stated in the failure reason rather than left to be discovered. **PBKDF2 and
scrypt are not searched** — their salt and cost parameters are inputs, not properties of the
ciphertext, and guessing a salt is a dictionary attack rather than a search, so set those by hand.
And **a fixed IV is not detectable**, because there is nothing in the payload to recover it from.

The endpoint is editor-only and grants no capability the caller lacked: it needs the key, so anyone
who can call it could already decrypt. It never echoes the key back, sends `Cache-Control:
no-store`, and truncates the preview so it cannot be used to drain a column.
