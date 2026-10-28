import { useState } from 'react'
import {
  Alert, Badge, Button, Card, Group, Loader, Modal, Stack, Tabs, Text, Textarea, Title,
} from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { IconArrowBackUp, IconCheck, IconRocket, IconX } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { getSessionRole, getSessionUser } from '@/auth/session'
import { formatDateTime } from '@/utils/dateUtils'
import {
  approvePromotion, environmentKeys, rejectPromotion, rollbackPromotion, statusColor, statusLabel, useInstance,
  usePromotions, useRules, useSent, type Promotion,
} from '@/lib/environments'

/**
 * Promotions: the workflows linked instances sent here, waiting for a decision
 * or already deployed, and the ones this instance sent elsewhere.
 */
export function PromotionsPage() {
  const { data: instance, isLoading: instanceLoading } = useInstance()
  const role = instance?.role ?? ''
  const receives = role === 'staging' || role === 'production'
  const sends = role === 'development' || role === 'staging'
  const { data: received, isLoading } = usePromotions(receives)
  const { data: sent } = useSent(sends)

  const pending = (received ?? []).filter((p) => p.status === 'pending' || p.status === 'deploying')
  const decided = (received ?? []).filter((p) => p.status !== 'pending' && p.status !== 'deploying')
  // Rollback only reaches the newest deploy of each workflow.
  const newest = new Set<string>()
  const seen = new Set<string>()
  for (const p of received ?? []) {
    if (p.status === 'deployed' && !seen.has(p.workflow_id)) newest.add(p.id)
    if (p.status === 'deployed' || p.status === 'rolled_back') seen.add(p.workflow_id)
  }

  return (
    <Stack gap="lg">
      <Stack gap={4}>
        <Title order={2}>Promotions</Title>
        <Text size="sm" c="dimmed">Workflows moving between environments, and who let them through.</Text>
      </Stack>
      {role === '' && (
        <Alert color="blue">This instance has no environment role yet. An administrator sets it in Settings › Environments.</Alert>
      )}
      {instanceLoading && <Loader size="sm" aria-label="Loading" />}
      {/* Keyed by role: the first tab depends on it, and it arrives after the first render. */}
      <Tabs key={role} defaultValue={receives ? 'received' : 'sent'} keepMounted={false}>
        <Tabs.List>
          {receives && <Tabs.Tab value="received">Received{pending.length > 0 ? ` (${pending.length} waiting)` : ''}</Tabs.Tab>}
          {sends && <Tabs.Tab value="sent">Sent</Tabs.Tab>}
        </Tabs.List>
        {receives && (
          <Tabs.Panel value="received" pt="md">
            {isLoading ? <Loader size="sm" /> : (
              <Stack gap="lg">
                <Stack gap="sm">
                  <Title order={4}>Waiting for approval</Title>
                  {pending.length === 0
                    ? <Text size="sm" c="dimmed">Nothing is waiting.</Text>
                    : pending.map((p) => <PromotionCard key={p.id} p={p} />)}
                </Stack>
                {decided.length > 0 && (
                  <Stack gap="sm">
                    <Title order={4}>History</Title>
                    {decided.map((p) => <PromotionCard key={p.id} p={p} canRollback={newest.has(p.id)} />)}
                  </Stack>
                )}
              </Stack>
            )}
          </Tabs.Panel>
        )}
        {sends && (
          <Tabs.Panel value="sent" pt="md">
            <Stack gap="sm">
              {(sent ?? []).length === 0
                ? <Text size="sm" c="dimmed">Nothing sent yet. Open a workflow and click Promote.</Text>
                : (sent ?? []).map((p) => <PromotionCard key={p.id} p={p} outgoing />)}
            </Stack>
          </Tabs.Panel>
        )}
      </Tabs>
    </Stack>
  )
}

