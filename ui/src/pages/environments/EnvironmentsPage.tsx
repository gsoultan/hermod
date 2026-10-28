import { useState } from 'react'
import {
  Alert, Anchor, Badge, Box, Button, Grid, Group, Loader, Modal, Paper, Stack, Text, Textarea, Title,
} from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link as RouterLink } from '@tanstack/react-router'
import { notifications } from '@mantine/notifications'
import { IconArrowBackUp, IconArrowRight, IconCheck, IconRocket } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { getSessionRole, getSessionUser } from '@/auth/session'
import { formatDateTime } from '@/utils/dateUtils'
import {
  approvePromotion, environmentKeys, groupByRelease, rejectPromotion, releaseStatus, roleColor, roleLabel,
  rollbackPromotion, statusColor, statusLabel, useInstance, usePeers, usePromotions, useReady, useRules, useSent,
  workflowNames, type ApprovalRule, type Promotion, type ReadyLane, type ReleaseItem, type Rules,
} from '@/lib/environments'
import { ReleaseDialog } from './ReleaseDialog'

/**
 * Environments: on a sending install, one lane per connected vhost with what
 * changed since it was last promoted, and one button that promotes all of it;
 * on a receiving install, the releases waiting for an administrator.
 */
export function EnvironmentsPage() {
  const { data: instance, isLoading } = useInstance()
  const role = instance?.role ?? ''
  const sends = role === 'development' || role === 'staging'
  const receives = role === 'staging' || role === 'production'

  return (
    <Stack gap="xl" maw={1120}>
      <Group gap="sm">
        <Title order={2}>Environments</Title>
        {role && <Badge color={roleColor[role]} variant="light" size="lg">{instance?.name || roleLabel[role]}</Badge>}
      </Group>
      {isLoading && <Loader size="sm" aria-label="Loading" />}
      {!isLoading && role === '' && (
        <Alert color="blue">This instance has no environment role yet. An administrator sets it in Settings › Environments.</Alert>
      )}
      {receives ? (
        <Grid gap="xl">
          <Grid.Col span={{ base: 12, md: 8 }}>
            <Stack gap="xl">
              <Inbox />
              {sends && <Lanes />}
            </Stack>
          </Grid.Col>
          <Grid.Col span={{ base: 12, md: 4 }}><SidePanel /></Grid.Col>
        </Grid>
      ) : sends && <Lanes />}
    </Stack>
  )
}

const canPromote = () => ['Administrator', 'Editor'].includes(getSessionRole() ?? '')

function Lanes() {
  const promoter = canPromote()
  const { data: lanes, isLoading } = useReady(promoter)
  const { data: sent } = useSent(promoter)
  const [release, setRelease] = useState<ReleaseItem[] | null>(null)

  if (!promoter) {
    return <Text size="sm" c="dimmed">Editors and administrators promote workflows from here.</Text>
  }
  const ready = (lanes ?? []).flatMap((l) => (l.workflows ?? []).map((w) => ({ workflow_id: w.id, version: 0 })))

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Stack gap={0}>
          <Title order={3}>Ready to promote</Title>
          <Text size="sm" c="dimmed">What changed in each vhost since it last went out.</Text>
        </Stack>
        <Button
          size="md"
          leftSection={<IconRocket size="1.1rem" />}
          disabled={ready.length === 0}
          onClick={() => setRelease(ready)}
        >
          Promote all ready ({ready.length})
        </Button>
      </Group>
      {isLoading && <Loader size="sm" />}
      {!isLoading && (lanes ?? []).length === 0 && (
        <Paper withBorder radius="lg" p="xl">
          <Text size="sm" c="dimmed" ta="center">
            No vhost is connected to another environment yet. An administrator connects one in Settings › Environments.
          </Text>
        </Paper>
      )}
      {(lanes ?? []).map((l) => <Lane key={l.link_id} lane={l} onPromote={setRelease} />)}
      <SentList sent={sent ?? []} />
      <ReleaseDialog opened={release !== null} onClose={() => setRelease(null)} items={release ?? []} />
    </Stack>
  )
}

