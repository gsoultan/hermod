import { useState, useEffect } from 'react';
import { 
  Stack, Button, Table, Text, 
  Paper, Group, ScrollArea, Alert, ActionIcon, Tooltip,
  List, Divider, Loader, Textarea, Grid, Box
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconAlertCircle, IconCopy, IconDatabase, IconPlayerPlay, IconTable, IconColumns, IconRefresh, IconPlus } from '@tabler/icons-react';

interface SQLQueryBuilderProps {
  type: 'source' | 'sink';
  sourceType?: string;
  config: any;
  onSelectResult?: (row: any) => void;
  initialQuery?: string;
  onQueryChange?: (query: string) => void;
}

export function SQLQueryBuilder({ type, sourceType, config, onSelectResult, initialQuery, onQueryChange }: SQLQueryBuilderProps) {
  const [query, setQuery] = useState(initialQuery || 'SELECT * FROM tables LIMIT 10');
  const [loading, setLoading] = useState(false);
  const [results, setResults] = useState<any[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tables, setTables] = useState<string[]>([]);
  const [fetchingTables, setFetchingTables] = useState(false);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [columns, setColumns] = useState<any[]>([]);
  const [fetchingColumns, setFetchingColumns] = useState(false);

  useEffect(() => {
    if (initialQuery && initialQuery !== query) {
      setQuery(initialQuery);
    }
  }, [initialQuery]);

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
        body: JSON.stringify({ 
          config: {
            type: sourceType || config.type || '',
            config: config
          }, 
          query 
        }),
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

  const fetchTables = async () => {
    setFetchingTables(true);
    try {
      const response = await fetch(`/api/${type}s/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          type: sourceType || config.type || '', 
          config: config 
        }),
      });
      if (response.ok) {
        const data = await response.json();
        setTables(data || []);
      }
    } catch (e) {
      console.error('Failed to fetch tables', e);
    } finally {
      setFetchingTables(false);
    }
  };

  const fetchColumns = async (tableName: string) => {
    setFetchingColumns(true);
    setSelectedTable(tableName);
    try {
      const response = await fetch(`/api/${type}s/discover/columns`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          [type]: {
            type: sourceType || config.type || '',
            config: config
          },
          table: tableName 
        }),
      });
      if (response.ok) {
        const data = await response.json();
        setColumns(data || []);
      }
    } catch (e) {
      console.error('Failed to fetch columns', e);
    } finally {
      setFetchingColumns(false);
    }
  };

  const insertText = (text: string) => {
    const newQuery = query + (query.endsWith(' ') || query === '' ? '' : ' ') + text;
    handleQueryChange(newQuery);
  };

  const resultColumns = results && results.length > 0 ? Object.keys(results[0]) : [];

  return (
    <Stack gap="md">
      <Grid gap="md">
        <Grid.Col span={{ base: 12, md: 8 }}>
          <Stack gap="xs">
            <Paper withBorder p="md" shadow="sm" radius="md">
              <Stack gap="sm">
                <Group justify="space-between">
                  <Group gap="xs">
                    <IconDatabase size={20} color="var(--mantine-color-blue-filled)" />
                    <Text fw={600} size="sm">Query Editor</Text>
                  </Group>
                  <Group gap="xs">
                    {onSelectResult && results && results.length > 0 && (
                      <Text size="xs" c="dimmed">Click a row to select</Text>
                    )}
                    <Button 
                      leftSection={<IconPlayerPlay size={14} />} 
                      onClick={executeQuery} 
                      loading={loading}
                      variant="filled"
                      size="xs"
                    >
                      Run Query
                    </Button>
                  </Group>
                </Group>
                
                <Textarea
                  placeholder="SELECT * FROM my_table LIMIT 10"
                  value={query}
                  onChange={(e) => handleQueryChange(e.currentTarget.value)}
                  minRows={6}
                  maxRows={12}
                  autosize
                  styles={{ 
                    input: { 
                      fontFamily: 'JetBrains Mono, Menlo, Monaco, Courier New, monospace', 
                      fontSize: '13px',
                      backgroundColor: 'var(--mantine-color-gray-0)'
                    } 
                  }}
                />
                
                <Group gap="xs">
                  <Text size="xs" fw={500} c="dimmed">Quick Insert:</Text>
                  <Button size="compact-xs" variant="light" onClick={() => insertText('SELECT * FROM')}>SELECT</Button>
                  <Button size="compact-xs" variant="light" onClick={() => insertText('WHERE')}>WHERE</Button>
                  <Button size="compact-xs" variant="light" onClick={() => insertText('ORDER BY')}>ORDER BY</Button>
                  <Button size="compact-xs" variant="light" onClick={() => insertText('LIMIT 10')}>LIMIT</Button>
                  <Tooltip label="Insert dynamic last value variable">
                    <Button size="compact-xs" variant="light" color="orange" onClick={() => insertText('{{.last_value}}')}>
                      {"{{.last_value}}"}
                    </Button>
                  </Tooltip>
                  <Button size="compact-xs" variant="subtle" color="gray" onClick={() => handleQueryChange('')}>Clear</Button>
                </Group>
              </Stack>
            </Paper>
          </Stack>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Paper withBorder p="md" shadow="sm" radius="md" h="100%">
            <Stack gap="xs" h="100%">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconTable size={18} color="var(--mantine-color-blue-filled)" />
                  <Text size="xs" fw={700} c="dimmed">DATABASE EXPLORER</Text>
                </Group>
                <ActionIcon variant="subtle" size="sm" onClick={fetchTables} loading={fetchingTables}>
                  <IconRefresh size={14} />
                </ActionIcon>
              </Group>
              <Divider />
              
              <Box style={{ flex: 1, minHeight: 0 }}>
                <ScrollArea h={300}>
                  {tables.length === 0 && !fetchingTables && (
                    <Box py="xl" ta="center">
                      <Button size="xs" variant="light" onClick={fetchTables}>Load Tables</Button>
                    </Box>
                  )}
                  <List size="xs" spacing={4} icon={<IconTable size={12} />}>
                    {tables.map(t => (
                      <List.Item 
                        key={t}
                        styles={{ itemWrapper: { width: '100%' } }}
                      >
                        <Group gap={4} wrap="nowrap" justify="space-between" w="100%">
                          <Text 
                            span 
                            style={{ cursor: 'pointer', flex: 1 }} 
                            onClick={() => fetchColumns(t)}
                            fw={selectedTable === t ? 700 : 400}
                            c={selectedTable === t ? 'blue' : 'inherit'}
                          >
                            {t}
                          </Text>
                          <Tooltip label="Insert table name">
                            <ActionIcon size="xs" variant="subtle" onClick={() => insertText(t)}>
                              <IconPlus size={10} />
                            </ActionIcon>
                          </Tooltip>
                        </Group>
                        
                        {selectedTable === t && (
                          <Box pl="md" mt={4} mb={8}>
                            {fetchingColumns ? <Loader size="xs" mt="xs" /> : (
                              <List size="xs" spacing={2} icon={<IconColumns size={10} />}>
                                {columns.map(c => {
                                  const colName = typeof c === 'object' ? c.name : c;
                                  return (
                                    <List.Item key={colName}>
                                      <Group gap={4} wrap="nowrap">
                                        <Text 
                                          span 
                                          style={{ cursor: 'pointer' }} 
                                          onClick={() => insertText(colName)}
                                        >
                                          {colName}
                                        </Text>
                                        {typeof c === 'object' && c.type && (
                                          <Text size="10px" c="dimmed">({c.type})</Text>
                                        )}
                                      </Group>
                                    </List.Item>
                                  );
                                })}
                              </List>
                            )}
                          </Box>
                        )}
                      </List.Item>
                    ))}
                  </List>
                </ScrollArea>
              </Box>
            </Stack>
          </Paper>
        </Grid.Col>
      </Grid>

      {error && (
        <Alert icon={<IconAlertCircle size={16} />} title="Query failed" color="red" variant="light">
          <Text size="xs">{error}</Text>
        </Alert>
      )}

      {results && (
        <Paper withBorder shadow="sm" radius="md" style={{ overflow: 'hidden' }}>
          <Group p="xs" bg="var(--mantine-color-gray-0)" justify="space-between">
            <Group gap="sm">
              <Text size="xs" fw={600} c="blue">{results.length} rows</Text>
              <Divider orientation="vertical" />
              <Text size="xs" c="dimmed">Results Preview</Text>
            </Group>
            {results.length > 0 && (
              <Group gap="xs">
                 <Tooltip label="Copy results as JSON">
                   <ActionIcon variant="light" size="sm" onClick={() => {
                     navigator.clipboard.writeText(JSON.stringify(results, null, 2));
                     notifications.show({ message: 'All results copied to clipboard', color: 'teal' });
                   }}>
                     <IconCopy size={14} />
                   </ActionIcon>
                 </Tooltip>
              </Group>
            )}
          </Group>
          <ScrollArea h={results.length > 0 ? 350 : 'auto'} scrollbars="xy">
            <Table 
              striped 
              highlightOnHover 
              withColumnBorders 
              verticalSpacing="xs"
              horizontalSpacing="sm"
              stickyHeader
            >
              <Table.Thead>
                <Table.Tr>
                  {resultColumns.map((col) => (
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
                    {resultColumns.map((col) => (
                      <Table.Td key={col} style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {typeof row[col] === 'object' ? JSON.stringify(row[col]) : String(row[col])}
                      </Table.Td>
                    ))}
                  </Table.Tr>
                ))}
                {results.length === 0 && (
                  <Table.Tr>
                    <Table.Td colSpan={resultColumns.length || 1}>
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


