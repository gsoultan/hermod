import { ScrollArea, Table, Text } from '@mantine/core'

const cell = (v: unknown) => (v === null || v === undefined ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v))

/**
 * A few rows as a table, before they are a dataset: the first rows a query
 * returns, or the first lines of a file. `rows` holds each row as a list in
 * the columns' order or as a column -> value map.
 */
export function RowsPreview({ columns, rows, caption, maxHeight = 280 }: {
  columns: string[]
  rows: Array<unknown[] | Record<string, unknown>>
  caption: string
  maxHeight?: number
}) {
  if (columns.length === 0) return <Text size="sm" c="dimmed">No columns to show.</Text>
  return (
    <ScrollArea.Autosize mah={maxHeight} type="auto" offsetScrollbars>
      <Table striped highlightOnHover stickyHeader fz="xs" withTableBorder withColumnBorders aria-label={caption}>
        <Table.Thead>
          <Table.Tr>{columns.map((c) => <Table.Th key={c} style={{ whiteSpace: 'nowrap' }}>{c}</Table.Th>)}</Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row, i) => (
            <Table.Tr key={i}>
              {columns.map((c, j) => (
                <Table.Td key={c} style={{ whiteSpace: 'nowrap', maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {cell(Array.isArray(row) ? row[j] : row[c])}
                </Table.Td>
              ))}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </ScrollArea.Autosize>
  )
}
