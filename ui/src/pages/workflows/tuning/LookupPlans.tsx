import { useMutation } from '@tanstack/react-query'
import { Alert, Box, Button, Code, Paper, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconDatabase } from '@tabler/icons-react'
import { checkLookupPlans, type LookupPlanNode } from '@/lib/insights'
import { PlanFinding, planCodeStyle as codeStyle, sortFindings } from '@/components/queryplan/PlanFinding'

interface LookupPlansProps {
  workflowId: string
  /** The check connects to the flow's source databases, so it takes an editor. */
  canCheck: boolean
}

function NodeCard({ node }: { node: LookupPlanNode }) {
  const findings = sortFindings(node.findings)
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
          findings.map((f, i) => <PlanFinding key={`${f.rule}:${f.table ?? ''}:${i}`} finding={f} />)
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
