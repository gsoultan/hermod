import { useState, useEffect, useRef, useMemo } from 'react';
import {
  Stack, Button, Table, Text,
  Paper, Group, ScrollArea, Alert, ActionIcon, Tooltip,
  List, Divider, Loader, Textarea, Grid, Box, Modal, TextInput, Badge
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconAlertCircle, IconCopy, IconDatabase, IconPlayerPlay, IconTable,
  IconColumns, IconRefresh, IconPlus, IconArrowsMaximize, IconArrowsMinimize,
  IconWand, IconTrash, IconSearch
} from '@tabler/icons-react';

interface SQLQueryBuilderProps {
  type: 'source' | 'sink';
  sourceType?: string;
  config: any;
  onSelectResult?: (row: any) => void;
  initialQuery?: string;
  onQueryChange?: (query: string) => void;
}

// Common SQL keywords used by the "Quick Insert" toolbar. Kept outside the
// component so the reference stays stable across re-renders.
const QUICK_KEYWORDS = [
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'JOIN', 'LEFT JOIN', 'INNER JOIN',
  'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET', 'DISTINCT',
];

// Keywords that should start on a new line when formatting a query, making
// long statements far easier to read.
const NEWLINE_KEYWORDS = [
  'FROM', 'WHERE', 'AND', 'OR', 'LEFT JOIN', 'RIGHT JOIN', 'INNER JOIN',
  'OUTER JOIN', 'JOIN', 'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET',
  'UNION', 'VALUES', 'SET',
];

// formatSQL applies a lightweight, dependency-free formatting pass: it
// upper-cases well known keywords and breaks long statements onto multiple
// lines so they are easier to scan.
function formatSQL(sql: string): string {
  if (!sql.trim()) return sql;
  let result = sql.replace(/\s+/g, ' ').trim();
  // Break major clauses onto their own line (longest keywords first to avoid
  // partially matching shorter ones).
  for (const kw of [...NEWLINE_KEYWORDS].sort((a, b) => b.length - a.length)) {
    const re = new RegExp(`\\s+${kw.replace(/ /g, '\\s+')}\\b`, 'gi');
    result = result.replace(re, `\n${kw.toUpperCase()}`);
  }
  // Upper-case the leading SELECT for consistency.
  result = result.replace(/^\s*select\b/i, 'SELECT');
  return result.trim();
}

// Default query shown when no initialQuery is provided.
const DEFAULT_QUERY = 'SELECT * FROM tables LIMIT 10';