function Lane({ lane, onPromote }: { lane: ReadyLane; onPromote: (items: ReleaseItem[]) => void }) {
  const workflows = lane.workflows ?? []
  return (
    <Paper withBorder radius="lg" p="lg">
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <IconArrowRight size="1rem" aria-hidden />
            <Text fw={600}>{lane.vhost || 'Every vhost'} → {lane.link_name} · {lane.target_vhost || lane.vhost}</Text>
          </Group>
          {workflows.length > 0 ? (
            <Button
              size="xs"
              variant="light"
              aria-label={`Promote ${lane.vhost}`}
              onClick={() => onPromote(workflows.map((w) => ({ workflow_id: w.id, version: 0 })))}
            >
              Promote {workflows.length}
            </Button>
          ) : (
            <Badge color="green" variant="light" leftSection={<IconCheck size="0.8rem" />}>Up to date</Badge>
          )}
        </Group>
        {workflows.map((w) => (
          <Group key={w.id} justify="space-between" wrap="nowrap" pl="lg">
            <Text size="sm">{w.name}</Text>
            {w.last_sent_version > 0
              ? <Text size="xs" c="dimmed">v{w.last_sent_version} → v{w.version}</Text>
              : <Badge size="sm" variant="light" color="blue">New</Badge>}
          </Group>
        ))}
      </Stack>
    </Paper>
  )
}

function SentList({ sent }: { sent: Promotion[] }) {
  const releases = groupByRelease(sent).slice(0, 10)
  if (releases.length === 0) return null
  return (
    <Stack gap="xs">
      <Title order={4}>Recently sent</Title>
      <Paper withBorder radius="lg">
        {releases.map((ps, i) => {
          const status = releaseStatus(ps)
          return (
            <Group key={ps[0].id} justify="space-between" wrap="nowrap" p="md" style={i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined}>
              <Stack gap={0}>
                <Text size="sm" fw={600}>
                  {ps.length === 1 ? ps[0].workflow_name : `${ps.length} workflows`} to {ps[0].from_env}
                </Text>
                {ps.length > 1 && <Text size="xs" c="dimmed">{workflowNames(ps)}</Text>}
                <Text size="xs" c="dimmed">{formatDateTime(ps[0].created_at)}{ps[0].reason ? ` · ${ps[0].reason}` : ''}</Text>
              </Stack>
              <Badge color={statusColor[status]} variant="light">{statusLabel[status]}</Badge>
            </Group>
          )
        })}
      </Paper>
    </Stack>
  )
}

function Inbox() {
  const { data: received, isLoading } = usePromotions()
  const releases = groupByRelease(received ?? [])
  const waiting = releases.filter((ps) => ps.some((p) => p.status === 'pending' || p.status === 'deploying'))
  const decided = releases.filter((ps) => !waiting.includes(ps))

  // Undo only reaches a release that is still the newest deploy of every
  // workflow in it; the list is newest first.
  const newest = new Set<string>()
  const seen = new Set<string>()
  for (const p of received ?? []) {
    if (p.status === 'deployed' && !seen.has(p.workflow_id)) newest.add(p.id)
    if (p.status === 'deployed' || p.status === 'rolled_back') seen.add(p.workflow_id)
  }
  const undoable = (ps: Promotion[]) => ps.every((p) => newest.has(p.id))

  if (isLoading) return <Loader size="sm" />
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={3}>Waiting for you</Title>
        {waiting.length === 0
          ? <Text size="sm" c="dimmed">Nothing is waiting. Releases sent here show up for approval.</Text>
          : waiting.map((ps) => <ReleaseCard key={ps[0].id} ps={ps} />)}
      </Stack>
      {decided.length > 0 && (
        <Stack gap="xs">
          <Title order={4}>Recently live</Title>
          <Paper withBorder radius="lg">
            {decided.slice(0, 20).map((ps, i) => <DecidedRow key={ps[0].id} ps={ps} first={i === 0} canUndo={undoable(ps)} />)}
          </Paper>
        </Stack>
      )}
    </Stack>
  )
}

const approverFor = (rules: Rules | undefined, vhost: string): ApprovalRule | undefined =>
  rules?.by_vhost?.[vhost]?.approval ?? rules?.approval

