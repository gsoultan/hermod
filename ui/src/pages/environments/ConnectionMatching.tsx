import { useMemo, useState } from 'react'
import { Alert, Anchor, Box, Button, Group, Loader, Progress, Select, Stack, Text, ThemeIcon } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { IconAlertCircle, IconArrowRight, IconDatabaseImport, IconExternalLink } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { FilterBar, RowList, StatePill, type Tone } from '@/components/rows'
import { environmentKeys, getMatching, putMapping, type Link, type MatchRow, type MatchStatus } from '@/lib/environments'
import { PlainCredentials } from './ReleaseConnections'

/** How a row reads: its match status, or that it travels, or that a plain credential holds it back. */
type Shown = MatchStatus | 'travels' | 'plain'

const STATE: Record<Shown, { tone: Tone; label: string; order: number }> = {
  plain: { tone: 'warn', label: 'Needs a secret first', order: 0 },
  missing: { tone: 'err', label: 'Missing', order: 1 },
  suggested: { tone: 'warn', label: 'Check this', order: 2 },
  travels: { tone: 'info', label: 'Comes with the first release', order: 3 },
  matched: { tone: 'ok', label: 'Matched', order: 4 },
}

/** What a row is, once what the server knows about travelling is taken in. */
const shown = (r: MatchRow): Shown =>
  r.status === 'missing' && !r.target_id ? ((r.plain_keys ?? []).length > 0 ? 'plain' : r.travels ? 'travels' : 'missing') : r.status
const ready = (s: Shown) => s === 'matched' || s === 'travels'

type Show = 'all' | 'check' | 'matched'

interface ConnectionMatchingProps {
  link: Link
  onDone?: () => void
}

const key = (r: MatchRow) => `${r.kind}:${r.id}`

/** Every word a person might type to find a row: its name, type and kind, and what it is matched to there. */
const haystack = (r: MatchRow) => [r.name, r.type, r.kind, r.target_name ?? ''].join(' ').toLowerCase()

/**
 * Which connection in the linked install each local source and sink becomes
 * when a workflow is promoted. Hermod matches by name and type; a person only
 * confirms the suggestions and picks the ones it could not match.
 *
 * One row per connection, the ones that need a look first, with search and a
 * To check / Matched filter. Order and filter go by what the server last
 * saved, not by unsaved picks, so a row never jumps or vanishes under the
 * cursor while a person works down the list.
 */
export function ConnectionMatching({ link, onDone }: ConnectionMatchingProps) {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({
    queryKey: environmentKeys.matching(link.id, ''),
    queryFn: ({ signal }) => getMatching(link.id, '', signal),
  })
  const [edits, setEdits] = useState<Record<string, string>>({})
  const [search, setSearch] = useState('')
  const [show, setShow] = useState<Show>('all')

  const rows = useMemo(
    () => [...(data ?? [])].sort((a, b) => STATE[shown(a)].order - STATE[shown(b)].order || a.name.localeCompare(b.name)),
    [data],
  )
  // A pick makes it matched; clearing it puts back what it was.
  const effective = (r: MatchRow): MatchRow =>
    r.id in edits ? { ...r, target_id: edits[r.id], status: edits[r.id] ? 'matched' : (r.target_id ? 'missing' : r.status) } : r
  const readyNow = rows.filter((r) => ready(shown(effective(r)))).length
  const savedReady = rows.filter((r) => ready(shown(r))).length
  const travelling = rows.filter((r) => shown(effective(r)) === 'travels').length
  const toCheck = rows.length - savedReady

  const needle = search.trim().toLowerCase()
  const visible = rows.filter((r) =>
    (show === 'all' || (show === 'matched') === ready(shown(r))) && (!needle || haystack(r).includes(needle)))

  const save = useMutation({
    mutationFn: () => putMapping(link.id, edits),
    onSuccess: async () => {
      setEdits({})
      await queryClient.invalidateQueries({ queryKey: ['environments', 'matching', link.id] })
      notifications.show({ color: 'green', message: 'Connection matches saved.' })
      onDone?.()
    },
  })

  if (isLoading) return <Group justify="center" p="lg"><Loader size="sm" aria-label="Loading connections" /></Group>
  if (error) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title={`Could not read ${link.name}'s connections`}>
        {(error as Error).message}
      </Alert>
    )
  }
  if (rows.length === 0) {
    return <Text size="sm" c="dimmed">This instance has no sources or sinks yet, so there is nothing to match.</Text>
  }

  const dirty = Object.keys(edits).length > 0
  const all = readyNow === rows.length
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['environments', 'matching', link.id] })
  return (
    <Stack gap="sm">
      <Stack gap={6}>
        <Text size="sm">
          {all && <><b>You can promote now.</b>{' '}</>}
          <b>{readyNow} of {rows.length}</b> connections are ready.
          {travelling > 0 && ` ${link.name} has no copy of ${travelling} of them yet, so ${travelling} of them travel with the first release and are created there when it is approved.`}
          {!all && ' A flow can be promoted once every connection it uses is ready.'}
        </Text>
        <Progress value={(readyNow / rows.length) * 100} color={all ? 'green' : 'orange'} size="sm" aria-hidden />
      </Stack>

      <FilterBar
        search={{ value: search, onChange: setSearch, placeholder: 'Search connections' }}
        filter={{
          value: show,
          onChange: (v) => setShow(v as Show),
          options: [
            { value: 'all', label: `All (${rows.length})` },
            { value: 'check', label: `To check (${toCheck})` },
            { value: 'matched', label: `Ready (${savedReady})` },
          ],
        }}
      />

      <RowList aria-label={`Connections matched in ${link.name}`}>
        {visible.map((r) => <MatchItem key={key(r)} row={effective(r)} link={link} onPick={(v) => setEdits((e) => ({ ...e, [r.id]: v }))} onMoved={refresh} />)}
      </RowList>
      {visible.length === 0 && (
        <Text size="sm" c="dimmed" ta="center" py="md">
          {needle ? `No connection matches “${search.trim()}”.` : show === 'check' ? 'Nothing to check: every connection is ready.' : 'No connection is ready yet.'}
        </Text>
      )}

      <Group
        justify="flex-end"
        gap="xs"
        py="xs"
        style={{ position: 'sticky', bottom: 0, background: 'var(--mantine-color-body)', zIndex: 1 }}
      >
        {dirty && <Text size="sm" c="dimmed" mr="auto">{Object.keys(edits).length} not saved yet</Text>}
        {onDone && !dirty && <Button variant="default" onClick={onDone}>Done</Button>}
        {dirty && (
          <>
            <Button variant="default" onClick={() => setEdits({})}>Undo changes</Button>
            <Button onClick={() => save.mutate()} loading={save.isPending}>Save matches</Button>
          </>
        )}
      </Group>
      {save.error && <Alert color="red">{(save.error as Error).message}</Alert>}
    </Stack>
  )
}

