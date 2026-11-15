import { useState } from 'react'
import { Alert, Badge, Button, Code, Collapse, Group, Paper, SimpleGrid, Stack, Text, TextInput, UnstyledButton } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { IconChevronDown, IconKey } from '@tabler/icons-react'
import { andList } from './SecretValues'
import { moveToSecret, type ConnectionRow, type Preview } from '@/lib/environments'

/** Settings changed for the connections created there, by connection ID and setting. */
export type ConnectionSettings = Record<string, Record<string, string>>

/** Each source and sink the release uses, once, in the order the flows name them. */
export function releaseConnections(previews: Preview[]): ConnectionRow[] {
  const seen = new Map<string, ConnectionRow>()
  for (const p of previews) {
    for (const c of p.connections ?? []) {
      if (c.status !== 'unmatched' && !seen.has(`${c.kind}/${c.id}`)) seen.set(`${c.kind}/${c.id}`, c)
    }
  }
  return [...seen.values()]
}

/** A secret name for a credential: the connection's name and the setting, in capitals. */
export const secretNameFor = (c: ConnectionRow, key: string) =>
  `${c.name}_${key}`.toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '')

const ACRONYMS = new Set(['url', 'uri', 'id', 'api', 'sql', 'ssl', 'tls', 'http', 'https', 'dsn', 'db', 'ip', 'tcp', 'aws', 'gcp', 'jwt', 'smtp', 'sasl'])

/** A setting's key as a person reads it: base_url is "Base URL", maxRetries is "Max retries". */
export function settingLabel(key: string): string {
  const words = key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').split(/[\s_.-]+/).filter(Boolean).map((w) => w.toLowerCase())
  if (words.length === 0) return key
  return words.map((w, i) => (ACRONYMS.has(w) ? w.toUpperCase() : i === 0 ? w[0].toUpperCase() + w.slice(1) : w)).join(' ')
}

/**
 * The sources and sinks a release uses, and what happens to each there. One
 * with a credential typed in plain text needs it moved to a secret first, so
 * it stays in view; the rest (one there reused, a missing one created there
 * on approval with settings that can be changed for there) fold away.
 */
export function ReleaseConnections({ rows, where, settings, onSettings, onMoved }: {
  rows: ConnectionRow[]
  where: string
  settings: ConnectionSettings
  onSettings: (s: ConnectionSettings) => void
  onMoved: () => void
}) {
  const [open, setOpen] = useState(false)
  const blocked = rows.filter((c) => c.status === 'blocked')
  const rest = rows.filter((c) => c.status !== 'blocked')
  const created = rest.filter((c) => c.status === 'create')
  if (rows.length === 0) return null
  return (
    <Stack gap="xs">
      {blocked.map((c) => (
        <Paper key={`${c.kind}/${c.id}`} withBorder radius="md" p="sm">
          <Stack gap="xs">
            <ConnectionHead c={c} where={where} />
            <PlainCredentials c={c} onMoved={onMoved} />
          </Stack>
        </Paper>
      ))}
      {rest.length > 0 && (
        <Paper withBorder radius="md">
          <UnstyledButton w="100%" p="sm" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
            <Group justify="space-between" wrap="nowrap">
              <Stack gap={0}>
                <Text size="sm" fw={600}>{created.length > 0 ? `Change settings for ${where}` : `Connections in ${where}`}</Text>
                <Text size="xs" c="dimmed">
                  {created.length > 0
                    ? `${andList(created.map((c) => c.name))} ${created.length === 1 ? 'is' : 'are'} created there with the same settings unless you change them`
                    : `Uses ${andList(rest.map((c) => c.target_name || c.name))} there`}
                </Text>
              </Stack>
              <IconChevronDown size="1rem" aria-hidden style={{ flex: 'none', transform: open ? 'rotate(180deg)' : undefined }} />
            </Group>
          </UnstyledButton>
          <Collapse expanded={open}>
            {open && (
              <Stack gap="sm" p="sm" pt={0}>
                {rest.map((c) => (
                  <Stack key={`${c.kind}/${c.id}`} gap="xs" data-connection={c.name}>
                    <ConnectionHead c={c} where={where} />
                    {c.status === 'create' && <CreatedSettings c={c} settings={settings} onSettings={onSettings} />}
                  </Stack>
                ))}
              </Stack>
            )}
          </Collapse>
        </Paper>
      )}
    </Stack>
  )
}

