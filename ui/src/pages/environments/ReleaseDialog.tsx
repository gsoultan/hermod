import { useState } from 'react'
import {
  Alert, Badge, Button, Group, Loader, Modal, Paper, Select, Stack, Switch, Text, Textarea, ThemeIcon,
} from '@mantine/core'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { IconAlertTriangle, IconArrowRight, IconCheck, IconRocket, IconX } from '@tabler/icons-react'
import { formatDateTime } from '@/utils/dateUtils'
import {
  environmentKeys, previewRelease, sendRelease, type Preview, type ReleaseGroup, type ReleaseItem, type SentRelease,
} from '@/lib/environments'

interface Version {
  version: number
  created_at?: string
}

interface ReleaseDialogProps {
  opened: boolean
  onClose: () => void
  /** What to promote; each workflow goes to the environment its vhost is connected to. */
  items: ReleaseItem[]
  /** Offered when there is one workflow, so an older version can go instead of the newest. */
  versions?: Version[]
  title?: string
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`

/**
 * Promotes workflows as one release. Each goes to the environment its vhost
 * is connected to; production checks them all first, and anything that cannot
 * land keeps the button off until it is fixed or left out. Whatever is sent
 * together is approved, undone and rolled back together there.
 */
export function ReleaseDialog({ opened, onClose, items, versions = [], title = 'Promote' }: ReleaseDialogProps) {
  return (
    <Modal opened={opened} onClose={onClose} title={title} size="lg">
      {/* Remounted each time it opens, so a new release starts from the items it was given. */}
      {opened && <ReleaseForm items={items} versions={versions} onClose={onClose} />}
    </Modal>
  )
}

function ReleaseForm({ items, versions, onClose }: { items: ReleaseItem[]; versions: Version[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [kept, setKept] = useState<ReleaseItem[]>(items)
  const [note, setNote] = useState('')
  const [start, setStart] = useState(true)
  const [autoRollback, setAutoRollback] = useState<boolean | null>(null)
  const [sent, setSent] = useState<SentRelease[] | null>(null)

  const preview = useQuery({
    queryKey: ['environments', 'release-preview', kept],
    queryFn: () => previewRelease(kept),
    enabled: kept.length > 0 && !sent,
    placeholderData: keepPreviousData,
    staleTime: 0,
  })
  const groups = (preview.data?.groups ?? []).filter((g) => g.previews.length > 0)
  const unlinked = preview.data?.unlinked ?? []
  const rollback = autoRollback ?? groups[0]?.previews[0]?.rules.auto_rollback ?? true
  const settled = !preview.isFetching && !preview.isPlaceholderData

  const send = useMutation({
    mutationFn: () => sendRelease(kept, { note: note.trim(), start, auto_rollback: rollback }),
    onSuccess: setSent,
    onSettled: () => Promise.all([
      queryClient.invalidateQueries({ queryKey: environmentKeys.sent }),
      queryClient.invalidateQueries({ queryKey: environmentKeys.ready }),
    ]),
  })

  const leaveOut = (workflowId: string) => setKept((k) => k.filter((i) => i.workflow_id !== workflowId))
  const setVersion = (v: string | null) => setKept((k) => k.map((i) => ({ ...i, version: Number(v ?? 0) })))

  if (sent) return <Sent releases={sent} groups={groups} onClose={onClose} />
  if (kept.length === 0) {
    return (
      <Stack>
        <Text size="sm" c="dimmed">Everything was left out, so there is nothing to promote.</Text>
        <Group justify="flex-end"><Button variant="default" onClick={onClose}>Close</Button></Group>
      </Stack>
    )
  }

  return (
    <Stack gap="md">
      {kept.length === 1 && versions.length > 0 && (
        <Select
          label="Version"
          data={[
            { value: '0', label: 'Newest' },
            ...versions.map((v) => ({
              value: String(v.version),
              label: `v${v.version}${v.created_at ? ` · ${formatDateTime(v.created_at)}` : ''}`,
            })),
          ]}
          value={String(kept[0].version)}
          allowDeselect={false}
          onChange={setVersion}
        />
      )}

      {preview.isLoading && <Group gap="xs"><Loader size="xs" /><Text size="sm">Checking with each environment…</Text></Group>}
      {preview.error && <Alert color="red" title="Could not run the checks">{(preview.error as Error).message}</Alert>}

      {groups.map((g) => <GroupCard key={g.link_id} group={g} onLeaveOut={leaveOut} />)}
      {unlinked.map((u) => (
        <Alert key={u.workflow_id} color="orange" icon={<IconAlertTriangle size="1rem" />}>
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm">
              Nothing is connected for {u.vhost}, so <b>{u.workflow_name}</b> has nowhere to go. An administrator
              connects it in Settings › Environments.
            </Text>
            <Button size="xs" variant="default" aria-label={`Leave out ${u.workflow_name}`} onClick={() => leaveOut(u.workflow_id)}>
              Leave out
            </Button>
          </Group>
        </Alert>
      ))}

      <Textarea
        label="Note for the approver"
        placeholder="What changed and why"
        autosize
        minRows={2}
        maxLength={1000}
        value={note}
        onChange={(e) => setNote(e.currentTarget.value)}
      />
      <Switch label="Start them once deployed" checked={start} onChange={(e) => setStart(e.currentTarget.checked)} />
      <Switch
        label="Undo the whole release if one of them starts failing"
        checked={rollback}
        onChange={(e) => setAutoRollback(e.currentTarget.checked)}
      />
      {send.error && <Alert color="red" title="Not everything was sent">{(send.error as Error).message}</Alert>}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>Cancel</Button>
        <Button
          leftSection={<IconRocket size="1rem" />}
          disabled={!settled || !preview.data?.ready}
          loading={send.isPending}
          onClick={() => send.mutate()}
        >
          Promote {plural(kept.length, 'workflow')} to {plural(Math.max(groups.length, 1), 'environment')}
        </Button>
      </Group>
    </Stack>
  )
}

function GroupCard({ group, onLeaveOut }: { group: ReleaseGroup; onLeaveOut: (workflowId: string) => void }) {
  const approval = group.previews[0]?.rules.approval
  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap">
          <Group gap={6} wrap="nowrap">
            <Badge variant="light" color="yellow" tt="none">{group.vhost}</Badge>
            <IconArrowRight size="0.9rem" aria-hidden />
            <Text fw={600}>{group.link_name} · {group.target_vhost || group.vhost}</Text>
          </Group>
          <Text size="xs" c="dimmed">{approval === 'none' ? 'Goes live at once' : 'An administrator there approves'}</Text>
        </Group>
        {group.previews.map((p) => <PreviewRow key={p.workflow_id} p={p} onLeaveOut={onLeaveOut} />)}
      </Stack>
    </Paper>
  )
}

function PreviewRow({ p, onLeaveOut }: { p: Preview; onLeaveOut: (workflowId: string) => void }) {
  const failing = p.checks.filter((c) => !c.ok)
  const Icon = p.ready ? IconCheck : IconX
  const changes = p.changes ?? []
  return (
    <Stack gap={4}>
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap">
          <ThemeIcon size={20} radius="xl" variant="light" color={p.ready ? 'green' : 'red'}><Icon size="0.8rem" /></ThemeIcon>
          <Text size="sm" fw={500}>{p.workflow_name}</Text>
          <Text size="xs" c="dimmed">
            {p.current_version > 0 ? `v${p.current_version} → v${p.from_version}` : `New · v${p.from_version}`}
          </Text>
        </Group>
        {!p.ready && (
          <Button size="xs" variant="default" aria-label={`Leave out ${p.workflow_name}`} onClick={() => onLeaveOut(p.workflow_id)}>
            Leave out
          </Button>
        )}
      </Group>
      {failing.map((c) => (
        <Text key={c.name} size="xs" c={c.blocking ? 'red' : 'yellow.8'} pl={28}>{c.detail || c.name}</Text>
      ))}
      {changes.length > 0 && (
        <Stack gap={0} pl={28}>
          {changes.map((c, i) => <Text key={i} size="xs" c="dimmed">{c.what}</Text>)}
        </Stack>
      )}
    </Stack>
  )
}

function Sent({ releases, groups, onClose }: { releases: SentRelease[]; groups: ReleaseGroup[]; onClose: () => void }) {
  return (
    <Stack>
      {releases.map((r) => {
        const g = groups.find((x) => x.link_id === r.link_id)
        const where = `${r.link_name}${g ? ` · ${g.target_vhost || g.vhost}` : ''}`
        const live = r.promotions.every((p) => p.status === 'deployed')
        return (
          <Alert key={r.link_id} color={live ? 'green' : 'blue'} icon={<IconCheck size="1rem" />}>
            {plural(r.promotions.length, 'workflow')}: the release {live ? 'is live' : 'waits for approval'} in {where}.
          </Alert>
        )
      })}
      <Group justify="flex-end"><Button onClick={onClose}>Close</Button></Group>
    </Stack>
  )
}