function ReleaseCard({ ps }: { ps: Promotion[] }) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const { data: rules } = useRules()
  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState('')
  const head = ps[0]
  const names = workflowNames(ps)
  const isAdmin = getSessionRole() === 'Administrator'
  const ownSend = approverFor(rules, head.vhost) === 'admin_except_sender' && getSessionUser()?.username === head.sender

  const refresh = () => queryClient.invalidateQueries({ queryKey: environmentKeys.promotions })
  const onError = async (err: Error) => {
    await refresh()
    notifications.show({ color: 'red', title: 'Not done', message: err.message })
  }
  // Each acts on the whole release, whichever member it names.
  const approve = useMutation({
    mutationFn: () => approvePromotion(head.id),
    onSuccess: async () => { await refresh(); notifications.show({ color: 'green', message: `${names} ${ps.length === 1 ? 'is' : 'are'} live.` }) },
    onError,
  })
  const reject = useMutation({
    mutationFn: () => rejectPromotion(head.id, reason.trim()),
    onSuccess: async () => { setRejecting(false); await refresh() },
    onError,
  })

  return (
    <Paper withBorder radius="lg" p="lg" shadow="xs">
      <Stack gap="md">
        <Group justify="space-between" wrap="nowrap">
          <Text fw={600}>{head.from_env} → {head.vhost}</Text>
          <Text size="sm" c="dimmed">{head.sender} · {formatDateTime(head.created_at)}</Text>
        </Group>
        {head.note && <Text>“{head.note}”</Text>}
        <Paper withBorder radius="md">
          {ps.map((p, i) => (
            <Box key={p.id} p="sm" style={i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined}>
              <Group justify="space-between" wrap="nowrap">
                <Text size="sm" fw={600}>{p.workflow_name}</Text>
                <Text size="xs" c="dimmed">
                  {(p.changes ?? []).length > 0 ? `${(p.changes ?? []).length} changes` : `v${p.from_version}`}
                </Text>
              </Group>
              {(p.changes ?? []).map((c, j) => <Text key={j} size="xs" c="dimmed">{c.what}</Text>)}
            </Box>
          ))}
        </Paper>
        {head.status === 'deploying' && <Group gap="xs"><Loader size="xs" /><Text size="sm">Going live…</Text></Group>}
        {head.auto_rollback && (
          <Text size="sm" c="green.8">Undone on its own if it fails in the first 10 minutes</Text>
        )}
        {isAdmin && head.status === 'pending' && (
          <Group justify="flex-end" gap="sm">
            {ownSend && <Text size="xs" c="dimmed">You sent this, so another administrator approves it.</Text>}
            <Button variant="default" onClick={() => setRejecting(true)}>Reject</Button>
            <Button
              color="green"
              disabled={ownSend}
              loading={approve.isPending}
              onClick={async () => {
                if (await confirm({
                  title: `Approve ${ps.length === 1 ? head.workflow_name : `${ps.length} workflows`}`,
                  message: `${names} ${ps.length === 1 ? 'goes' : 'go'} live here now${head.start ? ' and start' + (ps.length === 1 ? 's' : '') : ''}.${head.auto_rollback ? ' If one starts failing within 10 minutes, the whole release is undone on its own.' : ''}`,
                  confirmLabel: 'Approve and go live',
                })) approve.mutate()
              }}
            >
              Approve and go live
            </Button>
          </Group>
        )}
      </Stack>
      <Modal opened={rejecting} onClose={() => setRejecting(false)} title={`Reject ${names}`}>
        <Stack>
          <Textarea
            label="Why"
            description="Kept with this release in the history here."
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
    </Paper>
  )
}

