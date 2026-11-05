import { useState } from 'react'
import { Alert, Badge, Button, Code, Group, Paper, SimpleGrid, Stack, Text, TextInput } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { IconKey } from '@tabler/icons-react'
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

/**
 * The sources and sinks a release uses, and what happens to each there: one
 * there is reused, a missing one is created there on approval (its settings
 * can be changed for there), and one with a credential typed in plain text
 * needs that credential moved to a secret first.
 */
export function ReleaseConnections({ rows, where, settings, onSettings, onMoved }: {
  rows: ConnectionRow[]
  where: string
  settings: ConnectionSettings
  onSettings: (s: ConnectionSettings) => void
  onMoved: () => void
}) {
  if (rows.length === 0) return null
  return (
    <Stack gap="xs">
      <Text size="sm" fw={600}>Connections</Text>
      {rows.map((c) => (
        <Paper key={`${c.kind}/${c.id}`} withBorder radius="md" p="sm">
          <Stack gap="xs">
            <Group justify="space-between" wrap="nowrap" gap="sm">
              <Stack gap={0}>
                <Text size="sm" fw={600}>{c.name}</Text>
                <Text size="xs" c="dimmed">{c.kind === 'source' ? 'Source' : 'Sink'} · {c.type}</Text>
              </Stack>
              {c.status === 'uses' && <Badge color="green" variant="light" tt="none">Uses {where}'s {c.target_name}</Badge>}
              {c.status === 'create' && <Badge color="blue" variant="light" tt="none">Created in {where}</Badge>}
              {c.status === 'blocked' && <Badge color="orange" variant="light" tt="none">Needs one step</Badge>}
            </Group>
            {c.status === 'create' && (
              <CreatedSettings c={c} where={where} settings={settings} onSettings={onSettings} />
            )}
            {c.status === 'blocked' && <PlainCredentials c={c} onMoved={onMoved} />}
          </Stack>
        </Paper>
      ))}
    </Stack>
  )
}

function CreatedSettings({ c, where, settings, onSettings }: {
  c: ConnectionRow; where: string; settings: ConnectionSettings; onSettings: (s: ConnectionSettings) => void
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
    <Stack gap={6}>
      <Text size="xs" c="dimmed">{where} has no {c.name} yet, so it comes with this release. Change what differs in {where}:</Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
        {all.map((s) => s.secret ? (
          <Stack key={s.key} gap={2}>
            <Text size="sm" fw={500}>{s.key}</Text>
            <Code>{s.value}</Code>
          </Stack>
        ) : (
          <TextInput
            key={s.key}
            label={s.key}
            size="sm"
            value={settings[c.id]?.[s.key] ?? s.value}
            onChange={(e) => set(s.key, e.currentTarget.value, s.value)}
          />
        ))}
      </SimpleGrid>
    </Stack>
  )
}

function PlainCredentials({ c, onMoved }: { c: ConnectionRow; onMoved: () => void }) {
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
