import { Table } from '@mantine/core'

/** Shows a metric the way people read it: four significant digits. */
export const formatMetric = (v: number) => (Number.isInteger(v) ? String(v) : v.toPrecision(4))

export function MetricsTable({ metrics }: { metrics: Record<string, number> }) {
  const entries = Object.entries(metrics).filter(([k]) => k !== 'score')
  return (
    <Table withRowBorders={false} verticalSpacing={2} fz="sm">
      <Table.Tbody>
        {entries.map(([k, val]) => (
          <Table.Tr key={k}>
            <Table.Td c="dimmed" w={140}>{k}</Table.Td>
            <Table.Td ff="var(--mantine-font-family-monospace)">{formatMetric(val)}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}
