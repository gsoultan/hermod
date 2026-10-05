import { useState, useEffect, useRef, useMemo } from 'react';
import {
  Stack, Button, Text, Paper, Group, ScrollArea, Alert, ActionIcon, Tooltip,
  Grid, Modal, Badge, Tabs, Anchor,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import {
  IconAlertCircle, IconCopy, IconDatabase, IconPlayerPlay, IconTable,
  IconArrowsMaximize, IconArrowsMinimize, IconWand, IconBraces, IconTemplate,
} from '@tabler/icons-react';
import { SqlEditor } from './sqlBuilder/SqlEditor';
import { VariablesPanel } from './sqlBuilder/VariablesPanel';
import { SchemaExplorer } from './sqlBuilder/SchemaExplorer';
import { TemplatesPanel } from './sqlBuilder/TemplatesPanel';
import { ResultsTable } from './sqlBuilder/ResultsTable';
import { extractVariables, formatSQL } from './sqlBuilder/sqlText';
import { buildTemplates, keywordsFor, type SqlColumn, type SqlIntent } from './sqlBuilder/templates';

interface SQLQueryBuilderProps {
  type: 'source' | 'sink';
  sourceType?: string;
  config: any;
  onSelectResult?: (row: any) => void;
  initialQuery?: string;
  onQueryChange?: (query: string) => void;
  availableFields?: { path: string; type: string }[];
  sampleMessage?: any;
  /**
   * What the statement is for. A lookup or a batch query reads, which is the
   * default; execute_sql writes, and gets the keywords, templates and wording
   * of a statement that changes rows.
   */
  intent?: SqlIntent;
}

// Default query shown when no initialQuery is provided.
const DEFAULT_QUERY = 'SELECT * FROM tables LIMIT 10';

// The builder lays itself out by the width it is given, not by the window's.
// It is embedded in a third of a drawer and in a near-fullscreen modal, and a
// viewport breakpoint cannot tell those apart: at 1600px wide it split a 440px
// column into a 250px editor and a 130px schema list.
const CONTAINER_BREAKPOINTS = { xs: '360px', sm: '560px', md: '860px', lg: '1180px', xl: '1500px' };

type ReferenceTab = 'variables' | 'schema' | 'templates';

// normalizeRows coerces an API response into an array. The Go backend returns
// `null` for a zero-row result, which would otherwise hide the results preview.
function normalizeRows<T>(data: unknown): T[] {
  return Array.isArray(data) ? (data as T[]) : [];
}

// A column arrives as an object from every current endpoint; a bare name is
// what older ones sent.
function normalizeColumns(data: unknown): SqlColumn[] {
  return normalizeRows<any>(data).map((c) => (typeof c === 'object' && c !== null ? c : { name: String(c) }));
}

export function SQLQueryBuilder({
  type, sourceType, config, onSelectResult, initialQuery, onQueryChange,
  availableFields = [], sampleMessage, intent = 'read',
}: SQLQueryBuilderProps) {
  const writes = intent === 'write';
  // A write starts empty. The default is a SELECT, and showing one in a node
  // that has no statement saved made the node look configured when it was not.
  const [query, setQuery] = useState(initialQuery || (writes ? '' : DEFAULT_QUERY));
  const [loading, setLoading] = useState(false);
  const [results, setResults] = useState<any[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tables, setTables] = useState<string[]>([]);
  const [fetchingTables, setFetchingTables] = useState(false);
  const [schemaError, setSchemaError] = useState<string | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [columns, setColumns] = useState<SqlColumn[]>([]);
  const [fetchingColumns, setFetchingColumns] = useState(false);
  const [tab, setTab] = useState<ReferenceTab>('variables');
  // The statement a template replaced, kept so it can be put back.
  const [replaced, setReplaced] = useState<string | null>(null);
  const [expanded, { open: openExpanded, close: closeExpanded }] = useDisclosure(false);

  // activeEditorRef points at the textarea that last had focus, so an insert
  // lands at its caret. The inline and the expanded editor are never mounted
  // together, and insertText checks the element is still in the document.
  const activeEditorRef = useRef<HTMLTextAreaElement | null>(null);
  // abortRef cancels any in-flight query when a new one starts or the component
  // unmounts, preventing races and setState-after-unmount warnings.
  const abortRef = useRef<AbortController | null>(null);
  const schemaRequested = useRef(false);

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
      const response = await apiFetch(`/api/${type}s/query`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: buildConfigPayload(), query, sampleData: sampleMessage }),
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
        id: `sql-query-error-${err.message}`,
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
    schemaRequested.current = true;
    setFetchingTables(true);
    setSchemaError(null);
    try {
      const response = await apiFetch(`/api/${type}s/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(buildConfigPayload()),
        // Shown in the Schema tab, where it was asked for. A toast as well
        // reported one failure twice.
        silent: true,
      });
      if (response.ok) {
        const data = await response.json();
        setTables(normalizeRows<string>(data));
      } else {
        setSchemaError(await extractError(response, 'Failed to fetch tables'));
      }
    } catch (e: any) {
      setSchemaError(e?.message || 'Failed to fetch tables');
    } finally {
      setFetchingTables(false);
    }
  };

  const fetchColumns = async (tableName: string) => {
    setFetchingColumns(true);
    setSelectedTable(tableName);
    setColumns([]);
    try {
      const response = await apiFetch(`/api/${type}s/discover/columns`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ [type]: buildConfigPayload(), table: tableName }),
        silent: true,
      });
      if (response.ok) {
        const data = await response.json();
        setColumns(normalizeColumns(data));
      } else {
        setSchemaError(await extractError(response, `Failed to fetch the columns of ${tableName}`));
      }
    } catch (e: any) {
      setSchemaError(e?.message || `Failed to fetch the columns of ${tableName}`);
    } finally {
      setFetchingColumns(false);
    }
  };

  // Opening Schema reads the tables. It used to wait for a Load Tables button
  // inside the panel, which is one more thing to find before the first table.
  const changeTab = (value: string | null) => {
    if (!value) return;
    setTab(value as ReferenceTab);
    if (value === 'schema' && !schemaRequested.current) {
      void fetchTables();
    }
  };

  // insertText inserts a snippet at the current caret position (or replaces the
  // active selection) instead of always appending at the end. This is far more
  // ergonomic when editing long, multi-line queries.
  const insertText = (text: string) => {
    const el = activeEditorRef.current;
    if (!el || !el.isConnected) {
      const newQuery = query + (/\s$/.test(query) || query === '' ? '' : ' ') + text;
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

  const useTemplate = (sql: string) => {
    setReplaced(query.trim() ? query : null);
    handleQueryChange(sql);
  };

  const undoTemplate = () => {
    if (replaced === null) return;
    handleQueryChange(replaced);
    setReplaced(null);
  };

  const handleCopyQuery = () => {
    navigator.clipboard.writeText(query);
    notifications.show({
      id: 'sql-query-copied',
      message: 'Query copied to clipboard',
      color: 'teal',
    });
  };

  const lineCount = query ? query.split('\n').length : 0;
  const queryVariables = useMemo(() => extractVariables(query), [query]);
  const fieldPaths = useMemo(() => availableFields.map((f) => f.path), [availableFields]);
  const templates = useMemo(
    () => buildTemplates(intent, {
      engine: sourceType,
      table: selectedTable ?? undefined,
      columns: fetchingColumns ? undefined : columns,
      fields: fieldPaths,
    }),
    [intent, sourceType, selectedTable, columns, fetchingColumns, fieldPaths]
  );
  const keywords = useMemo(() => keywordsFor(intent), [intent]);

  const toolbar = (
    <Group justify="space-between" gap="xs">
      <Group gap="xs" wrap="nowrap">
        <IconDatabase size={18} color="var(--mantine-color-blue-filled)" />
        <Text fw={600} size="sm">{writes ? 'Statement' : 'Query Editor'}</Text>
        {sourceType && <Badge size="xs" variant="light" color="gray">{sourceType}</Badge>}
      </Group>
      <Group gap="xs" wrap="nowrap">
        <Tooltip label="Format query">
          <ActionIcon variant="default" size="md" onClick={() => handleQueryChange(formatSQL(query))} aria-label="Format query">
            <IconWand size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Copy query">
          <ActionIcon variant="default" size="md" onClick={handleCopyQuery} aria-label="Copy query">
            <IconCopy size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label={expanded ? 'Back to the form' : 'Open the workspace: editor, schema and results side by side'}>
          <ActionIcon
            variant="default"
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
          {writes ? 'Run statement' : 'Run Query'}
        </Button>
      </Group>
    </Group>
  );

  const reference = (large: boolean) => (
    <Paper withBorder radius="md" p="xs">
      <Tabs value={tab} onChange={changeTab}>
        <Tabs.List grow>
          <Tabs.Tab value="variables" leftSection={<IconBraces size={14} />}>
            Variables{queryVariables.length > 0 ? ` (${queryVariables.length})` : ''}
          </Tabs.Tab>
          <Tabs.Tab value="schema" leftSection={<IconTable size={14} />}>Schema</Tabs.Tab>
          <Tabs.Tab value="templates" leftSection={<IconTemplate size={14} />}>Templates</Tabs.Tab>
        </Tabs.List>

        <ScrollArea.Autosize mah={large ? '64vh' : 340} type="auto" offsetScrollbars pt="sm">
          <Tabs.Panel value="variables">
            <VariablesPanel
              variables={queryVariables}
              availableFields={availableFields}
              sampleMessage={sampleMessage}
              engine={sourceType}
              onInsert={insertText}
            />
          </Tabs.Panel>
          <Tabs.Panel value="schema">
            <SchemaExplorer
              tables={tables}
              loading={fetchingTables}
              error={schemaError}
              onLoad={fetchTables}
              selectedTable={selectedTable}
              columns={columns}
              loadingColumns={fetchingColumns}
              onSelectTable={fetchColumns}
              onInsert={insertText}
            />
          </Tabs.Panel>
          <Tabs.Panel value="templates">
            <TemplatesPanel
              intent={intent}
              templates={templates}
              keywords={keywords}
              table={selectedTable}
              hasStatement={query.trim() !== ''}
              onUseTemplate={useTemplate}
              onInsert={insertText}
              onClear={() => handleQueryChange('')}
            />
          </Tabs.Panel>
        </ScrollArea.Autosize>
      </Tabs>
    </Paper>
  );

  const workspace = (large: boolean) => (
    <Stack gap="sm">
      {toolbar}
      <Grid type="container" breakpoints={CONTAINER_BREAKPOINTS} gap="md">
        <Grid.Col span={{ base: 12, md: 7, lg: 8 }}>
          <Stack gap="xs">
            <SqlEditor
              label="SQL statement"
              value={query}
              onChange={handleQueryChange}
              onRun={executeQuery}
              onFocusEditor={(el) => { activeEditorRef.current = el; }}
              placeholder={writes
                ? 'INSERT INTO my_table (column_a)\nVALUES ({{.field}})\nRETURNING id'
                : 'SELECT * FROM my_table LIMIT 10'}
              height={large ? '46vh' : 260}
            />
            <Group justify="space-between" gap="xs">
              <Text size="xs" c="dimmed">
                {lineCount} lines · {query.length} chars
              </Text>
              <Text size="xs" c="dimmed">Cmd/Ctrl + Enter to run</Text>
            </Group>

            {writes && (
              <Text size="xs" c="dimmed">
                Run executes the statement on the database with the sample message. Rows it writes are real.
              </Text>
            )}

            {replaced !== null && (
              <Group gap="xs">
                <Text size="xs" c="dimmed">The template replaced your statement.</Text>
                <Anchor component="button" type="button" size="xs" onClick={undoTemplate}>
                  Undo
                </Anchor>
              </Group>
            )}

            {error && (
              <Alert icon={<IconAlertCircle size={16} />} title="Query failed" color="red" variant="light">
                <Text size="xs">{error}</Text>
              </Alert>
            )}

            {results && (
              <ResultsTable
                rows={results}
                title={writes ? 'Returned rows' : 'Results Preview'}
                emptyMessage={writes
                  ? 'The statement ran and returned no rows. Add RETURNING to see what it wrote.'
                  : 'Query returned no rows'}
                onSelectRow={onSelectResult}
                height={large ? 320 : 260}
              />
            )}
          </Stack>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 5, lg: 4 }}>
          {reference(large)}
        </Grid.Col>
      </Grid>
    </Stack>
  );

  return (
    <>
      <Modal
        opened={expanded}
        onClose={closeExpanded}
        title={<Group gap="xs"><IconDatabase size={18} /><Text fw={600}>SQL workspace</Text></Group>}
        size="96%"
        radius="md"
        centered
      >
        {expanded && workspace(true)}
      </Modal>

      {/* One editor at a time: two would hold the same statement and only one
          of them would be the one inserts land in. */}
      {expanded ? (
        <Paper withBorder radius="md" p="md">
          <Text size="sm" c="dimmed">The statement is open in the workspace.</Text>
        </Paper>
      ) : (
        workspace(false)
      )}
    </>
  );
}