function MatchItem({ row: r, link, onPick, onMoved }: { row: MatchRow; link: Link; onPick: (targetId: string) => void; onMoved: () => void }) {
  const options = (r.candidates ?? []).map((c) => ({ value: c.id, label: `${c.name} (${c.type}${c.vhost && c.vhost !== 'default' ? `, ${c.vhost}` : ''})` }))
  if (r.target_id && !options.some((o) => o.value === r.target_id)) {
    options.unshift({ value: r.target_id, label: r.target_name || r.target_id })
  }
  const s = shown(r)
  const state = STATE[s]
  const Icon = r.kind === 'source' ? IconDatabaseImport : IconExternalLink
  return (
    <Box component="li" data-name={r.name} className="hermod-match-row">
      <Group gap="sm" wrap="nowrap" className="hermod-match-here" style={{ minWidth: 0 }}>
        <ThemeIcon variant="light" color={s === 'missing' ? 'red' : 'gray'} radius="md" size={32} aria-hidden className="hermod-row-icon" style={{ flex: 'none' }}>
          <Icon size="1rem" stroke={1.6} />
        </ThemeIcon>
        <Box style={{ minWidth: 0 }}>
          <Text size="sm" fw={600} truncate="end" title={r.name}>{r.name}</Text>
          <Text size="xs" c="dimmed" truncate="end">{r.kind === 'source' ? 'Source' : 'Sink'} · {r.type}</Text>
        </Box>
      </Group>
      <IconArrowRight size="0.9rem" aria-hidden className="hermod-match-arrow" style={{ color: 'var(--mantine-color-dimmed)' }} />
      <Box className="hermod-match-there" style={{ minWidth: 0 }}>
        {s === 'plain' ? (
          <PlainCredentials c={{ kind: r.kind, id: r.id, name: r.name, type: r.type, status: 'blocked', plain_keys: r.plain_keys }} onMoved={onMoved} />
        ) : options.length > 0 ? (
          <Select
            aria-label={`Match for ${r.name}`}
            size="sm"
            data={options}
            value={r.target_id || null}
            placeholder={s === 'travels' ? `Or use one in ${link.name}` : `Pick one in ${link.name}`}
            clearable
            searchable
            nothingFoundMessage="No connection by that name"
            onChange={(v) => onPick(v ?? '')}
          />
        ) : s === 'travels' ? (
          <Text size="xs" c="dimmed">Created in {link.name} when the first release with it is approved</Text>
        ) : (
          <Anchor href={`${link.url}/${r.kind === 'source' ? 'sources' : 'sinks'}/new`} target="_blank" rel="noopener noreferrer" size="sm">
            <Group gap={4} wrap="nowrap">
              Create it in {link.name} <IconExternalLink size="0.8rem" aria-hidden />
            </Group>
          </Anchor>
        )}
      </Box>
      <StatePill tone={state.tone} className="hermod-match-state">{state.label}</StatePill>
    </Box>
  )
}
