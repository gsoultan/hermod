import { Fragment, useState } from 'react'
import {
  Alert, Anchor, Badge, Box, Button, Chip, Code, Divider, Group, Highlight, Modal, Paper, PasswordInput, Popover, Radio,
  SegmentedControl, Stack, Text, Textarea, TextInput, UnstyledButton,
} from '@mantine/core'
import { IconClipboardText, IconExternalLink, IconKey, IconSearch } from '@tabler/icons-react'
import type { SecretPlace } from '@/lib/environments'

/** Where a secret is used, and for a step the flow it is in here, to open it. */
export interface Place extends SecretPlace {
  workflowId?: string
}

/** A secret production lacks, the flows that use it, and every step, source and sink that does. */
export interface NeededSecret {
  name: string
  usedBy: string[]
  via?: string[]
  places: Place[]
}

/** Up to this many are typed in the dialog itself; more open one place to fill them. */
export const INLINE_SECRETS = 5

/** Up to this many places show under a secret; the rest open on request. */
const SHOWN_PLACES = 2

const usedByLabel = (s: NeededSecret) => (s.usedBy.length > 2 ? `${s.usedBy.length} flows` : s.usedBy.join(', '))

/** The places an instance that only names its connections ("sink warehouse") answered with. */
function placesFromUses(uses: string[]): Place[] {
  return uses.flatMap((label) => {
    const [kind, ...rest] = label.split(' ')
    return (kind === 'source' || kind === 'sink') && rest.length ? [{ kind, name: rest.join(' ') } as Place] : []
  })
}

const samePlace = (a: Place, b: Place) =>
  a.kind === b.kind && a.name === b.name && (a.field ?? '') === (b.field ?? '') && (a.node_id ?? '') === (b.node_id ?? '')

/**
 * Each secret once, in name order, with every flow that uses it, the
 * connections that read it, and every place it is used.
 */
export function neededSecrets(previews: {
  workflow_name: string
  workflow_id?: string
  missing_secrets?: string[] | null
  secret_uses?: Record<string, string[]> | null
  secret_places?: Record<string, SecretPlace[]> | null
}[]): NeededSecret[] {
  const by = new Map<string, NeededSecret>()
  for (const p of previews) {
    for (const name of p.missing_secrets ?? []) {
      const s = by.get(name) ?? { name, usedBy: [], via: [], places: [] }
      s.usedBy.push(p.workflow_name)
      const uses = p.secret_uses?.[name] ?? []
      for (const c of uses) if (!s.via!.includes(c)) s.via!.push(c)
      const places: Place[] = p.secret_places?.[name]?.length
        ? p.secret_places[name].map((pl) => (pl.kind === 'step' && p.workflow_id ? { ...pl, workflowId: p.workflow_id } : pl))
        : placesFromUses(uses)
      for (const pl of places) if (!s.places.some((have) => samePlace(have, pl))) s.places.push(pl)
      by.set(name, s)
    }
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name))
}

const KIND_LABEL: Record<Place['kind'], string> = { step: 'Step', source: 'Source', sink: 'Sink' }
const KIND_COLOR: Record<Place['kind'], string> = { step: 'blue', source: 'green', sink: 'orange' }
const has = (s: NeededSecret, kind: Place['kind']) => s.places.some((p) => p.kind === kind)

/** "3 are used by steps and 1 by a sink.", or '' when no place is known. */
export function whereLine(needed: NeededSecret[]): string {
  const steps = needed.filter((s) => has(s, 'step')).length
  const sources = needed.filter((s) => has(s, 'source')).length
  const sinks = needed.filter((s) => has(s, 'sink')).length
  const verb = (n: number) => (n === 1 ? 'is' : 'are')
  const noun = (n: number, one: string, many: string) => (n === 1 ? one : many)
  const parts: [number, string][] = []
  if (steps) parts.push([steps, noun(steps, 'a step', 'steps')])
  if (sources) parts.push([sources, noun(sources, 'a source', 'sources')])
  if (sinks) parts.push([sinks, noun(sinks, 'a sink', 'sinks')])
  if (parts.length === 0) return ''
  const [[n, first], ...rest] = parts
  const tail = rest.map(([m, what]) => `${m} by ${what}`)
  return `${n} ${verb(n)} used by ${first}${tail.length ? ` and ${andList(tail)}` : ''}.`
}

/** A step's saved setting, with the secret marked, and a way to open the flow. */
function StepDetail({ place, secret }: { place: Place; secret: string }) {
  return (
    <Stack gap={8} maw={360}>
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="sm">
        <Text size="sm"><Text span fw={600}>{place.name}</Text> <Text span c="dimmed" size="xs">· step</Text></Text>
        {place.workflowId && (
          <Anchor href={`/workflows/${place.workflowId}/edit`} target="_blank" rel="noopener noreferrer" size="xs" fw={600} style={{ whiteSpace: 'nowrap' }}>
            Open step <IconExternalLink size="0.8rem" style={{ verticalAlign: 'middle' }} aria-hidden />
          </Anchor>
        )}
      </Group>
      {place.field && <Text size="xs" c="dimmed">{place.field}</Text>}
      {place.value && (
        <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          <Highlight component="span" highlight={secret} ff="monospace" size="xs" inherit>{place.value}</Highlight>
        </Code>
      )}
    </Stack>
  )
}