function ConnectionHead({ c, where }: { c: ConnectionRow; where: string }) {
  return (
    <Group justify="space-between" wrap="nowrap" gap="sm">
      <Stack gap={0}>
        <Text size="sm" fw={600}>{c.name}</Text>
        <Text size="xs" c="dimmed">{c.kind === 'source' ? 'Source' : 'Sink'} · {c.type}</Text>
      </Stack>
      {c.status === 'uses' && <Badge color="green" variant="light" tt="none">Uses {where}'s {c.target_name}</Badge>}
      {c.status === 'create' && <Badge color="blue" variant="light" tt="none">Created in {where}</Badge>}
      {c.status === 'blocked' && <Badge color="orange" variant="light" tt="none">Needs one step</Badge>}
    </Group>
  )
}

function CreatedSettings({ c, settings, onSettings }: {
  c: ConnectionRow; settings: ConnectionSettings; onSettings: (s: ConnectionSettings) => void
}) {
  const all = c.settings ?? []
  if (all.length === 0) return null
  const set = (key: string, value: string, original: string) => {
    const mine = { ...(settings[c.id] ?? {}) }
    if (value === original) delete mine[key]
    else mine[key] = value
    const next = { ...settings }
    if (Object.keys(mine).length) next[c.id] = mine
    else delete next[c.id]
    onSettings(next)
  }
  return (
    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
      {all.map((s) => s.secret ? (
        // A reference to a secret: each environment keeps its own value.
        <Stack key={s.key} gap={2}>
          <Text size="sm" fw={500}>{settingLabel(s.key)}</Text>
          <Code>{s.value}</Code>
        </Stack>
      ) : (
        <TextInput
          key={s.key}
          label={settingLabel(s.key)}
          size="sm"
          value={settings[c.id]?.[s.key] ?? s.value}
          onChange={(e) => set(s.key, e.currentTarget.value, s.value)}
        />
      ))}
    </SimpleGrid>
  )
}

/** The plain-text credentials that keep a connection where it is, each with a way to move it to a secret. */
export function PlainCredentials({ c, onMoved }: { c: ConnectionRow; onMoved: () => void }) {
  const keys = c.plain_keys ?? []
  const [moving, setMoving] = useState<string | null>(null)
  const [name, setName] = useState('')
  const move = useMutation({
    mutationFn: () => moveToSecret(c.kind, c.id, moving!, name.trim()),
    onSuccess: () => { setMoving(null); onMoved() },
  })
  return (
    <Stack gap={6}>
      <Text size="sm">
        {keys.join(', ')} {keys.length === 1 ? 'is' : 'are'} typed in plain text, so {c.name} can't travel. Move it to a
        secret here and it will; each environment then keeps its own value.
      </Text>
      {moving === null ? (
        <Group gap="xs">
          {keys.map((k) => (
            <Button
              key={k}
              size="xs"
              leftSection={<IconKey size="0.9rem" />}
              aria-label={`Move ${k} to a secret`}
              onClick={() => { setMoving(k); setName(secretNameFor(c, k)) }}
            >
              Move {k} to a secret
            </Button>
          ))}
        </Group>
      ) : (
        <Group gap="xs" align="flex-end" wrap="wrap">
          <TextInput label="Secret name" size="xs" value={name} onChange={(e) => setName(e.currentTarget.value)} />
          <Button size="xs" loading={move.isPending} disabled={!name.trim()} onClick={() => move.mutate()}>Move</Button>
          <Button size="xs" variant="default" onClick={() => setMoving(null)}>Cancel</Button>
        </Group>
      )}
      {move.error && <Alert color="red" variant="light">{(move.error as Error).message}</Alert>}
    </Stack>
  )
}
