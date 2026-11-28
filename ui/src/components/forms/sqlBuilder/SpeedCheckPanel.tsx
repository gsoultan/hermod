import { Alert, Button, Code, CopyButton, Group, Paper, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy, IconGauge } from '@tabler/icons-react'
import { PlanFinding, planCodeStyle, sortFindings } from '@/components/queryplan/PlanFinding'
import { workSaved, type SpeedCheck } from '@/lib/speedCheck'

/** The engines whose plans the server reads. */
const PLANNED = new Set(['postgres', 'yugabyte', 'pgx'])

interface SpeedCheckPanelProps {
  /** The connector's type; the check reads PostgreSQL plans only. */
  engine?: string
  result: SpeedCheck | null
  loading: boolean
  error: string | null
  /** The statement changed since the result was checked. */
  stale: boolean
  hasStatement: boolean
  onCheck: () => void
  onApply: (sql: string) => void
}

/**
 * The SQL builder's speed check: the statement planned on its database,
 * without running it, and read against the indexes of the tables it reads.
 * When the plan shows a condition that defeats an index and a rewrite means
 * the same rows, the whole statement comes back rewritten, to apply.
 */
export function SpeedCheckPanel({ engine, result, loading, error, stale, hasStatement, onCheck, onApply }: SpeedCheckPanelProps) {
  if (engine && !PLANNED.has(engine)) {
    return (
      <Text size="sm" c="dimmed">
        Speed check reads PostgreSQL query plans, and this database is {engine}.
      </Text>
    )
  }
  const findings = sortFindings(result?.findings)
  const saved = result ? workSaved(result) : null
  return (
    <Stack gap="sm">
      <Text size="xs" c="dimmed">
        Hermod asks the database how it would run this statement, without running it, and reads that against the tables&apos; indexes.
        Nothing is executed or changed.
      </Text>
      <Group gap="xs">
        <Button size="xs" variant="default" leftSection={<IconGauge size="0.9rem" />} onClick={onCheck} loading={loading} disabled={!hasStatement}>
          {result ? 'Check again' : 'Check speed'}
        </Button>
        {result && stale && <Text size="xs" c="dimmed">The statement changed since this check.</Text>}
      </Group>

      {error && (
        <Alert color="yellow" icon={<IconAlertCircle size="1rem" />} title="Could not check the statement">
          <Text size="xs" style={{ overflowWrap: 'anywhere' }}>{error}</Text>
        </Alert>
      )}

      {result?.rewritten && (
        <Paper withBorder radius="md" p="sm" data-speed-rewrite>
          <Stack gap="xs">
            <Text size="sm" fw={600}>A faster way to write it</Text>
            <Text size="xs" c="dimmed">
              {saved
                ? `The database estimates about ${saved.toLocaleString()} times less work for this version, for the same rows.`
                : 'The same rows, read through the index.'}
            </Text>
            <Group gap="xs">
              <Button size="xs" leftSection={<IconCheck size="0.9rem" />} onClick={() => onApply(result.rewritten!)}>
                Apply to the query
              </Button>
              <CopyButton value={result.rewritten}>
                {({ copied, copy }) => (
                  <Button size="xs" variant="default" leftSection={copied ? <IconCheck size="0.9rem" /> : <IconCopy size="0.9rem" />} onClick={copy}>
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                )}
              </CopyButton>
            </Group>
            <Code block style={planCodeStyle}>{result.rewritten}</Code>

          </Stack>
        </Paper>
      )}

      {result && findings.length === 0 && (
        <Text size="sm" c="dimmed">The plan shows nothing to improve.</Text>
      )}
      {findings.map((f, i) => <PlanFinding key={`${f.rule}:${f.table ?? ''}:${i}`} finding={f} />)}

      {result && (
        <Text size="xs" c="dimmed">
          Planned without the message&apos;s values, as the engine&apos;s reused statement is. Estimates come from the
          database&apos;s statistics, so they are not timings.
        </Text>
      )}
    </Stack>
  )
}