/** One place: its kind, its name (a step's opens how it is used) and the setting. */
function PlaceLine({ place, secret }: { place: Place; secret: string }) {
  const [open, setOpen] = useState(false)
  const name = place.kind === 'step' && place.value ? (
    <Popover opened={open} onChange={setOpen} position="bottom-start" shadow="md" withinPortal>
      <Popover.Target>
        <UnstyledButton onClick={() => setOpen((o) => !o)} aria-expanded={open} aria-label={`Where ${secret} is used in ${place.name}`} style={{ display: 'inline' }}>
          <Text span size="xs" fw={600} c="blue" td="underline">{place.name}</Text>
        </UnstyledButton>
      </Popover.Target>
      <Popover.Dropdown><StepDetail place={place} secret={secret} /></Popover.Dropdown>
    </Popover>
  ) : <Text span size="xs" fw={600}>{place.name}</Text>
  return (
    <Group gap={6} wrap="nowrap" align="flex-start" mt={2}>
      <Badge size="xs" radius="sm" variant="light" color={KIND_COLOR[place.kind]} mt={1} style={{ flex: 'none' }}>{KIND_LABEL[place.kind]}</Badge>
      <Text size="xs" miw={0}>
        {name}
        {place.field && <Text span size="xs" c="dimmed"> · {place.field}</Text>}
      </Text>
    </Group>
  )
}

/** Where a secret is used: two places, then the rest on request. */
function Places({ s }: { s: NeededSecret }) {
  const [all, setAll] = useState(false)
  const shown = all ? s.places : s.places.slice(0, SHOWN_PLACES)
  const more = s.places.length - shown.length
  return (
    <>
      {shown.map((p, i) => <PlaceLine key={i} place={p} secret={s.name} />)}
      {more > 0 && (
        <Anchor component="button" type="button" size="xs" fw={600} onClick={() => setAll(true)}>and {more} more</Anchor>
      )}
    </>
  )
}

/** A secret, where it is used, and its value box. */
function SecretRow({ s, value, onChange, where, showFlows }: {
  s: NeededSecret; value: string; onChange: (v: string) => void; where: string; showFlows: boolean
}) {
  return (
    <Group gap="sm" wrap="wrap" align="center" style={{ rowGap: 6 }}>
      <Box miw={220} style={{ flex: '1 1 260px' }}>
        <Code fz="sm">{s.name}</Code>
        <Places s={s} />
        {showFlows && s.usedBy.length > 0 && <Text size="xs" c="dimmed" mt={2}>{usedByLabel(s)}</Text>}
      </Box>
      <PasswordInput
        aria-label={s.name}
        placeholder={`Value in ${where}`}
        autoComplete="new-password"
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        miw={200}
        style={{ flex: '1 1 220px', maxWidth: '100%' }}
      />
    </Group>
  )
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
  const placed = whereLine(needed)
  const severalFlows = flows > 1

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
                : `${placed ? `${placed} ` : ''}${mode === 'send'
                  ? `Type ${where}'s values. They go straight to ${where} and are never stored or shown here.`
                  : `Type them and they are saved in ${where} as it goes live.`}`}
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
          <Stack gap="sm">
            {needed.map((s, i) => (
              <Fragment key={s.name}>
                {i > 0 && <Divider />}
                <SecretRow s={s} value={values[s.name] ?? ''} onChange={(v) => set(s.name, v)} where={where} showFlows={severalFlows} />
              </Fragment>
            ))}
          </Stack>
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
  const [kind, setKind] = useState<'any' | Place['kind']>('any')
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
  const kinds = (['source', 'step', 'sink'] as const).map((k) => ({ k, n: needed.filter((s) => has(s, k)).length })).filter((x) => x.n > 0)
  const rows = needed.filter((s) => (show === 'all' || emptyAtOpen.has(s.name)) &&
    (kind === 'any' || has(s, kind)) &&
    (!q || s.name.toLowerCase().includes(q) ||
      [...s.usedBy, ...(s.via ?? []), ...s.places.flatMap((p) => [p.name, p.field ?? ''])].some((f) => f.toLowerCase().includes(q))))

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
        placeholder="Search secrets, steps or connections"
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
      {kinds.length > 1 && (
        <Chip.Group multiple={false} value={kind} onChange={(v) => setKind(v as typeof kind)}>
          <Box component="fieldset" aria-label="Where it is used" m={0} p={0} style={{ border: 0, minWidth: 0 }}>
            <Group gap={6}>
              <Chip value="any" size="xs">Anywhere</Chip>
              {kinds.map(({ k, n }) => <Chip key={k} value={k} size="xs">{`${KIND_LABEL[k]}s (${n})`}</Chip>)}
            </Group>
          </Box>
        </Chip.Group>
      )}
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
        {rows.length === 0 && (
          <Text size="sm" c="dimmed">{q || kind !== 'any' ? 'No secret matches that.' : 'Every one has a value.'}</Text>
        )}
        {rows.map((s, i) => (
          <Fragment key={s.name}>
            {i > 0 && <Divider />}
            <SecretRow s={s} value={values[s.name] ?? ''} onChange={(v) => onChange({ ...values, [s.name]: v })} where={where} showFlows />
          </Fragment>
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
