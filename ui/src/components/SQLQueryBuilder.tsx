import { useState } from 'react';
import { 
  Stack, TextInput, Button, Table, Text, 
  Paper, Group, ScrollArea, Alert, ActionIcon, Tooltip
} from '@mantine/core';import { notifications } from '@mantine/notifications';import { IconAlertCircle, IconCopy, IconDatabase, IconPlayerPlay } from '@tabler/icons-react';
interface SQLQueryBuilderProps {
  type: 'source' | 'sink';
  config: any;
  onSelectResult?: (row: any) => void;
  initialQuery?: string;
  onQueryChange?: (query: string) => void;
}

export function SQLQueryBuilder({ type, config, onSelectResult, initialQuery, onQueryChange }: SQLQueryBuilderProps) {
  const [query, setQuery] = useState(initialQuery || 'SELECT * FROM tables LIMIT 10');
  const [loading, setLoading] = useState(false);
  const [results, setResults] = useState<any[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const handleQueryChange = (val: string) => {
    setQuery(val);
    onQueryChange?.(val);
  };

  const executeQuery = async () => {
    if (!query.trim()) return;
    
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`/api/${type}s/query`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config, query }),
      });

      if (!response.ok) {
        const err = await response.json();
        throw new Error(err.error || 'Failed to execute query');
      }

      const data = await response.json();
      setResults(data);
    } catch (err: any) {
      setError(err.message);
      notifications.show({
        title: 'Query Error',
        message: err.message,
        color: 'red',
      });
    } finally {
      setLoading(false);
    }
  };

  const columns = results && results.length > 0 ? Object.keys(results[0]) : [];

  return (
    <Stack gap="md">
      <Paper withBorder p="md" bg="gray.0">
        <Stack gap="xs">
          <Group justify="space-between">
            <Group gap="xs">
              <IconDatabase size={20} color="#228be6" />
              <Text fw={600} size="sm">SQL Query Builder & Executor</Text>
            </Group>
            <Group gap="xs">
               {onSelectResult && results && results.length > 0 && (
                 <Text size="xs" c="dimmed">Click a row to select it</Text>
               )}
              <Button 
                leftSection={<IconPlayerPlay size={14} />} 
                onClick={executeQuery} 
                loading={loading}
                variant="filled"
                size="xs"
                color="blue"
              >
                Execute
              </Button>
            </Group>
          </Group>
          
          <TextInput
            placeholder="SELECT * FROM my_table LIMIT 10"
            value={query}
            onChange={(e) => handleQueryChange(e.currentTarget.value)}
            styles={{ input: { fontFamily: 'monospace', fontSize: '13px' } }}
          />
          <Text size="xs" c="dimmed">
            Tip: You can explore your database by running queries here before saving the configuration.
          </Text>
        </Stack>
      </Paper>

      {error && (
        <Alert icon={<IconAlertCircle size={16} />} title="Query failed" color="red" variant="light">
          <Text size="xs">{error}</Text>
        </Alert>
      )}

      {results && (
        <Paper withBorder p={0} radius="md" style={{ overflow: 'hidden' }}>
          <Group p="xs" bg="gray.1" justify="space-between">
            <Text size="xs" fw={500}>{results.length} rows returned</Text>
            {results.length > 0 && (
               <Tooltip label="Copy first row as JSON">
                 <ActionIcon variant="subtle" size="sm" onClick={() => {
                   navigator.clipboard.writeText(JSON.stringify(results[0], null, 2));
                   notifications.show({ message: 'Copied to clipboard', color: 'teal' });
                 }}>
                   <IconCopy size={14} />
                 </ActionIcon>
               </Tooltip>
            )}
          </Group>
          <ScrollArea h={results.length > 0 ? 300 : 'auto'} scrollbars="xy">
            <Table striped highlightOnHover withColumnBorders withTableBorder={false}>
              <Table.Thead>
                <Table.Tr>
                  {columns.map((col) => (
                    <Table.Th key={col} style={{ whiteSpace: 'nowrap' }}>{col}</Table.Th>
                  ))}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {results.map((row, i) => (
                  <Table.Tr 
                    key={i} 
                    onClick={() => onSelectResult?.(row)} 
                    style={{ cursor: onSelectResult ? 'pointer' : 'default' }}
                  >
                    {columns.map((col) => (
                      <Table.Td key={col} style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {typeof row[col] === 'object' ? JSON.stringify(row[col]) : String(row[col])}
                      </Table.Td>
                    ))}
                  </Table.Tr>
                ))}
                {results.length === 0 && (
                  <Table.Tr>
                    <Table.Td colSpan={columns.length || 1}>
                      <Text c="dimmed" ta="center" py="xl" size="sm">Query returned no rows</Text>
                    </Table.Td>
                  </Table.Tr>
                )}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Paper>
      )}
    </Stack>
  );
}


