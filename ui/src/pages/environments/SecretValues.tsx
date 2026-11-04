import { useState } from 'react'
import {
  Alert, Anchor, Badge, Button, Group, Modal, Paper, PasswordInput, Radio, SegmentedControl, Stack, Text, Textarea, TextInput,
} from '@mantine/core'
import { IconClipboardText, IconKey, IconSearch } from '@tabler/icons-react'

/** A secret production lacks, the flows that use it, and the sources or sinks there that read it. */
export interface NeededSecret {
  name: string
  usedBy: string[]
  via?: string[]
}

/** Up to this many are typed in the dialog itself; more open one place to fill them. */
export const INLINE_SECRETS = 5

const usedByLabel = (s: NeededSecret) => (s.usedBy.length > 2 ? `${s.usedBy.length} flows` : s.usedBy.join(', '))

/** What reads a secret: the connection when one does, else the flows. */
function readBy(s: NeededSecret, many: boolean): string | undefined {
  if (s.via?.length) return `Read by ${s.via.join(', ')}`
  return many && s.usedBy.length ? `Used by ${usedByLabel(s)}` : undefined
}

/** Each secret once, in name order, with every flow that uses it and the connections that read it. */
export function neededSecrets(previews: {
  workflow_name: string
  missing_secrets?: string[] | null
  secret_uses?: Record<string, string[]> | null
}[]): NeededSecret[] {
  const by = new Map<string, NeededSecret>()
  for (const p of previews) {
    for (const name of p.missing_secrets ?? []) {
      const s = by.get(name) ?? { name, usedBy: [], via: [] }
      s.usedBy.push(p.workflow_name)
      for (const c of p.secret_uses?.[name] ?? []) if (!s.via!.includes(c)) s.via!.push(c)
      by.set(name, s)
    }
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name))
}

/** The non-blank values, which is all that is ever sent. */
export function filledValues(values: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(values).filter(([, v]) => v.trim() !== ''))
}

/**
 * Reads NAME=value lines, as in a .env file: blank lines, comments and an
 * `export ` prefix are skipped, and one pair of surrounding quotes is taken
 * off. Names that are not needed are returned apart, so the person sees them.
 */
export function parseEnvBlock(text: string, needed: string[]): { values: Record<string, string>; ignored: string[] } {
  const values: Record<string, string> = {}
  const ignored: string[] = []
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim().replace(/^export\s+/, '')
    if (!line || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    const name = line.slice(0, eq).trim()
    let value = line.slice(eq + 1).trim()
    if (value.length >= 2 && (value[0] === '"' || value[0] === "'") && value.at(-1) === value[0]) value = value.slice(1, -1)
    if (needed.includes(name)) values[name] = value
    else ignored.push(name)
  }
  return { values, ignored }
}

/** "A and B", "A, B and C". */
export const andList = (names: string[]) =>
  names.length <= 1 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names.at(-1)}`

/**
 * What happens to a flow sent to start when some of its secrets still have
 * no value: it stays stopped (the default), or starts anyway, as when a
 * secret manager serves them.
 */
export function StartChoice({ empty, startAnyway, onChange }: {
  empty: string[]; startAnyway: boolean; onChange: (startAnyway: boolean) => void
}) {
  if (empty.length === 0) return null
  const names = andList(empty)
  return (
    <Radio.Group
      label={empty.length === 1 ? `${names} has no value` : `${names} have no value`}
      value={startAnyway ? 'start' : 'stop'}
      onChange={(v) => onChange(v === 'start')}
    >
      <Stack gap={6} mt={6}>
        <Radio value="stop" label={`Keep it stopped until ${names} ${empty.length === 1 ? 'has a value' : 'have values'}`} />
        <Radio value="start" label="Start it anyway (a secret manager serves them)" />
      </Stack>
    </Radio.Group>
  )
}

/** How the confirm says whether it starts: " and starts", or why it stays stopped. */
export function startLine(count: number, start: boolean, empty: string[], startAnyway: boolean) {
  if (!start) return ''
  if (empty.length > 0 && !startAnyway) {
    return ` but stays stopped until ${andList(empty)} ${empty.length === 1 ? 'has a value' : 'have values'}`
  }
  return count === 1 ? ' and starts' : ' and start'
}

interface Props {
  needed: NeededSecret[]
  values: Record<string, string>
  onChange: (values: Record<string, string>) => void
  /** Where they go: the environment when sending, the vhost when approving. */
  where: string
  /** Sending from the dialog, where the approver fills what is left, or approving, where they are saved. */
  mode: 'send' | 'approve'
}

/**
 * Values for the secrets production lacks. A few are typed right here; many
 * open one place with search, an Empty filter and a .env paste. Values go
 * with the release and are never kept on this side.
 */
export function SecretValues({ needed, values, onChange, where, mode }: Props) {
  const [open, setOpen] = useState(false)
  if (needed.length === 0) return null
  const filled = needed.filter((s) => (values[s.name] ?? '').trim() !== '').length
  const set = (name: string, v: string) => onChange({ ...values, [name]: v })
  const many = needed.length > INLINE_SECRETS
  const flows = new Set(needed.flatMap((s) => s.usedBy)).size

  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm">
        <Group gap="xs" wrap="nowrap" align="flex-start">
          <IconKey size="1.1rem" aria-hidden style={{ flex: 'none', marginTop: 2 }} />
          <Stack gap={2}>
            <Text fw={600}>
              {mode === 'send'
                ? (needed.length === 1 ? `1 secret is new to ${where}` : `${needed.length} secrets are new to ${where}`)
                : (needed.length === 1 ? `1 secret has no value in ${where} yet` : `${needed.length} secrets have no value in ${where} yet`)}
            </Text>
            <Text size="sm" c="dimmed">
              {many
                ? `Used by ${flows === 1 ? '1 flow' : `${flows} flows`}. Each one is listed once, even when several flows use it.`
                : mode === 'send'
                  ? `Type ${where}'s values. They go straight to ${where} and are never stored or shown here.`
                  : `Type them and they are saved in ${where} as it goes live.`}
            </Text>
          </Stack>
        </Group>
        {many ? (
          <Group justify="space-between" wrap="wrap" gap="sm">
            <Badge variant="light" color={filled === needed.length ? 'green' : 'gray'} size="lg" tt="none">
              {filled} of {needed.length} filled
            </Badge>
            <Button variant="light" onClick={() => setOpen(true)}>Fill in values</Button>
          </Group>
        ) : (
          needed.map((s) => (
            <PasswordInput
              key={s.name}
              label={s.name}
              description={readBy(s, needed.length > 1)}
              placeholder={`Value in ${where}`}
              autoComplete="new-password"
              value={values[s.name] ?? ''}
              onChange={(e) => set(s.name, e.currentTarget.value)}
            />
          ))
        )}
        <Text size="xs" c="dimmed">
          {mode === 'send'
            ? `You can send with some empty; the approver fills the rest. ${where} never replaces a secret it already has.`
            : `Leave one empty when a secret manager serves it. A secret ${where} already has is never replaced.`}
        </Text>
      </Stack>
      <Modal opened={open} onClose={() => setOpen(false)} title={`Secret values for ${where}`} size="lg">
        {open && <ManySecrets needed={needed} values={values} onChange={onChange} where={where} mode={mode} onDone={() => setOpen(false)} />}
      </Modal>
    </Paper>
  )
}