// renderCellValue converts an arbitrary result cell into a display-safe string,
// guarding against null/undefined (which would otherwise render the literal
// "null"/"undefined") and serialising nested objects/arrays.
function renderCellValue(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

// normalizeRows coerces an API response into an array. The Go backend returns
// `null` for a zero-row result, which would otherwise hide the results preview.
function normalizeRows<T>(data: unknown): T[] {
  return Array.isArray(data) ? (data as T[]) : [];
}

export function SQLQueryBuilder({ type, sourceType, config, onSelectResult, initialQuery, onQueryChange }: SQLQueryBuilderProps) {
  const [query, setQuery] = useState(initialQuery || DEFAULT_QUERY);
  const [loading, setLoading] = useState(false);
  const [results, setResults] = useState<any[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tables, setTables] = useState<string[]>([]);
  const [fetchingTables, setFetchingTables] = useState(false);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [columns, setColumns] = useState<any[]>([]);
  const [fetchingColumns, setFetchingColumns] = useState(false);
  const [tableFilter, setTableFilter] = useState('');
  const [expanded, { open: openExpanded, close: closeExpanded }] = useDisclosure(false);

  // activeEditorRef points at whichever textarea (inline or fullscreen) currently
  // has focus. Tracking focus instead of sharing one ref keeps insertText working
  // regardless of which editor mounts/unmounts first.
  const activeEditorRef = useRef<HTMLTextAreaElement | null>(null);
  // abortRef cancels any in-flight query when a new one starts or the component
  // unmounts, preventing races and setState-after-unmount warnings.
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (initialQuery !== undefined && initialQuery !== query) {
      setQuery(initialQuery);
    }
    // `query` is intentionally omitted: we only sync when the parent pushes a new
    // initialQuery, not on every local keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialQuery]);

  // Abort any pending request on unmount.
  useEffect(() => () => abortRef.current?.abort(), []);

  const handleQueryChange = (val: string) => {
    setQuery(val);
    onQueryChange?.(val);
  };

  // buildConfigPayload assembles the { type, config } object every discovery and
  // query endpoint expects, keeping source/sink type resolution in one place.
  const buildConfigPayload = () => ({
    type: sourceType || config?.type || '',
    config: config ?? {},
  });

  // extractError derives a human-readable message from a failed Response,
  // degrading gracefully to the HTTP status when the body is not JSON.
  const extractError = async (response: Response, fallback: string): Promise<string> => {
    let msg = fallback;
    try {
      const err = await response.json();
      if (err?.error) msg = err.error;
    } catch {
      msg = `Request failed (${response.status} ${response.statusText || 'error'})`;
    }
    const lower = msg.toLowerCase();
    if (lower.includes('offline') || lower.includes('worker')) {
      msg += '. Please ensure at least one worker is online or that the source/sink is reachable from the API.';
    }
    return msg;
  };

  const executeQuery = async () => {
    if (!query.trim()) return;

    // Cancel any previous run so only the latest result wins.
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`/api/${type}s/query`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: buildConfigPayload(), query }),
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new Error(await extractError(response, 'Failed to execute query'));
      }

      const data = await response.json();
      setResults(normalizeRows(data));
    } catch (err: any) {
      if (err?.name === 'AbortError') return; // superseded by a newer run / unmounted
      setError(err.message);
      notifications.show({
        title: 'Query Error',
        message: err.message,
        color: 'red',
      });
    } finally {
      if (abortRef.current === controller) {
        setLoading(false);
        abortRef.current = null;
      }
    }
  };

  const fetchTables = async () => {
    setFetchingTables(true);
    setError(null);
    try {
      const response = await fetch(`/api/${type}s/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(buildConfigPayload()),
      });
      if (response.ok) {
        const data = await response.json();
        setTables(normalizeRows<string>(data));
      } else {
        setError(await extractError(response, 'Failed to fetch tables'));
      }
    } catch (e: any) {
      console.error('Failed to fetch tables', e);
      setError(e.message);
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
        body: JSON.stringify({ [type]: buildConfigPayload(), table: tableName }),
      });
      if (response.ok) {
        const data = await response.json();
        setColumns(normalizeRows(data));
      }
    } catch (e) {
      console.error('Failed to fetch columns', e);
    } finally {
      setFetchingColumns(false);
    }
  };

  // insertText inserts a snippet at the current caret position (or replaces the
  // active selection) instead of always appending at the end. This is far more
  // ergonomic when editing long, multi-line queries.
  const insertText = (text: string) => {
    const el = activeEditorRef.current;
    if (!el) {
      const newQuery = query + (query.endsWith(' ') || query === '' ? '' : ' ') + text;
      handleQueryChange(newQuery);
      return;
    }

    const start = el.selectionStart ?? query.length;
    const end = el.selectionEnd ?? query.length;
    const before = query.slice(0, start);
    const after = query.slice(end);
    const needsLeadingSpace = before.length > 0 && !/\s$/.test(before);
    const snippet = (needsLeadingSpace ? ' ' : '') + text;
    const newQuery = before + snippet + after;
    handleQueryChange(newQuery);

    // Restore the caret just after the inserted snippet.
    const caret = before.length + snippet.length;
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(caret, caret);
    });
  };

  const handleFormat = () => {
    handleQueryChange(formatSQL(query));
  };

  const handleCopyQuery = () => {
    navigator.clipboard.writeText(query);
    notifications.show({ message: 'Query copied to clipboard', color: 'teal' });
  };

  const handleEditorKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      executeQuery();
    }
  };

  const resultColumns = results && results.length > 0 ? Object.keys(results[0]) : [];

  const filteredTables = useMemo(() => {
    const f = tableFilter.trim().toLowerCase();
    if (!f) return tables;
    return tables.filter((t) => t.toLowerCase().includes(f));
  }, [tables, tableFilter]);

  const lineCount = query ? query.split('\n').length : 0;

  const editorToolbar = (
    <Group justify="space-between">
      <Group gap="xs">
        <IconDatabase size={20} color="var(--mantine-color-blue-filled)" />
        <Text fw={600} size="sm">Query Editor</Text>
        <Badge size="xs" variant="light" color="gray">
          {query.length} chars · {lineCount} lines
        </Badge>
      </Group>
      <Group gap="xs">
        {onSelectResult && results && results.length > 0 && (
          <Text size="xs" c="dimmed">Click a row to select</Text>
        )}
        <Tooltip label="Format query">
          <ActionIcon variant="light" size="md" color="grape" onClick={handleFormat} aria-label="Format query">
            <IconWand size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Copy query">
          <ActionIcon variant="light" size="md" onClick={handleCopyQuery} aria-label="Copy query">
            <IconCopy size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label={expanded ? 'Collapse editor' : 'Expand editor (fullscreen)'}>
          <ActionIcon
            variant="light"
            size="md"
            onClick={expanded ? closeExpanded : openExpanded}
            aria-label="Toggle fullscreen editor"
          >
            {expanded ? <IconArrowsMinimize size={16} /> : <IconArrowsMaximize size={16} />}
          </ActionIcon>
        </Tooltip>
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
  );

  const quickInsertBar = (
    <Group gap="xs">
      <Text size="xs" fw={500} c="dimmed">Quick Insert:</Text>
      {QUICK_KEYWORDS.map((kw) => (
        <Button key={kw} size="compact-xs" variant="light" onClick={() => insertText(kw)}>
          {kw}
        </Button>
      ))}
      <Tooltip label="Insert dynamic last value variable">
        <Button size="compact-xs" variant="light" color="orange" onClick={() => insertText('{{.last_value}}')}>
          {"{{.last_value}}"}
        </Button>
      </Tooltip>
      <Button
        size="compact-xs"
        variant="subtle"
        color="gray"
        leftSection={<IconTrash size={12} />}
        onClick={() => handleQueryChange('')}
      >
        Clear
      </Button>
    </Group>
  );

  const editorField = (fullscreen: boolean) => (
    <Textarea
      placeholder="SELECT * FROM my_table LIMIT 10"
      value={query}
      onChange={(e) => handleQueryChange(e.currentTarget.value)}
      onKeyDown={handleEditorKeyDown}
      onFocus={(e) => { activeEditorRef.current = e.currentTarget; }}
      minRows={fullscreen ? 20 : 6}
      maxRows={fullscreen ? 30 : 12}
      autosize
      spellCheck={false}
      styles={{
        input: {
          fontFamily: 'JetBrains Mono, Menlo, Monaco, Courier New, monospace',
          fontSize: '13px',
          lineHeight: 1.6,
          backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))',
          color: 'light-dark(var(--mantine-color-black), var(--mantine-color-white))',
        }
      }}
    />
  );

  return (
    <Stack gap="md">
      <Modal
        opened={expanded}
        onClose={closeExpanded}
        title={<Group gap="xs"><IconDatabase size={18} /><Text fw={600}>Query Editor</Text></Group>}
        size="90%"
        radius="md"
      >
        <Stack gap="sm">
          {editorToolbar}
          {editorField(true)}
          {quickInsertBar}
          <Text size="xs" c="dimmed">Tip: press Cmd/Ctrl + Enter to run the query.</Text>
        </Stack>
      </Modal>

      <Grid gap="md">
        <Grid.Col span={{ base: 12, md: 8 }}>
          <Stack gap="xs">
            <Paper withBorder p="md" shadow="sm" radius="md">
              <Stack gap="sm">
                {editorToolbar}
                {editorField(false)}
                {quickInsertBar}
                <Text size="xs" c="dimmed">Tip: press Cmd/Ctrl + Enter to run the query.</Text>
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

              {tables.length > 0 && (
                <TextInput
                  size="xs"
                  placeholder="Filter tables..."
                  value={tableFilter}
                  onChange={(e) => setTableFilter(e.currentTarget.value)}
                  leftSection={<IconSearch size={12} />}
                />
              )}

              <Box style={{ flex: 1, minHeight: 0 }}>
                <ScrollArea h={300}>
                  {tables.length === 0 && !fetchingTables && (
                    <Box py="xl" ta="center">
                      <Button size="xs" variant="light" onClick={fetchTables}>Load Tables</Button>
                    </Box>
                  )}
                  {tables.length > 0 && filteredTables.length === 0 && (
                    <Text size="xs" c="dimmed" ta="center" py="md">No tables match "{tableFilter}"</Text>
                  )}
                  <List size="xs" spacing={4} icon={<IconTable size={12} />}>
                    {filteredTables.map(t => (
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
          <Group p="xs" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))" justify="space-between">
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
                        {renderCellValue(row[col])}
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
