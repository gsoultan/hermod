import { Alert, Group, Loader, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconFirstAidKit } from '@tabler/icons-react'
import { useProposals } from '@/lib/proposals'
import { ProposalCard } from './ProposalCard'

interface ProposalsPanelProps {
  workflowId: string
  /** Editors and administrators may approve, reject and roll back. */
  canDecide: boolean
}

/**
 * The fixes the self-correction gate proposed for this workflow: the ones
 * waiting for a decision first, then the decided ones.
 */
export function ProposalsPanel({ workflowId, canDecide }: ProposalsPanelProps) {
  const { data, isLoading, error } = useProposals(workflowId)

  if (isLoading) return <Group justify="center" p="xl"><Loader size="sm" aria-label="Loading proposals" /></Group>
  if (error) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="Could not load proposals">
        {(error as Error).message}
      </Alert>
    )
  }

  const proposals = data ?? []
  const pending = proposals.filter((p) => p.status === 'pending')
  const decided = proposals.filter((p) => p.status !== 'pending')

  if (proposals.length === 0) {
    return (
      <Stack align="center" py={60} gap="xs">
        <IconFirstAidKit size="2.5rem" color="var(--mantine-color-dimmed)" aria-hidden />
        <Text c="dimmed" size="sm" ta="center" maw={460}>
          No self-healing proposals. When a node keeps failing in a way a settings change would fix, the fix is
          proposed here for review; nothing changes until someone approves it.
        </Text>
      </Stack>
    )
  }

  return (
    <Stack gap="lg">
      <Stack gap="sm">
        <Title order={4}>Waiting for review ({pending.length})</Title>
        {pending.length === 0 ? (
          <Text size="sm" c="dimmed">Nothing is waiting for a decision.</Text>
        ) : (
          pending.map((p) => <ProposalCard key={p.id} workflowId={workflowId} proposal={p} canDecide={canDecide} />)
        )}
      </Stack>
      {decided.length > 0 && (
        <Stack gap="sm">
          <Title order={5}>Decided</Title>
          {decided.map((p) => <ProposalCard key={p.id} workflowId={workflowId} proposal={p} canDecide={canDecide} />)}
        </Stack>
      )}
    </Stack>
  )
}
