import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Alert, Badge, Button, Code, Group, Paper, Stack, Table, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconAlertCircle, IconArrowBackUp, IconCheck, IconX } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { formatDateTime } from '@/utils/dateUtils'
import { ApiRequestError } from '@/lib/executions'
import {
  approveProposal, formatPatchValue, proposalsKey, rejectProposal, rollbackWorkflow, type Proposal, type ProposalEffect,
} from '@/lib/proposals'

const statusMeta: Record<string, { label: string; color: string }> = {
  pending: { label: 'Pending', color: 'blue' },
  applied: { label: 'Applied', color: 'green' },
  rejected: { label: 'Rejected', color: 'gray' },
  stale: { label: 'Stale', color: 'yellow' },
}

interface Failure {
  title: string
  message: string
  color: string
}

/** Why a decision failed, in the terms the handler's status codes mean. */
function describeFailure(err: unknown, action: 'approve' | 'reject' | 'rollback'): Failure {
  const status = err instanceof ApiRequestError ? err.status : 0
  const message = (err as Error)?.message || 'The request failed.'
  if (status === 409) {
    return {
      title: 'This fix can no longer be applied',
      message: `${message}. The list has been refreshed; a stale proposal stays here for the record.`,
      color: 'yellow',
    }
  }
  if (status === 422) {
    return { title: 'Workflow validation rejected this fix', message: `${message}. The workflow was not changed.`, color: 'red' }
  }
  const verb = action === 'approve' ? 'Approving' : action === 'reject' ? 'Rejecting' : 'Rolling back'
  return { title: `${verb} failed`, message, color: 'red' }
}

const verdictMeta: Record<ProposalEffect['verdict'], { text: string; color: string }> = {
  better: { text: 'The flow runs better since this fix.', color: 'green' },
  worse: { text: 'The flow runs worse since this fix. Roll it back if that was not expected.', color: 'red' },
  same: { text: 'No measurable change since this fix.', color: 'dimmed' },
}

const ms = (v: number) => `${v.toFixed(1)} ms`
const share = (v: number) => `${(v * 100).toFixed(1)}%`
const rate = (v: number) => `${v.toFixed(1)} msg/s`

