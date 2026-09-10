/**
 * The option lists for the encrypt and decrypt node editors.
 *
 * These mirror the algorithm table in `pkg/comm/transformer/security/cipher.go`.
 * A hand-mirrored list is exactly the kind of thing that drifts silently — the
 * picker offers an algorithm the backend has never heard of, and the node fails
 * at runtime instead of in the editor — so `TestUIAlgorithmListMatchesBackend`
 * in that package reads this file and fails if the two disagree. Add an
 * algorithm in both places, or neither.
 */

export interface AlgorithmOption {
  value: string
  label: string
  /** Authenticated algorithms detect tampering; the rest cannot. */
  authenticated: boolean
}

export const ALGORITHM_OPTIONS: AlgorithmOption[] = [
  // Authenticated (AEAD) — the only ones that detect a modified ciphertext.
  { value: 'aes-256-gcm', label: 'AES-256-GCM (recommended)', authenticated: true },
  { value: 'aes-192-gcm', label: 'AES-192-GCM', authenticated: true },
  { value: 'aes-128-gcm', label: 'AES-128-GCM', authenticated: true },
  { value: 'chacha20-poly1305', label: 'ChaCha20-Poly1305', authenticated: true },
  { value: 'xchacha20-poly1305', label: 'XChaCha20-Poly1305', authenticated: true },

  // Unauthenticated — present for reading and writing data that belongs to
  // systems which already chose them.
  { value: 'aes-256-cbc', label: 'AES-256-CBC', authenticated: false },
  { value: 'aes-192-cbc', label: 'AES-192-CBC', authenticated: false },
  { value: 'aes-128-cbc', label: 'AES-128-CBC', authenticated: false },
  { value: 'aes-256-ctr', label: 'AES-256-CTR', authenticated: false },
  { value: 'aes-192-ctr', label: 'AES-192-CTR', authenticated: false },
  { value: 'aes-128-ctr', label: 'AES-128-CTR', authenticated: false },
  { value: 'aes-256-cfb', label: 'AES-256-CFB', authenticated: false },
  { value: 'aes-192-cfb', label: 'AES-192-CFB', authenticated: false },
  { value: 'aes-128-cfb', label: 'AES-128-CFB', authenticated: false },
]

export const DEFAULT_ALGORITHM = 'aes-256-gcm'

/** Grouped for the Select, so the security trade-off is visible while choosing. */
export const ALGORITHM_SELECT_DATA = [
  {
    group: 'Authenticated — detects tampering',
    items: ALGORITHM_OPTIONS.filter((a) => a.authenticated).map(({ value, label }) => ({ value, label })),
  },
  {
    group: 'Unauthenticated — for compatibility only',
    items: ALGORITHM_OPTIONS.filter((a) => !a.authenticated).map(({ value, label }) => ({ value, label })),
  },
]

export function isAuthenticated(algorithm?: string): boolean {
  return ALGORITHM_OPTIONS.find((a) => a.value === (algorithm || DEFAULT_ALGORITHM))?.authenticated ?? false
}

export const KEY_FORMAT_OPTIONS = [
  {
    value: 'passphrase',
    label: 'Passphrase (SHA-256)',
    description: 'Any length, hashed to the key size. The default, and what nodes created before the picker used.',
  },
  {
    value: 'raw',
    label: 'Raw bytes',
    description: 'The key text is the key. Must be exactly the algorithm’s key length.',
  },
  { value: 'hex', label: 'Hex-encoded', description: 'Decoded from hex, then used as-is.' },
  { value: 'base64', label: 'Base64-encoded', description: 'Decoded from base64, then used as-is.' },
  {
    value: 'pbkdf2',
    label: 'PBKDF2',
    description: 'Derived from a password and salt. Use this when the key is human-chosen.',
  },
  {
    value: 'scrypt',
    label: 'scrypt',
    description: 'Memory-hard derivation from a password and salt.',
  },
]

export const KDF_FORMATS = ['pbkdf2', 'scrypt']

export const KDF_HASH_OPTIONS = [
  { value: 'sha256', label: 'SHA-256 (default)' },
  { value: 'sha512', label: 'SHA-512' },
  { value: 'sha1', label: 'SHA-1 (legacy systems only)' },
]

export const ENCODING_OPTIONS = [
  { value: 'base64', label: 'Base64', description: 'Standard alphabet, padded. The default.' },
  { value: 'base64url', label: 'Base64 (URL-safe)', description: 'The -_ alphabet.' },
  { value: 'hex', label: 'Hex', description: 'Lowercase hex.' },
]

export const FORMAT_OPTIONS = [
  {
    value: 'envelope',
    label: 'Hermod envelope (recommended)',
    description: 'Values are written as enc:v1:… / enc:v2:… so the pair can tell ciphertext from plaintext.',
  },
  {
    value: 'raw',
    label: 'Raw ciphertext',
    description: 'No marker. Required to read or write a column another system owns.',
  },
]

export const IV_PLACEMENT_OPTIONS = [
  {
    value: 'prefix',
    label: 'Random per value, prepended (recommended)',
    description: 'A fresh IV for every value, stored in front of the ciphertext.',
  },
  {
    value: 'fixed',
    label: 'Fixed IV',
    description: 'One configured IV for every value. Only for matching an external system that does this.',
  },
]

export const ON_ERROR_OPTIONS = [
  { value: 'fail', label: 'Fail the message (recommended)' },
  { value: 'skip', label: 'Skip — leave the value as it is' },
  { value: 'null', label: 'Null — clear the value' },
]

export const ON_PLAINTEXT_OPTIONS = [
  {
    value: 'passthrough',
    label: 'Pass it through (default)',
    description: 'Right during a rollout, when a column holds a mix of encrypted and plain values.',
  },
  {
    value: 'fail',
    label: 'Fail the message',
    description: 'Turns a node that has quietly stopped matching anything into a visible failure.',
  },
  { value: 'null', label: 'Null — clear the value', description: 'Drops anything that is not encrypted.' },
]

export const PARSE_JSON_OPTIONS = [
  {
    value: 'off',
    label: 'Leave it as text (default)',
    description: 'The decrypted value stays a string, whatever it contains.',
  },
  {
    value: 'objects',
    label: 'Parse JSON objects and arrays',
    description:
      'A decrypted value starting with { or [ becomes a real object. Scalars and unparseable values are left as text.',
  },
  {
    value: 'strict',
    label: 'Parse as JSON — fail if it is not',
    description:
      'The whole value must be a JSON document, including bare numbers and booleans. A parse failure takes the failure policy above.',
  },
]
