import {
  Accordion,
  Alert,
  Card,
  Group,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  TagsInput,
  Text,
  TextInput,
  ThemeIcon,
  rem,
} from '@mantine/core'
import { useMemo } from 'react'
import {
  IconAlertTriangle,
  IconBraces,
  IconInfoCircle,
  IconKey,
  IconLock,
  IconLockOpen,
  IconShieldOff,
} from '@tabler/icons-react'
import {
  AAD_MODE_OPTIONS,
  ALGORITHM_SELECT_DATA,
  DEFAULT_ALGORITHM,
  ENCODING_OPTIONS,
  FORMAT_OPTIONS,
  IV_PLACEMENT_OPTIONS,
  KDF_FORMATS,
  KDF_HASH_OPTIONS,
  KEY_FORMAT_OPTIONS,
  ON_ERROR_OPTIONS,
  ON_PLAINTEXT_OPTIONS,
  PARSE_JSON_OPTIONS,
  isAuthenticated,
} from './cipherOptions'

interface EncryptConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  availableFields?: any[]
  transType?: string
}

/** The description of the currently selected option, shown under its control. */
function describe(options: { value: string; description?: string }[], value: string): string | undefined {
  return options.find((o) => o.value === value)?.description
}

/**
 * Configures both the `encrypt` and `decrypt` transformations.
 *
 * They take the same options and differ only in direction, so one editor serves
 * both; `transType` decides the wording and whether the two failure-policy
 * controls are shown (they only apply to decryption).
 *
 * The layout is deliberately tiered. A node that only has to protect a column
 * inside Hermod needs the first card and nothing else — the defaults are
 * AES-256-GCM in the Hermod envelope. Everything needed to read or write a
 * column that another system owns lives under "Format & interoperability",
 * because getting there requires matching that system exactly and every one of
 * those controls is a way to get it subtly wrong.
 */
