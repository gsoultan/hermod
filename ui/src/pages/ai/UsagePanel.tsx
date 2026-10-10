import { Alert, Badge, Group, Paper, Progress, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import {
  costOf, level, share, usedTokens, WARN_RATIO, type AIBudget, type AIBudgetReport, type AIUsage,
} from '@/lib/aiBudget'

const COLOR = { none: 'gray', ok: 'teal', warn: 'yellow', over: 'red' } as const

const tokens = (n: number) => n.toLocaleString('en-US')
const money = (n: number, currency?: string) => `${n.toFixed(2)}${currency ? ` ${currency}` : ''}`

/** One limit: what was used of it, as words, a percentage and a bar. */
function Meter({ label, used, limit, format, unit }: { label: string; used: number; limit?: number; format: (n: number) => string; unit?: string }) {
  const ratio = share(used, limit)
  const state = level(ratio)
  return (
    <Stack gap={4}>
      <Group justify="space-between" gap="xs">
        <Text size="sm" fw={500}>{label}</Text>
        {ratio === null
          ? <Badge variant="light" color="gray">No limit</Badge>
          : <Badge variant="light" color={COLOR[state]}>{`${Math.floor(ratio * 100)}%`}</Badge>}
      </Group>
      <Text size="sm" c="dimmed">{ratio === null ? `${format(used)}${unit ? ` ${unit}` : ''} used` : `${format(used)} of ${format(limit as number)}${unit ? ` ${unit}` : ''}`}</Text>
      {ratio !== null && (
        <Progress value={Math.min(ratio, 1) * 100} color={COLOR[state]} aria-label={`${label}: ${Math.floor(ratio * 100)}% used`} />
      )}
    </Stack>
  )
}

/** The scopes past 80% of a limit, in words, for the warning. */
function warnings(budget: AIBudget, report: AIBudgetReport): string[] {
  const out: string[] = []
  const check = (who: string, u: AIUsage | undefined, tokenLimit?: number, costLimit?: number) => {
    const t = share(usedTokens(u), tokenLimit)
    const c = share(costOf(u?.cost_micros ?? 0), costLimit)
    if (t !== null && t >= WARN_RATIO) out.push(`${who}: ${Math.floor(t * 100)}% of its token budget`)
    if (c !== null && c >= WARN_RATIO) out.push(`${who}: ${Math.floor(c * 100)}% of its cost budget`)
  }
  check(`Vhost ${report.vhost}`, report.usage, budget.monthly_tokens, budget.monthly_cost)
  for (const cap of budget.workflows ?? []) {
    check(`Workflow ${cap.workflow_id}`, report.workflows.find((w) => w.workflow_id === cap.workflow_id), cap.monthly_tokens, cap.monthly_cost)
  }
  return out
}

/**
 * This month's AI usage against the vhost's limits and each workflow's, with
 * a warning once any of them passes 80%.
 */
export function UsagePanel({ report }: { report: AIBudgetReport }) {
  const { budget } = report
  const over = warnings(budget, report)
  const capOf = (id?: string) => (budget.workflows ?? []).find((c) => c.workflow_id === id)
  const capped = new Set((budget.workflows ?? []).map((c) => c.workflow_id))
  const rows: AIUsage[] = [
    ...report.workflows,
    // A capped workflow that has spent nothing yet still has a row.
    ...[...capped].filter((id) => !report.workflows.some((w) => w.workflow_id === id))
      .map((id) => ({ vhost: report.vhost, period: report.period, workflow_id: id, calls: 0, input_tokens: 0, output_tokens: 0, cost_micros: 0 })),
  ]

  return (
    <Paper p="md" withBorder radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>Usage in {report.period}</Title>
          <Text size="sm" c="dimmed">{tokens(report.usage.calls)} calls · months are UTC calendar months</Text>
        </Group>
        {over.length > 0 && (
          <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Over 80% of a monthly limit">
            {over.join('; ')}. At 100% their AI calls are refused and the messages take the node's error branch.
          </Alert>
        )}
        <Group grow align="flex-start">
          <Meter label="Tokens" used={usedTokens(report.usage)} limit={budget.monthly_tokens} format={tokens} unit="tokens" />
          <Meter label="Cost" used={costOf(report.usage.cost_micros)} limit={budget.monthly_cost} format={(n) => money(n)} unit={budget.currency} />
        </Group>
        {rows.length > 0 && (
          <Table.ScrollContainer minWidth={560}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Workflow</Table.Th>
                  <Table.Th>Calls</Table.Th>
                  <Table.Th>Tokens</Table.Th>
                  <Table.Th>Cost</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((w) => {
                  const cap = capOf(w.workflow_id)
                  const t = level(share(usedTokens(w), cap?.monthly_tokens))
                  const c = level(share(costOf(w.cost_micros), cap?.monthly_cost))
                  return (
                    <Table.Tr key={w.workflow_id}>
                      <Table.Td><Text size="sm" ff="var(--mantine-font-family-monospace)">{w.workflow_id}</Text></Table.Td>
                      <Table.Td>{tokens(w.calls)}</Table.Td>
                      <Table.Td>
                        <Text size="sm" c={t === 'over' ? 'red' : t === 'warn' ? 'yellow.8' : undefined}>
                          {tokens(usedTokens(w))}{cap?.monthly_tokens ? ` / ${tokens(cap.monthly_tokens)}` : ''}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c={c === 'over' ? 'red' : c === 'warn' ? 'yellow.8' : undefined}>
                          {money(costOf(w.cost_micros))}{cap?.monthly_cost ? ` / ${money(cap.monthly_cost, budget.currency)}` : ''}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
                  )
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Paper>
  )
}