function PromotionCard({ p, outgoing = false, canRollback = false }: { p: Promotion; outgoing?: boolean; canRollback?: boolean }) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const { data: rules } = useRules(!outgoing)
  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState('')
  const isAdmin = getSessionRole() === 'Administrator'
  const me = getSessionUser()?.username
  const ownSend = rules?.approval === 'admin_except_sender' && me === p.sender

  const refresh = () => queryClient.invalidateQueries({ queryKey: environmentKeys.promotions })
  const onError = async (err: Error) => {
    await refresh()
    notifications.show({ color: 'red', title: 'Not done', message: err.message })
  }
  const approve = useMutation({
    mutationFn: () => approvePromotion(p.id),
    onSuccess: async () => { await refresh(); notifications.show({ color: 'green', message: `${p.workflow_name} is deployed.` }) },
    onError,
  })
  const reject = useMutation({
    mutationFn: () => rejectPromotion(p.id, reason.trim()),
    onSuccess: async () => { setRejecting(false); await refresh() },
    onError,
  })
  const rollback = useMutation({
    mutationFn: () => rollbackPromotion(p.id),
    onSuccess: async () => { await refresh(); notifications.show({ color: 'green', message: `${p.workflow_name} is back to how it was.` }) },
    onError,
  })

  return (
    <Card withBorder radius="md" padding="md">
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs">
            <IconRocket size="1rem" aria-hidden />
            <Text fw={600}>{p.workflow_name}</Text>
            <Text size="sm" c="dimmed">{p.from_version > 0 ? `v${p.from_version}` : 'latest'} {outgoing ? 'to' : 'from'} {p.from_env}</Text>
          </Group>
          <Badge color={statusColor[p.status]} variant="light">{statusLabel[p.status]}</Badge>
        </Group>
        <Text size="xs" c="dimmed">
          Sent by {p.sender} on {formatDateTime(p.created_at)}
          {p.vhost && p.vhost !== 'default' ? ` · vhost ${p.vhost}` : ''}
          {p.decided_by ? ` · ${p.status === 'rejected' ? 'rejected' : 'decided'} by ${p.decided_by} ${formatDateTime(p.decided_at)}` : ''}
          {p.applied_version ? ` · now version ${p.applied_version}` : ''}
        </Text>
        {p.note && <Text size="sm">“{p.note}”</Text>}
        {(p.changes ?? []).length > 0 && (
          <Stack gap={2}>
            {(p.changes ?? []).map((c, i) => <Text key={i} size="sm">• {c.what}</Text>)}
          </Stack>
        )}
        {p.reason && <Alert color={p.status === 'failed' ? 'red' : 'gray'} p="xs">{p.reason}</Alert>}
        {!outgoing && isAdmin && p.status === 'pending' && (
          <Group justify="flex-end" gap="xs">
            {ownSend && <Text size="xs" c="dimmed">You sent this, so another administrator approves it.</Text>}
            <Button size="xs" variant="default" leftSection={<IconX size="0.9rem" />} onClick={() => setRejecting(true)}>Reject</Button>
            <Button
              size="xs"
              color="green"
              leftSection={<IconCheck size="0.9rem" />}
              disabled={ownSend}
              loading={approve.isPending}
              onClick={async () => {
                if (await confirm({
                  title: `Deploy ${p.workflow_name}`,
                  message: `It goes live here now${p.start ? ' and starts' : ''}. ${p.auto_rollback ? 'If it starts failing within 10 minutes it is rolled back on its own.' : ''}`,
                  confirmLabel: 'Approve and deploy',
                })) approve.mutate()
              }}
            >
              Approve and deploy
            </Button>
          </Group>
        )}
        {!outgoing && isAdmin && canRollback && (
          <Group justify="flex-end">
            <Button
              size="xs"
              variant="light"
              color="orange"
              leftSection={<IconArrowBackUp size="0.9rem" />}
              loading={rollback.isPending}
              onClick={async () => {
                if (await confirm({
                  title: `Roll back ${p.workflow_name}`,
                  message: p.previous_version
                    ? `It goes back to version ${p.previous_version}, the one before this promotion.`
                    : 'This promotion created the workflow, so rolling back stops it.',
                  confirmLabel: 'Roll back',
                  danger: true,
                })) rollback.mutate()
              }}
            >
              Roll back
            </Button>
          </Group>
        )}
      </Stack>
      <Modal opened={rejecting} onClose={() => setRejecting(false)} title={`Reject ${p.workflow_name}`}>
        <Stack>
          <Textarea
            label="Why"
            description="Kept with this promotion in the history here."
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
    </Card>
  )
}