function ManySecrets({ needed, values, onChange, where, mode, onDone }: Props & { onDone: () => void }) {
  const [query, setQuery] = useState('')
  const [show, setShow] = useState<'empty' | 'all'>('empty')
  const [pasting, setPasting] = useState(false)
  const [pasted, setPasted] = useState('')
  const [ignored, setIgnored] = useState<string[]>([])
  // Fixed when the window opens, so a row does not vanish under the cursor
  // the moment it is filled while Empty is chosen.
  const [emptyAtOpen] = useState(() => new Set(needed.filter((s) => !(values[s.name] ?? '').trim()).map((s) => s.name)))
  const isEmpty = (name: string) => !(values[name] ?? '').trim()
  const empty = needed.filter((s) => isEmpty(s.name)).length
  const filled = needed.length - empty
  const q = query.trim().toLowerCase()
  const rows = needed.filter((s) => (show === 'all' || emptyAtOpen.has(s.name)) &&
    (!q || s.name.toLowerCase().includes(q) || [...s.usedBy, ...(s.via ?? [])].some((f) => f.toLowerCase().includes(q))))

  const paste = () => {
    const got = parseEnvBlock(pasted, needed.map((s) => s.name))
    onChange({ ...values, ...got.values })
    setIgnored(got.ignored)
    setPasted('')
    setPasting(false)
  }

  return (
    <Stack gap="sm">
      <TextInput
        aria-label="Search secrets"
        placeholder="Search secrets"
        leftSection={<IconSearch size="0.9rem" />}
        value={query}
        onChange={(e) => setQuery(e.currentTarget.value)}
      />
      <Group justify="space-between" wrap="wrap" gap="sm">
        <SegmentedControl
          size="xs"
          value={show}
          onChange={(v) => setShow(v as 'empty' | 'all')}
          data={[{ value: 'empty', label: `Empty (${empty})` }, { value: 'all', label: `All (${needed.length})` }]}
        />
        <Button size="xs" variant="subtle" leftSection={<IconClipboardText size="0.9rem" />} onClick={() => setPasting((p) => !p)}>
          Paste a .env block
        </Button>
      </Group>
      {pasting && (
        <Paper withBorder radius="md" p="sm">
          <Stack gap="xs">
            <Textarea
              label="Lines like NAME=value"
              description="They fill the matching boxes. Names that are not needed here are ignored and shown to you."
              autosize
              minRows={3}
              value={pasted}
              onChange={(e) => setPasted(e.currentTarget.value)}
              styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
            />
            <Group justify="flex-end"><Button size="xs" onClick={paste} disabled={!pasted.trim()}>Paste</Button></Group>
          </Stack>
        </Paper>
      )}
      {ignored.length > 0 && (
        <Alert color="yellow" variant="light" withCloseButton onClose={() => setIgnored([])}>
          Not needed here, so ignored: {ignored.join(', ')}
        </Alert>
      )}
      <Stack gap="xs" mah={360} style={{ overflowY: 'auto' }}>
        {rows.length === 0 && <Text size="sm" c="dimmed">{q ? 'No secret matches that.' : 'Every one has a value.'}</Text>}
        {rows.map((s) => (
          <PasswordInput
            key={s.name}
            label={s.name}
            description={readBy(s, true)}
            placeholder={`Value in ${where}`}
            autoComplete="new-password"
            value={values[s.name] ?? ''}
            onChange={(e) => onChange({ ...values, [s.name]: e.currentTarget.value })}
          />
        ))}
      </Stack>
      <Text size="xs" c="dimmed">
        {mode === 'send'
          ? `Values go straight to ${where} with the release and are never stored here. ${where} never replaces a secret it already has.`
          : `Saved in ${where} as it goes live. A secret ${where} already has is never replaced.`}
      </Text>
      <Group justify="space-between">
        <Anchor component="button" type="button" size="sm" c="red" onClick={() => onChange({})} disabled={filled === 0}>
          Clear all
        </Anchor>
        <Button onClick={onDone}>Done · {filled} of {needed.length} filled</Button>
      </Group>
    </Stack>
  )
}