function DecidedRow({ ps, first, canUndo }: { ps: Promotion[]; first: boolean; canUndo: boolean }) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const head = ps[0]
  const status = releaseStatus(ps)
  const names = workflowNames(ps)
  const isAdmin = getSessionRole() === 'Administrator'

  const undo = useMutation({
    mutationFn: () => rollbackPromotion(head.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: environmentKeys.promotions })
      notifications.show({ color: 'green', message: `${names} ${ps.length === 1 ? 'is' : 'are'} back to how ${ps.length === 1 ? 'it was' : 'they were'}.` })
    },
    onError: (err: Error) => notifications.show({ color: 'red', title: 'Not undone', message: err.message }),
  })

  return (
    <Group justify="space-between" wrap="nowrap" p="md" style={first ? undefined : { borderTop: '1px solid var(--mantine-color-default-border)' }}>
      <Group gap="sm" wrap="nowrap">
        <Box w={8} h={8} style={{ borderRadius: '50%', flex: 'none', background: `var(--mantine-color-${statusColor[status]}-6)` }} aria-hidden />
        <Stack gap={0}>
          <Text size="sm" fw={600}>{ps.length === 1 ? head.workflow_name : `${ps.length} workflows`}</Text>
          <Text size="xs" c="dimmed">
            {ps.length > 1 ? `${names} · ` : ''}{head.from_env} → {head.vhost} · {formatDateTime(head.decided_at || head.created_at)}
            {head.decided_by ? ` · ${head.decided_by}` : ''}
          </Text>
          {head.reason && <Text size="xs" c={status === 'failed' ? 'red' : 'dimmed'}>{head.reason}</Text>}
        </Stack>
      </Group>
      <Group gap="xs" wrap="nowrap">
        <Badge color={statusColor[status]} variant="light">{statusLabel[status]}</Badge>
        {isAdmin && canUndo && (
          <Button
            size="xs"
            variant="default"
            leftSection={<IconArrowBackUp size="0.9rem" />}
            loading={undo.isPending}
            onClick={async () => {
              if (await confirm({
                title: `Undo ${ps.length === 1 ? head.workflow_name : `${ps.length} workflows`}`,
                message: `${names} ${ps.length === 1 ? 'goes' : 'go'} back to how ${ps.length === 1 ? 'it was' : 'they were'} before this release. A workflow the release created is stopped.`,
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

const approvalShort: Record<ApprovalRule, string> = {
  admin_except_sender: 'Another admin approves',
  any_admin: 'Any admin approves',
  none: 'Goes live without approval',
}

function minutes(m: number): string {
  if (m % 1440 === 0) return `${m / 1440} day${m === 1440 ? '' : 's'}`
  if (m % 60 === 0) return `${m / 60} hour${m === 60 ? '' : 's'}`
  return `${m} min`
}

function SidePanel() {
  const isAdmin = getSessionRole() === 'Administrator'
  const { data: peers } = usePeers(isAdmin)
  const { data: rules } = useRules()

  return (
    <Stack gap="md">
      {isAdmin && (
        <Paper withBorder radius="lg" p="lg">
          <Stack gap="sm">
            <Text fw={700}>Sending here</Text>
            {(peers ?? []).length === 0 && <Text size="sm" c="dimmed">Nothing is connected to this instance yet.</Text>}
            {(peers ?? []).map((p) => (
              <Stack key={p.id} gap={0}>
                <Text size="sm" fw={600}>{p.name}</Text>
                <Text size="xs" c="dimmed">{p.vhost ? `into ${p.vhost}` : 'into any vhost (older link)'}</Text>
              </Stack>
            ))}
            <Anchor component={RouterLink} to="/settings" search={{ tab: 'environments' } as any} size="sm" fw={600}>
              Manage in Settings
            </Anchor>
          </Stack>
        </Paper>
      )}
      {rules && (
        <Paper withBorder radius="lg" p="lg">
          <Stack gap={6}>
            <Text fw={700}>Rules</Text>
            <Text size="sm">{approvalShort[rules.approval]}</Text>
            {rules.auto_rollback && <Text size="sm">Undo the whole release if it fails</Text>}
            {rules.min_soak_minutes > 0 && <Text size="sm">Wait {minutes(rules.min_soak_minutes)} in the sending environment</Text>}
            {Object.entries(rules.by_vhost ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([v, r]) => (
              <Text key={v} size="sm">
                {v}: {approvalShort[r.approval].toLowerCase()}{r.min_soak_minutes > 0 ? `, wait ${minutes(r.min_soak_minutes)}` : ''}
              </Text>
            ))}
          </Stack>
        </Paper>
      )}
    </Stack>
  )
}
