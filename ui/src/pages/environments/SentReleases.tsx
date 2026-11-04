import { useState } from 'react'
import { Badge, Button, Group, Modal, Paper, Stack, Text, Textarea, Title } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { IconArrowBackUp, IconCheck } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { getSessionRole } from '@/auth/session'
import { formatDateTime } from '@/utils/dateUtils'
import {
  approveSent, environmentKeys, groupByRelease, rejectSent, releaseStatus, rollbackSent, statusColor, statusLabel,
  useLinks, workflowNames, type Promotion,
} from '@/lib/environments'
import { StartChoice, startLine } from './SecretValues'

const title = (ps: Promotion[]) => (ps.length === 1 ? ps[0].workflow_name : `${ps.length} workflows`)
const border = (i: number) => (i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined)

/**
 * What this instance sent. Where the instance it went to lets administrators
 * here decide, a waiting release is approved or rejected from here, and a
 * live one undone, without signing in there.
 */
export function SentReleases({ sent }: { sent: Promotion[] }) {
  const isAdmin = getSessionRole() === 'Administrator'
  const { data: links } = useLinks()
  const deciding = new Set((links ?? []).filter((l) => l.decide).map((l) => l.id))
  const releases = groupByRelease(sent)
  const mayDecide = (ps: Promotion[]) => isAdmin && deciding.has(ps[0].peer_id)
  const waiting = releases.filter((ps) => releaseStatus(ps) === 'pending' && mayDecide(ps))
  const rest = releases.filter((ps) => !waiting.includes(ps)).slice(0, 10)

  // Undo reaches only a release that is still the newest deploy of each of
  // its workflows there; the list is newest first.
  const newest = new Set<string>()
  const seen = new Set<string>()
  for (const p of sent) {
    const key = `${p.peer_id}|${p.workflow_id}`
    if (p.status === 'deployed' && !seen.has(key)) newest.add(p.id)
    if (p.status === 'deployed' || p.status === 'rolled_back') seen.add(key)
  }
  const undoable = (ps: Promotion[]) => mayDecide(ps) && ps.every((p) => newest.has(p.id))

  if (releases.length === 0) return null
  return (
    <Stack gap="lg">
      {waiting.length > 0 && (
        <Stack gap="xs">
          <Title order={4}>Needs your approval</Title>
          <Text size="sm" c="dimmed">Approve here and it goes live there. Nobody has to sign in to the other instance.</Text>
          <Paper withBorder radius="lg">
            {waiting.map((ps, i) => <WaitingRow key={ps[0].id} ps={ps} style={border(i)} />)}
          </Paper>
        </Stack>
      )}
      {rest.length > 0 && (
        <Stack gap="xs">
          <Title order={4}>Recently sent</Title>
          <Paper withBorder radius="lg">
            {rest.map((ps, i) => <SentRow key={ps[0].id} ps={ps} canUndo={undoable(ps)} style={border(i)} />)}
          </Paper>
        </Stack>
      )}
    </Stack>
  )
}

function useRefresh() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: environmentKeys.sent })
}

