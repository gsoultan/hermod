import { ActionIcon, Divider, Group, Paper, ScrollArea, Table, Text, Tooltip } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconCopy } from '@tabler/icons-react';

// renderCellValue converts an arbitrary result cell into a display-safe string,
// guarding against null/undefined (which would otherwise render the literal
// "null"/"undefined") and serialising nested objects/arrays.
function renderCellValue(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

interface ResultsTableProps {
  rows: any[];
  title: string;
  /** What an empty result means for this kind of statement. */
  emptyMessage: string;
  onSelectRow?: (row: any) => void;
  height: number;
}

export function ResultsTable({ rows, title, emptyMessage, onSelectRow, height }: ResultsTableProps) {
  const columns = rows.length > 0 ? Object.keys(rows[0]) : [];

  return (
    <Paper withBorder radius="md" style={{ overflow: 'hidden' }} data-testid="sql-results">
      <Group p="xs" bg="var(--mantine-color-default-hover)" justify="space-between">
        <Group gap="sm">
          <Text size="xs" fw={600} c="blue">{rows.length} rows</Text>
          <Divider orientation="vertical" />
          <Text size="xs" c="dimmed">{title}</Text>
          {onSelectRow && rows.length > 0 && (
            <Text size="xs" c="dimmed">· Click a row to select</Text>
          )}
        </Group>
        {rows.length > 0 && (
          <Tooltip label="Copy results as JSON">
            <ActionIcon
              aria-label="Copy results as JSON"
              variant="light"
              size="sm"
              onClick={() => {
                navigator.clipboard.writeText(JSON.stringify(rows, null, 2));
                notifications.show({
                  id: 'sql-results-copied',
                  message: 'All results copied to clipboard',
                  color: 'teal',
                });
              }}
            >
              <IconCopy size={14} />
            </ActionIcon>
          </Tooltip>
        )}
      </Group>
      {rows.length === 0 ? (
        <Text c="dimmed" ta="center" py="lg" size="sm">{emptyMessage}</Text>
      ) : (
        <ScrollArea.Autosize mah={height} scrollbars="xy">
          <Table striped highlightOnHover withColumnBorders verticalSpacing="xs" horizontalSpacing="sm" stickyHeader>
            <Table.Thead>
              <Table.Tr>
                {columns.map((col) => (
                  <Table.Th key={col} style={{ whiteSpace: 'nowrap' }}>{col}</Table.Th>
                ))}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((row, i) => (
                <Table.Tr
                  key={i}
                  onClick={() => onSelectRow?.(row)}
                  style={{ cursor: onSelectRow ? 'pointer' : 'default' }}
                >
                  {columns.map((col) => (
                    <Table.Td
                      key={col}
                      style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                    >
                      {renderCellValue(row[col])}
                    </Table.Td>
                  ))}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea.Autosize>
      )}
    </Paper>
  );
}
