import { useMutation } from '@tanstack/react-query'
import { ActionIcon, Alert, Badge, Box, Button, Code, CopyButton, Group, Paper, Stack, Text, Title, Tooltip } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy, IconDatabase } from '@tabler/icons-react'
import { checkLookupPlans, type LookupPlanFinding, type LookupPlanNode, type LookupPlanSeverity } from '@/lib/insights'

interface LookupPlansProps {
  workflowId: string
  /** The check connects to the flow's source databases, so it takes an editor. */
  canCheck: boolean
}

const severity: Record<LookupPlanSeverity, { label: string; color: string }> = {
  warn: { label: 'Can improve', color: 'orange' },
  info: { label: 'Note', color: 'gray' },
  ok: { label: 'Good', color: 'green' },
}
const rank: Record<LookupPlanSeverity, number> = { warn: 0, info: 1, ok: 2 }

const codeStyle = { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } as const

function Proposal({ sql }: { sql: string }) {
  return (
    <Stack gap={4}>
      <Group justify="space-between" align="center" wrap="nowrap">
        <Text size="xs" c="dimmed">Proposed index</Text>
        <CopyButton value={sql}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied' : 'Copy'}>
              <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} size="sm" onClick={copy} aria-label="Copy the SQL for this index">
                {copied ? <IconCheck size="0.9rem" /> : <IconCopy size="0.9rem" />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
      <Code block style={codeStyle}>{sql}</Code>
    </Stack>
  )
}

function FindingRow({ finding }: { finding: LookupPlanFinding }) {
  const sev = severity[finding.severity] ?? severity.info
  return (
    <Stack gap={6} data-plan-finding={finding.rule}>
      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
        <Text size="sm" fw={600} style={{ minWidth: 0, overflowWrap: 'anywhere' }}>{finding.title}</Text>
        <Badge variant="light" color={sev.color} size="sm" style={{ flexShrink: 0 }}>{sev.label}</Badge>
      </Group>
      <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{finding.detail}</Text>
      {finding.proposal && <Proposal sql={finding.proposal} />}
    </Stack>
  )
}

function NodeCard({ node }: { node: LookupPlanNode }) {
  const findings = [...(node.findings ?? [])].sort((a, b) => (rank[a.severity] ?? 1) - (rank[b.severity] ?? 1))
  return (
    <Paper withBorder p={{ base: 'sm', sm: 'md' }} radius="md" data-lookup-node={node.node_id}>
      <Stack gap="xs">
        <Box>
          <Title order={5} style={{ overflowWrap: 'anywhere' }}>{node.label || node.node_id}</Title>
          {node.source && <Text size="xs" c="dimmed">Queries {node.source}</Text>}
        </Box>
        {node.sql && <Code block style={codeStyle}>{node.sql}</Code>}
        {node.error ? (
          <Alert color="yellow" icon={<IconAlertCircle size="1rem" />} title="Could not check this lookup">
            {node.error}
          </Alert>
        ) : (
          findings.map((f, i) => <FindingRow key={`${f.rule}:${f.table ?? ''}:${i}`} finding={f} />)
        )}
      </Stack>
    </Paper>
  )
}

/**
 * Checks each DB Lookup step's query against the indexes of the tables it
 * reads: which index it uses and of what type, and which index would serve it
 * better. The server plans the query without running it, in a read-only
 * transaction; the SQL it proposes is for a person to run.
 */
export function LookupPlans({ workflowId, canCheck }: LookupPlansProps) {
  const check = useMutation({
    mutationFn: () => checkLookupPlans(workflowId),
    // Shown under the button; handling it here keeps the app's fallback toast
    // from saying it a second time.
    onError: () => {},
  })

  if (!canCheck) {
    return <Text c="dimmed" size="sm">An editor can check the lookup queries. The check connects to the flow&apos;s source databases.</Text>
  }

  const nodes = check.data?.nodes ?? []
  return (
    <Stack gap="sm">
      <Stack gap="xs" align="flex-start">
        <Button
          size="xs"
          variant="default"
          leftSection={<IconDatabase size="0.9rem" />}
          onClick={() => check.mutate()}
          loading={check.isPending}
        >
          {check.data ? 'Check again' : 'Check lookup queries'}
        </Button>
        {check.error && (
          <Alert color="yellow" icon={<IconAlertCircle size="1rem" />} title="Could not check the lookup queries" w="100%">
            {(check.error as Error).message}
          </Alert>
        )}
      </Stack>
      {check.data && nodes.length === 0 && (
        <Text c="dimmed" size="sm">This flow has no DB Lookup steps.</Text>
      )}
      {nodes.map((n) => <NodeCard key={n.node_id} node={n} />)}
      {nodes.some((n) => n.findings?.some((f) => f.proposal)) && (
        <Text size="xs" c="dimmed">
          CREATE INDEX CONCURRENTLY builds the index without blocking writes, but it reads the whole table. Run it in a quiet hour, and
          not inside a transaction.
        </Text>
      )}
    </Stack>
  )
}
