import { ActionIcon, Badge, Code, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core'
import { IconCheck, IconCopy } from '@tabler/icons-react'
import type { LookupPlanFinding, LookupPlanSeverity } from '@/lib/insights'

const severity: Record<LookupPlanSeverity, { label: string; color: string }> = {
  warn: { label: 'Can improve', color: 'orange' },
  info: { label: 'Note', color: 'gray' },
  ok: { label: 'Good', color: 'green' },
}
const rank: Record<LookupPlanSeverity, number> = { warn: 0, info: 1, ok: 2 }

export const planCodeStyle = { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } as const

/** The findings that can improve first, then notes, then what is good. */
export function sortFindings(findings: LookupPlanFinding[] | null | undefined): LookupPlanFinding[] {
  return [...(findings ?? [])].sort((a, b) => (rank[a.severity] ?? 1) - (rank[b.severity] ?? 1))
}

function CopyableSql({ label, sql, copyLabel }: { label: string; sql: string; copyLabel: string }) {
  return (
    <Stack gap={4}>
      <Group justify="space-between" align="center" wrap="nowrap">
        <Text size="xs" c="dimmed">{label}</Text>
        <CopyButton value={sql}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied' : 'Copy'}>
              <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} size="sm" onClick={copy} aria-label={copyLabel}>
                {copied ? <IconCheck size="0.9rem" /> : <IconCopy size="0.9rem" />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
      <Code block style={planCodeStyle}>{sql}</Code>
    </Stack>
  )
}

/**
 * One finding of a query plan check: what the plan shows, and what to
 * change. A rewrite is a condition meaning the same as the query's that an
 * existing index can serve; a proposal is an index to create.
 */
export function PlanFinding({ finding }: { finding: LookupPlanFinding }) {
  const sev = severity[finding.severity] ?? severity.info
  return (
    <Stack gap={6} data-plan-finding={finding.rule}>
      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
        <Text size="sm" fw={600} style={{ minWidth: 0, overflowWrap: 'anywhere' }}>{finding.title}</Text>
        <Badge variant="light" color={sev.color} size="sm" style={{ flexShrink: 0 }}>{sev.label}</Badge>
      </Group>
      <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{finding.detail}</Text>
      {finding.rewrite && <CopyableSql label="Same rows, uses the index" sql={finding.rewrite} copyLabel="Copy the rewritten condition" />}
      {finding.proposal && <CopyableSql label="Proposed index" sql={finding.proposal} copyLabel="Copy the SQL for this index" />}
    </Stack>
  )
}