/** The flow before an applied fix and since, in the numbers the fix is judged by. */
function EffectTable({ title, effect }: { title: string; effect: ProposalEffect }) {
  const verdict = verdictMeta[effect.verdict] ?? verdictMeta.same
  const rows: [string, string, string][] = [
    ['Step time per message', ms(effect.before.step_time_ms), ms(effect.after.step_time_ms)],
    ['Failures', share(effect.before.error_rate), share(effect.after.error_rate)],
    // Shown, not judged: throughput follows what the source sends.
    ['Throughput', rate(effect.before.throughput), rate(effect.after.throughput)],
  ]
  return (
    <Stack gap={4}>
      <Text size="sm" fw={600} c={verdict.color}>{verdict.text}</Text>
      <Table.ScrollContainer minWidth={260}>
        <Table aria-label={`Effect of ${title}`} verticalSpacing={4} fz="sm" withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Reading</Table.Th>
              <Table.Th ta="right">Before</Table.Th>
              <Table.Th ta="right">Since</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map(([label, before, after]) => (
              <Table.Tr key={label}>
                <Table.Td>{label}</Table.Td>
                <Table.Td ta="right" style={{ whiteSpace: 'nowrap' }}>{before}</Table.Td>
                <Table.Td ta="right" style={{ whiteSpace: 'nowrap' }}>{after}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Stack>
  )
}

interface ProposalCardProps {
  workflowId: string
  proposal: Proposal
  canDecide: boolean
}

export function ProposalCard({ workflowId, proposal: p, canDecide }: ProposalCardProps) {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const [failure, setFailure] = useState<Failure | null>(null)

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: proposalsKey(workflowId) })
    queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] })
    queryClient.invalidateQueries({ queryKey: ['versions', workflowId] })
  }

  const approve = useMutation({
    mutationFn: () => approveProposal(workflowId, p.id),
    onSuccess: (applied) => {
      setFailure(null)
      notifications.show({
        title: 'Fix applied',
        message: applied.applied_version ? `Saved as workflow version ${applied.applied_version}.` : p.title,
        color: 'green',
      })
      refresh()
    },
    onError: (err) => {
      setFailure(describeFailure(err, 'approve'))
      refresh()
    },
  })

  const reject = useMutation({
    mutationFn: () => rejectProposal(workflowId, p.id),
    onSuccess: () => {
      setFailure(null)
      notifications.show({ title: 'Proposal rejected', message: p.title, color: 'gray' })
      refresh()
    },
    onError: (err) => {
      setFailure(describeFailure(err, 'reject'))
      refresh()
    },
  })

  const rollback = useMutation({
    mutationFn: (version: number) => rollbackWorkflow(workflowId, version),
    onSuccess: (_data, version) => {
      setFailure(null)
      notifications.show({ title: 'Rolled back', message: `The workflow is back to version ${version}.`, color: 'green' })
      refresh()
    },
    onError: (err) => setFailure(describeFailure(err, 'rollback')),
  })

  // Every decision is asked for before the mutation: a cancelled confirmation
  // sends nothing.
  const askApprove = async () => {
    const ok = await confirm({
      title: 'Apply this fix?',
      message: <>Apply &ldquo;{p.title}&rdquo; to the workflow.</>,
      consequence: 'The change is saved as a new version of the workflow, and can be rolled back from here or from History.',
      confirmLabel: 'Apply fix',
    })
    if (ok) approve.mutate()
  }

  const askRollback = async (version: number) => {
    const ok = await confirm({
      title: 'Roll back this fix?',
      message: `Restore the workflow to version ${version}, the version before this fix was applied.`,
      consequence: 'Every change made after that version, not only this fix, is replaced.',
      confirmLabel: 'Roll back',
      danger: true,
    })
    if (ok) rollback.mutate(version)
  }

  const meta = statusMeta[p.status] ?? { label: p.status, color: 'gray' }
  const patch = p.patch ?? []
  const busy = approve.isPending || reject.isPending || rollback.isPending

  return (
    <Paper withBorder p="md" radius="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap={4} style={{ flex: 1, minWidth: 240 }}>
            <Group gap="xs">
              <Text fw={700}>{p.title}</Text>
              <Badge variant="light" color={meta.color}>{meta.label}</Badge>
              <Badge variant="outline" color="gray" size="sm">{p.kind}</Badge>
            </Group>
            <Text size="sm">{p.reason}</Text>
            <Text size="xs" c="dimmed">
              {p.node_id && <>Node <Code>{p.node_id}</Code> · </>}
              Seen {p.occurrences} time{p.occurrences === 1 ? '' : 's'}, last {formatDateTime(p.last_seen_at)} · first
              proposed {formatDateTime(p.created_at)}
            </Text>
            {p.status === 'applied' && (
              <Text size="xs" c="dimmed">
                Applied as version {p.applied_version ?? '?'}
                {p.decided_by && ` by ${p.decided_by}`}
                {p.decided_at && `, ${formatDateTime(p.decided_at)}`}
              </Text>
            )}
            {(p.status === 'rejected' || p.status === 'stale') && p.decided_at && (
              <Text size="xs" c="dimmed">
                {p.status === 'rejected' ? 'Rejected' : 'Marked stale'}
                {p.decided_by && ` by ${p.decided_by}`}, {formatDateTime(p.decided_at)}
              </Text>
            )}
          </Stack>
          {canDecide && p.status === 'pending' && (
            <Group gap="xs">
              <Button
                size="xs"
                color="green"
                leftSection={<IconCheck size="0.9rem" />}
                onClick={askApprove}
                loading={approve.isPending}
                disabled={busy && !approve.isPending}
              >
                Approve
              </Button>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconX size="0.9rem" />}
                onClick={() => reject.mutate()}
                loading={reject.isPending}
                disabled={busy && !reject.isPending}
              >
                Reject
              </Button>
            </Group>
          )}
          {canDecide && p.status === 'applied' && !!p.previous_version && (
            <Button
              size="xs"
              variant="light"
              color="orange"
              leftSection={<IconArrowBackUp size="0.9rem" />}
              onClick={() => askRollback(p.previous_version as number)}
              loading={rollback.isPending}
            >
              Roll back fix
            </Button>
          )}
        </Group>

        {patch.length > 0 && (
          <Table.ScrollContainer minWidth={380}>
            <Table aria-label={`Changes proposed by ${p.title}`} verticalSpacing={4} fz="sm" withTableBorder>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Setting</Table.Th>
                  <Table.Th>Before</Table.Th>
                  <Table.Th>After</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {patch.map((c) => (
                  <Table.Tr key={c.path}>
                    <Table.Td><Code>{c.path}</Code></Table.Td>
                    <Table.Td>
                      <Code c="red" style={{ textDecoration: 'line-through' }}>{formatPatchValue(c.before)}</Code>
                    </Table.Td>
                    <Table.Td>
                      <Code c="green">{formatPatchValue(c.after)}</Code>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}

        {p.status === 'applied' && p.effect && <EffectTable title={p.title} effect={p.effect} />}
        {p.status === 'applied' && !p.effect && p.effect_pending && (
          <Text size="sm" c="dimmed">
            Measuring what this fix changed. A before and after comparison appears here after a few minutes of traffic.
          </Text>
        )}

        {failure && (
          <Alert
            color={failure.color}
            icon={<IconAlertCircle size="1rem" />}
            title={failure.title}
            withCloseButton
            closeButtonLabel="Dismiss"
            onClose={() => setFailure(null)}
          >
            {failure.message}
          </Alert>
        )}
      </Stack>
    </Paper>
  )
}
