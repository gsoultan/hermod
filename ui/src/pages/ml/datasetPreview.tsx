import { ScrollArea, Table } from '@mantine/core'
import type { DatasetInfo } from '@/lib/mlModels'

/** A dataset's first rows, as the server sampled them, one column per field. */
export function DatasetPreview({ info, limit, caption }: { info: DatasetInfo; limit?: number; caption?: string }) {
  const cols = info.columns
  const rows = (info.sample ?? []).slice(0, limit)
  return (
    <ScrollArea>
      <Table striped fz="xs" withTableBorder aria-label={caption}>
        <Table.Thead><Table.Tr>{cols.map((c) => <Table.Th key={c.name}>{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
        <Table.Tbody>
          {rows.map((row, i) => (
            <Table.Tr key={i}>{cols.map((c) => <Table.Td key={c.name}>{String(row[c.name] ?? '')}</Table.Td>)}</Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </ScrollArea>
  )
}