function WaitingRow({ ps, style }: { ps: Promotion[]; style?: React.CSSProperties }) {
  const confirm = useConfirm()
  const refresh = useRefresh()
  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState('')
  const head = ps[0]
  const names = workflowNames(ps)
  const onError = async (err: Error) => {
    await refresh()
    notifications.show({ color: 'red', title: 'Not done', message: err.message })
  }
  // Secrets nobody typed a value for: they decide whether it starts there.
  const empty = head.start
    ? [...new Set(ps.flatMap((p) => (p.secrets ?? []).filter((n) => !(p.secrets_filled ?? []).includes(n))))].sort()
    : []
  const [startAnyway, setStartAnyway] = useState(false)
  // Each acts on the whole release there, whichever member it names.
  const approve = useMutation({
    mutationFn: () => approveSent(head.id, empty.length > 0 && startAnyway),
    onSuccess: async () => {
      await refresh()
      notifications.show({ color: 'green', message: `${names} ${ps.length === 1 ? 'is' : 'are'} live in ${head.from_env}.` })
    },
    onError,
  })
  const reject = useMutation({
    mutationFn: () => rejectSent(head.id, reason.trim()),
    onSuccess: async () => { setRejecting(false); await refresh() },
    onError,
  })

  return (
    <Group justify="space-between" wrap="wrap" gap="sm" p="md" style={style}>
      <Stack gap={0}>
        <Text size="sm" fw={600}>{title(ps)} to {head.from_env}</Text>
        {ps.length > 1 && <Text size="xs" c="dimmed">{names}</Text>}
        <Text size="xs" c="dimmed">Sent by {head.sender} · {formatDateTime(head.created_at)}</Text>
        <StartChoice empty={empty} startAnyway={startAnyway} onChange={setStartAnyway} />
      </Stack>
      <Group gap="xs" wrap="nowrap">
        <Button size="xs" variant="default" aria-label={`Reject ${title(ps)}`} onClick={() => setRejecting(true)}>Reject</Button>
        <Button
          size="xs"
          color="green"
          leftSection={<IconCheck size="0.9rem" />}
          aria-label={`Approve ${title(ps)}`}
          loading={approve.isPending}
          onClick={async () => {
            if (await confirm({
              title: `Approve ${title(ps)}`,
              message: `${names} ${ps.length === 1 ? 'goes' : 'go'} live in ${head.from_env} now${startLine(ps.length, head.start, empty, startAnyway)}. ${head.from_env}'s rules still apply: if it wants someone other than the sender, it refuses.`,
              confirmLabel: 'Approve and go live',
            })) approve.mutate()
          }}
        >
          Approve
        </Button>
      </Group>
      <Modal opened={rejecting} onClose={() => setRejecting(false)} title={`Reject ${names}`}>
        <Stack>
          <Textarea
            label="Why"
            description={`Kept with this release in ${head.from_env}'s history.`}
            autosize
            minRows={2}
            maxLength={1000}
            value={reason}
            onChange={(e) => setReason(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRejecting(false)}>Cancel</Button>
            <Button color="red" loading={reject.isPending} onClick={() => reject.mutate()}>Reject</Button>
          </Group>
        </Stack>
      </Modal>
    </Group>
  )
}

function SentRow({ ps, canUndo, style }: { ps: Promotion[]; canUndo: boolean; style?: React.CSSProperties }) {
  const confirm = useConfirm()
  const refresh = useRefresh()
  const head = ps[0]
  const status = releaseStatus(ps)
  const names = workflowNames(ps)
  const undo = useMutation({
    mutationFn: () => rollbackSent(head.id),
    onSuccess: async () => {
      await refresh()
      notifications.show({ color: 'green', message: `${names} ${ps.length === 1 ? 'is' : 'are'} back to how ${ps.length === 1 ? 'it was' : 'they were'} in ${head.from_env}.` })
    },
    onError: async (err: Error) => {
      await refresh()
      notifications.show({ color: 'red', title: 'Not undone', message: err.message })
    },
  })

  return (
    <Group justify="space-between" wrap="nowrap" p="md" style={style}>
      <Stack gap={0}>
        <Text size="sm" fw={600}>{title(ps)} to {head.from_env}</Text>
        {ps.length > 1 && <Text size="xs" c="dimmed">{names}</Text>}
        <Text size="xs" c="dimmed">
          {formatDateTime(head.created_at)}{head.decided_by ? ` · ${head.decided_by}` : ''}{head.reason ? ` · ${head.reason}` : ''}
        </Text>
      </Stack>
      <Group gap="xs" wrap="nowrap">
        <Badge color={statusColor[status]} variant="light">{statusLabel[status]}</Badge>
        {canUndo && (
          <Button
            size="xs"
            variant="default"
            leftSection={<IconArrowBackUp size="0.9rem" />}
            aria-label={`Undo ${title(ps)}`}
            loading={undo.isPending}
            onClick={async () => {
              if (await confirm({
                title: `Undo ${title(ps)} in ${head.from_env}`,
                message: `${names} ${ps.length === 1 ? 'goes' : 'go'} back to how ${ps.length === 1 ? 'it was' : 'they were'} in ${head.from_env} before this release. A workflow the release created is stopped.`,
                confirmLabel: 'Undo',
                danger: true,
              })) undo.mutate()
            }}
          >
            Undo
          </Button>
        )}
      </Group>
    </Group>
  )
}