export function EncryptConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  transType,
}: EncryptConfigProps) {
  const decrypting = transType === 'decrypt'
  const set = (patch: Record<string, any>) => updateNodeConfig(nodeId, patch)

  const algorithm: string = config.algorithm || DEFAULT_ALGORITHM
  const keyFormat: string = config.keyFormat || 'passphrase'
  const format: string = config.format || 'envelope'
  const encoding: string = config.encoding || 'base64'
  const ivPlacement: string = config.ivPlacement || 'prefix'
  const parseJson: string = config.parseJson || 'off'
  // Nodes saved before this control existed carry only `aad`, where a
  // non-empty value was the only way to ask for one. Infer the mode so such a
  // node shows what it is actually doing instead of reading as "None".
  const aadMode: string = config.aadMode || (config.aad ? 'value' : 'none')

  const authenticated = isAuthenticated(algorithm)
  const usesKDF = KDF_FORMATS.includes(keyFormat)
  const rawFormat = format === 'raw'

  const fieldPaths = useMemo(
    () => (availableFields || []).map((f) => (typeof f === 'string' ? f : f.path)).filter(Boolean),
    [availableFields],
  )

  // The backend accepts a single `field`, a `fields` array, or a comma-separated
  // string. The editor always writes the array and folds any legacy single
  // `field` into it, so a node configured before this control existed keeps
  // working and is migrated the first time it is edited.
  const fields: string[] = useMemo(() => {
    if (Array.isArray(config.fields)) return config.fields
    if (typeof config.fields === 'string' && config.fields.trim()) {
      return config.fields.split(',').map((f: string) => f.trim()).filter(Boolean)
    }
    if (typeof config.field === 'string' && config.field.trim()) return [config.field.trim()]
    return []
  }, [config.fields, config.field])

  return (
    <Stack gap="md">
      <Card withBorder radius="md" p="md">
        <Group gap="xs" mb="sm">
          <ThemeIcon variant="light" color="violet" size="sm">
            {decrypting ? <IconLockOpen size={rem(14)} /> : <IconLock size={rem(14)} />}
          </ThemeIcon>
          <Text fw={500} size="sm">
            {decrypting ? 'Fields to decrypt' : 'Fields to encrypt'}
          </Text>
        </Group>

        <Stack gap="sm">
          <TagsInput
            label="Fields"
            data={fieldPaths}
            value={fields}
            onChange={(next) =>
              // `field` is cleared so the two keys can never disagree.
              set({ fields: next, field: undefined })
            }
            placeholder="ssn, user.email"
            description={
              decrypting
                ? 'Dotted paths address nested values, for example user.email. List the same fields the encrypt node was given.'
                : 'Dotted paths address nested values, for example user.email. There is no wildcard: encrypting every field would destroy keys and routing columns.'
            }
            size="sm"
            clearable
          />

          <Select
            label="Algorithm"
            data={ALGORITHM_SELECT_DATA}
            value={algorithm}
            onChange={(val) => set({ algorithm: val || DEFAULT_ALGORITHM })}
            size="sm"
            allowDeselect={false}
            description={
              decrypting
                ? 'Must match how the value was encrypted. Values in the Hermod enc:v2: envelope name their own algorithm and are read with it.'
                : 'AES-256-GCM unless you are matching an existing system.'
            }
          />

          {!authenticated && (
            <Alert icon={<IconShieldOff size="1rem" />} color="red" variant="light">
              <Text size="sm">
                This mode is <strong>not authenticated</strong>. It cannot tell a modified ciphertext
                from a genuine one: an altered value decrypts to altered plaintext and the node
                reports success. A wrong key produces garbage rather than an error
                {algorithm.endsWith('-cbc') ? ', except when the padding happens to be invalid' : ''}.
                Choose it only to interoperate with data that already exists in it.
              </Text>
            </Alert>
          )}

          <PasswordInput
            label="Encryption key"
            value={config.key || ''}
            onChange={(e) => set({ key: e.currentTarget.value })}
            placeholder="Key or passphrase"
            description="Encrypt and decrypt nodes must use exactly the same key and key format."
            size="sm"
            leftSection={<IconKey size={rem(16)} />}
          />

          <Select
            label="Key format"
            data={KEY_FORMAT_OPTIONS.map(({ value, label }) => ({ value, label }))}
            value={keyFormat}
            onChange={(val) => set({ keyFormat: val || 'passphrase' })}
            size="sm"
            allowDeselect={false}
            description={describe(KEY_FORMAT_OPTIONS, keyFormat)}
          />

          {usesKDF && (
            <>
              <TextInput
                label="KDF salt"
                value={config.kdfSalt || ''}
                onChange={(e) => set({ kdfSalt: e.currentTarget.value })}
                placeholder="Required"
                size="sm"
                description="Must match the salt the data was encrypted with, byte for byte. There is no default: an empty salt would silently weaken every value."
              />
              {keyFormat === 'pbkdf2' ? (
                <Group grow align="flex-start">
                  <NumberInput
                    label="Iterations"
                    value={config.kdfIterations ?? 600000}
                    onChange={(val) => set({ kdfIterations: val })}
                    min={1}
                    step={10000}
                    size="sm"
                    description="600,000 is the OWASP guidance for SHA-256. Lower it only to match existing data."
                  />
                  <Select
                    label="PBKDF2 hash"
                    data={KDF_HASH_OPTIONS}
                    value={config.kdfHash || 'sha256'}
                    onChange={(val) => set({ kdfHash: val || 'sha256' })}
                    size="sm"
                    allowDeselect={false}
                  />
                </Group>
              ) : (
                <Group grow align="flex-start">
                  <NumberInput
                    label="N"
                    value={config.scryptN ?? 32768}
                    onChange={(val) => set({ scryptN: val })}
                    min={2}
                    size="sm"
                    description="Cost. Must be a power of two."
                  />
                  <NumberInput
                    label="r"
                    value={config.scryptR ?? 8}
                    onChange={(val) => set({ scryptR: val })}
                    min={1}
                    size="sm"
                    description="Block size."
                  />
                  <NumberInput
                    label="p"
                    value={config.scryptP ?? 1}
                    onChange={(val) => set({ scryptP: val })}
                    min={1}
                    size="sm"
                    description="Parallelism."
                  />
                </Group>
              )}
            </>
          )}

          {decrypting && (
            <Select
              label="On decryption failure"
              data={ON_ERROR_OPTIONS}
              value={config.onError || 'fail'}
              onChange={(val) => set({ onError: val || 'fail' })}
              size="sm"
              allowDeselect={false}
              description="Applies when a value cannot be decrypted — a wrong key, a wrong algorithm, or an altered ciphertext."
            />
          )}

          {decrypting ? (
            <Select
              label="Decrypted value"
              data={PARSE_JSON_OPTIONS.map(({ value, label }) => ({ value, label }))}
              value={parseJson}
              onChange={(val) => set({ parseJson: val || 'off' })}
              size="sm"
              allowDeselect={false}
              description={describe(PARSE_JSON_OPTIONS, parseJson)}
            />
          ) : (
            <Switch
              label="Seal objects and arrays as JSON"
              checked={!!config.serializeJson}
              onChange={(e) => set({ serializeJson: e.currentTarget.checked })}
              size="sm"
              description="Off, a field holding an object is refused rather than stringified into Go syntax. On, the subtree is encoded as JSON and encrypted as one document — pair it with “Parse JSON objects and arrays” on the decrypt node."
            />
          )}
        </Stack>
      </Card>

      {decrypting && parseJson !== 'off' && (
        <Alert icon={<IconBraces size="1rem" />} color="blue" variant="light">
          <Text size="sm">
            The decrypted document becomes a real object, so the live preview shows it as a tree
            instead of one escaped line, and a downstream node can address into it with a dotted
            path — <Text span ff="monospace">payload.contact.email</Text>.
            {parseJson === 'objects'
              ? ' Scalars keep their type: a decrypted "12345" stays the string "12345".'
              : ' Strict mode parses scalars too, so a decrypted "12345" becomes the number 12345.'}
          </Text>
        </Alert>
      )}

      <Accordion variant="contained" radius="md">
        <Accordion.Item value="interop">
          <Accordion.Control icon={<IconInfoCircle size={rem(16)} />}>
            <Text size="sm" fw={500}>
              Format &amp; interoperability
            </Text>
            <Text size="xs" c="dimmed">
              {[
                rawFormat
                  ? `Raw ${encoding} ciphertext, IV ${ivPlacement === 'fixed' ? 'fixed' : 'prepended'}`
                  : 'Hermod envelope',
                authenticated && aadMode !== 'none'
                  ? `AAD: ${aadMode === 'key' ? 'the encryption key' : 'a fixed value'}`
                  : null,
                decrypting && config.diagnose ? 'explaining failures' : null,
              ]
                .filter(Boolean)
                .join(' · ')}
            </Text>
          </Accordion.Control>
          <Accordion.Panel>
            <Stack gap="sm">
              <Select
                label="Value format"
                data={FORMAT_OPTIONS.map(({ value, label }) => ({ value, label }))}
                value={format}
                onChange={(val) => set({ format: val || 'envelope' })}
                size="sm"
                allowDeselect={false}
                description={describe(FORMAT_OPTIONS, format)}
              />

              {rawFormat && (
                <Alert icon={<IconAlertTriangle size="1rem" />} color="orange" variant="light">
                  <Text size="sm">
                    Raw ciphertext carries no marker, so nothing can tell it apart from plaintext.
                    {decrypting
                      ? ' Every listed field is decrypted, and a value that cannot be decrypted is an error rather than a silent pass-through — which is the point of this mode.'
                      : ' An encrypt node cannot skip a value it already encrypted, so running this workflow twice over the same column will encrypt it twice.'}
                  </Text>
                </Alert>
              )}

              <Select
                label="Payload encoding"
                data={ENCODING_OPTIONS.map(({ value, label }) => ({ value, label }))}
                value={encoding}
                onChange={(val) => set({ encoding: val || 'base64' })}
                size="sm"
                allowDeselect={false}
                description={describe(ENCODING_OPTIONS, encoding)}
              />

              <Select
                label="IV / nonce"
                data={IV_PLACEMENT_OPTIONS.map(({ value, label }) => ({ value, label }))}
                value={ivPlacement}
                onChange={(val) => set({ ivPlacement: val || 'prefix' })}
                size="sm"
                allowDeselect={false}
                description={describe(IV_PLACEMENT_OPTIONS, ivPlacement)}
              />

              {ivPlacement === 'fixed' && (
                <>
                  <TextInput
                    label="Fixed IV"
                    value={config.iv || ''}
                    onChange={(e) => set({ iv: e.currentTarget.value })}
                    placeholder={`Encoded as ${encoding}`}
                    size="sm"
                    description="Decoded with the payload encoding above, and must be exactly the algorithm's IV length."
                  />
                  <Alert icon={<IconAlertTriangle size="1rem" />} color="red" variant="light">
                    <Text size="sm">
                      A fixed IV means identical inputs produce identical ciphertext, which leaks
                      which rows share a value. With GCM or CTR it is worse than that: reusing an IV
                      under the same key exposes the XOR of the two plaintexts, and for GCM it also
                      exposes the authentication key. Use this only to match a system that already
                      does it.
                    </Text>
                  </Alert>
                </>
              )}

              {authenticated && (
                <>
                  <Select
                    label="Additional authenticated data"
                    data={AAD_MODE_OPTIONS.map(({ value, label }) => ({ value, label }))}
                    value={aadMode}
                    onChange={(val) => set({ aadMode: val || 'none' })}
                    size="sm"
                    allowDeselect={false}
                    description={describe(AAD_MODE_OPTIONS, aadMode)}
                  />
                  {aadMode === 'value' && (
                    <TextInput
                      label="AAD value"
                      value={config.aad || ''}
                      onChange={(e) => set({ aad: e.currentTarget.value })}
                      placeholder="e.g. a tenant id"
                      size="sm"
                      description="Not encrypted, but bound to the ciphertext: a value sealed under one AAD will not open under another."
                    />
                  )}
                </>
              )}

              {decrypting && (
                <Switch
                  label="Explain authentication failures"
                  checked={!!config.diagnose}
                  onChange={(e) => set({ diagnose: e.currentTarget.checked })}
                  size="sm"
                  description="A wrong key and a wrong AAD fail identically, which is correct but unhelpful while you are matching an external system. This runs a trial decryption on failure to say which one is wrong. It never returns the trial result. Leave it off in production."
                />
              )}

              {decrypting && !rawFormat && (
                <Select
                  label="When a value is not encrypted"
                  data={ON_PLAINTEXT_OPTIONS.map(({ value, label }) => ({ value, label }))}
                  value={config.onPlaintext || 'passthrough'}
                  onChange={(val) => set({ onPlaintext: val || 'passthrough' })}
                  size="sm"
                  allowDeselect={false}
                  description={describe(ON_PLAINTEXT_OPTIONS, config.onPlaintext || 'passthrough')}
                />
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

      <Alert icon={<IconAlertTriangle size="1rem" />} color="orange" variant="light">
        <Text size="sm">
          This key is stored with the workflow definition. Anyone who can read or export this
          workflow can read the key, and rotating it does not re-encrypt data already written
          under the old one.
        </Text>
      </Alert>

      {decrypting && !rawFormat && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          <Text size="sm">
            In envelope format this node only touches values written by an encrypt node — anything
            without an <Text span ff="monospace">enc:v1:</Text> or <Text span ff="monospace">enc:v2:</Text>{' '}
            prefix is passed through untouched. If you are decrypting a column that another system
            encrypted, switch <strong>Value format</strong> to <strong>Raw ciphertext</strong> under
            Format &amp; interoperability and describe its scheme, or this node will do nothing and
            still report success.
          </Text>
        </Alert>
      )}

      {!decrypting && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          <Text size="sm">
            A fresh random IV per value means the same input encrypts differently every time, so an
            encrypted field cannot be used as a join or lookup key downstream. Name leaf fields
            only: a field holding an object or array is refused rather than stringified.
          </Text>
        </Alert>
      )}
    </Stack>
  )
}
