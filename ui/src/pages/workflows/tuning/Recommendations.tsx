import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Alert, Badge, Box, Button, Group, Loader, Paper, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconBulb, IconCircleCheck, IconHourglass, IconTool } from '@tabler/icons-react'
import type { Insight, InsightSeverity, InsightsReport } from '@/lib/insights'
import { proposalsKey, proposeFix } from '@/lib/proposals'

interface RecommendationsProps {
  workflowId: string
  /** Editors and administrators may turn a recommendation into a fix to approve. */
  canApply: boolean
  report: InsightsReport | undefined
  isLoading: boolean
  error: Error | null
}

const count = new Intl.NumberFormat()

const severity: Record<InsightSeverity, { label: string; color: string }> = {
  high: { label: 'High impact', color: 'red' },
  medium: { label: 'Medium impact', color: 'orange' },
  low: { label: 'Low impact', color: 'gray' },
}
const rank: Record<InsightSeverity, number> = { high: 0, medium: 1, low: 2 }

/** How long the window covers, in the words a person reads. */
function windowLength(report: InsightsReport): string | null {
  if (!report.window_start || !report.window_end) return null
  const ms = Date.parse(report.window_end) - Date.parse(report.window_start)
  if (!Number.isFinite(ms) || ms <= 0) return null
  const min = Math.round(ms / 60_000)
  if (min < 1) return 'last minute'
  if (min < 120) return `last ${min} min`
  return `last ${Math.round(min / 60)} h`
}

/**
 * Turns a recommendation into a pending fix. It changes nothing by itself:
 * the fix waits under Suggested fixes until someone approves it.
 */
function ProposeFix({ workflowId, finding }: { workflowId: string; finding: Insight }) {
  const queryClient = useQueryClient()
  const [proposed, setProposed] = useState(false)
  const propose = useMutation({
    mutationFn: () => proposeFix(workflowId, finding),
    onSuccess: () => {
      setProposed(true)
      queryClient.invalidateQueries({ queryKey: proposalsKey(workflowId) })
    },
    // The failure is shown under the button; handling it here keeps the app's
    // fallback toast from saying it a second time.
    onError: () => {},
  })

  if (proposed) {
    return (
      <Group gap="xs" wrap="nowrap" c="green" component="output">
        <IconCircleCheck size={16} aria-hidden style={{ flexShrink: 0 }} />
        <Text size="sm">Waiting for approval under Suggested fixes, below. Nothing has changed yet.</Text>
      </Group>
    )
  }
  return (
    <Stack gap="xs" align="flex-start">
      <Button
        size="xs"
        variant="light"
        leftSection={<IconTool size="0.9rem" />}
        onClick={() => propose.mutate()}
        loading={propose.isPending}
      >
        Propose this fix
      </Button>
      {propose.error && (
        <Alert color="yellow" icon={<IconAlertCircle size="1rem" />} title="Could not propose this fix" w="100%">
          {(propose.error as Error).message}
        </Alert>
      )}
    </Stack>
  )
}

function FindingCard({ finding, workflowId, canApply }: { finding: Insight; workflowId: string; canApply: boolean }) {
  const sev = severity[finding.severity] ?? severity.low
  return (
    <Paper withBorder p={{ base: 'sm', sm: 'md' }} radius="md" data-finding={finding.rule}>
      <Stack gap="xs">
        <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
          <Title order={5} style={{ minWidth: 0, overflowWrap: 'anywhere' }}>{finding.title}</Title>
          <Badge variant="light" color={sev.color} size="sm" style={{ flexShrink: 0 }}>{sev.label}</Badge>
        </Group>
        <Text size="sm" c="dimmed">{finding.detail}</Text>
        <Group gap="xs" wrap="nowrap" align="flex-start">
          <IconBulb size={16} aria-hidden style={{ flexShrink: 0, marginTop: 2 }} />
          <Text size="sm">{finding.recommendation}</Text>
        </Group>
        {finding.evidence && finding.evidence.length > 0 && (
          <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="xs" verticalSpacing={4}>
            {finding.evidence.map((e) => (
              <Box key={e.label}>
                <Text size="xs" c="dimmed">{e.label}</Text>
                <Text size="sm" fw={600}>{e.value}</Text>
              </Box>
            ))}
          </SimpleGrid>
        )}
        {canApply && finding.fix && <ProposeFix workflowId={workflowId} finding={finding} />}
      </Stack>
    </Paper>
  )
}

/**
 * What the performance advisor recommends for this flow, most urgent first.
 * The server draws every finding from the flow's recent readings and says
 * which numbers it used. Nothing here changes the flow: a fix it can make is
 * proposed for approval, never applied.
 */
export function Recommendations({ workflowId, canApply, report, isLoading, error }: RecommendationsProps) {
  if (error) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="Could not load recommendations">
        {error.message}
      </Alert>
    )
  }
  if (isLoading || !report) {
    return <Group justify="center" p="md"><Loader size="sm" aria-label="Loading recommendations" /></Group>
  }
  if (report.state === 'not_running') {
    return <Text c="dimmed" size="sm">This flow is not running here. Turn the flow on and Hermod will start reading it.</Text>
  }

  const findings = [...(report.findings ?? [])].sort((a, b) => (rank[a.severity] ?? 2) - (rank[b.severity] ?? 2))
  const span = windowLength(report)

  return (
    <Stack gap="sm">
      {span && (
        <Text size="xs" c="dimmed">
          Based on the {span}: {count.format(report.processed)} messages, {report.throughput.toFixed(1)} msg/s on average.
        </Text>
      )}
      {report.state === 'collecting' && (
        <Group gap="xs" wrap="nowrap" c="dimmed">
          <IconHourglass size={16} aria-hidden style={{ flexShrink: 0 }} />
          <Text size="sm">Collecting readings. Recommendations that compare readings over time appear after a few minutes of traffic.</Text>
        </Group>
      )}
      {report.state === 'ready' && findings.length === 0 && (
        <Group gap="xs" wrap="nowrap" c="green">
          <IconCircleCheck size={16} aria-hidden style={{ flexShrink: 0 }} />
          <Text size="sm">Nothing to improve right now. Every step and sink is keeping up.</Text>
        </Group>
      )}
      {findings.map((f) => (
        <FindingCard key={`${f.rule}:${f.node_id ?? ''}:${f.sink_id ?? ''}`} finding={f} workflowId={workflowId} canApply={canApply} />
      ))}
    </Stack>
  )
}
