import { Suspense, lazy } from 'react';
import { Stack, Select, Text, TextInput, Box, Card, Group, rem, ThemeIcon, Alert } from '@mantine/core';
import { sourceAllowsDirectQueries } from '@/lib/sourceCdc';
import { IconDatabase, IconInfoCircle, IconSearch } from '@tabler/icons-react';

const SQLQueryBuilder = lazy(() =>
  import('../../../../forms/SQLQueryBuilder').then((m) => ({ default: m.SQLQueryBuilder }))
);

// Whether a statement hands rows back: RETURNING, SQL Server's OUTPUT, or a
// plain SELECT. Literals and comments are dropped first so a note that happens
// to say 'returning' does not count. It only decides whether to show a hint, so
// a wrong guess costs a line of text and never changes what runs.
export function statementReturnsRows(sql: string): boolean {
  const bare = (sql || '')
    .replace(/'(?:[^']|'')*'/g, "''")
    .replace(/--[^\n]*/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ');
  return /\breturning\b/i.test(bare) || /\boutput\s+(inserted|deleted)\./i.test(bare) || /^\s*select\b/i.test(bare);
}

interface SQLConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  sources: any[];
  availableFields?: any[];
  incomingPayload?: any;
}

export function SQLConfig({ config, updateNodeConfig, nodeId, sources, availableFields = [], incomingPayload }: SQLConfigProps) {
  const dbSources = (Array.isArray(sources) ? sources : [])
    .filter((s: any) =>
      [
        'postgres',
        'mysql',
        'mssql',
        'sqlite',
        'mariadb',
        'oracle',
        'db2',
        'mongodb',
        'yugabyte',
        'clickhouse',
      ].includes(s.type)
    )
    .map((s: any) => ({ label: s.name, value: s.id }));

  const selectedSource = (Array.isArray(sources) ? sources : []).find(
    (s) => s.id === (config.sourceId || config.sourceID)
  );

  // Unlike db_lookup and batch_sql, a CDC source is not refused here. Those two
  // read, and the rule keeps query load off a database already paying for
  // logical replication. execute_sql is there to write -- the most it hands
  // back is a row count and the rows its own statement returns -- so its hazard
  // is a feedback loop instead: a write into a published table produces a
  // change event that comes back round the pipeline. That is scoped to the
  // table while use_cdc is scoped to the source, so blocking the source would
  // break the ordinary case of writing an audit or status row nobody streams.
  // Name the risk and leave the choice.
  const targetIsCDC = !!selectedSource && !sourceAllowsDirectQueries(selectedSource);

  // Returned rows are kept only when a field is named for them, so an existing
  // node's messages do not grow a field on upgrade. The cost of opt-in is that
  // `INSERT ... RETURNING id` looks like it worked and the id is nowhere --
  // which is what this says.
  const resultField = String(config.resultField || '').trim();
  const discardsReturnedRows =
    !resultField && statementReturnsRows(config.queryTemplate || config.query || '');

  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="indigo"
        variant="light"
        radius="md"
        title="SQL Enrichment"
      >
        <Text size="sm">
          Enrich your message by executing a query against an external database using data from
          the current payload.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="indigo" radius="md">
              <IconDatabase size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Database Connection
            </Text>
          </Group>

          <Select
            label="Database Source"
            placeholder="Select a configured database source"
            data={dbSources}
            value={config.sourceId || config.sourceID || ''}
            onChange={(val) => {
              updateNodeConfig(nodeId, { 
                sourceId: val,
                sourceID: val // Keep both for backward compatibility
              });
            }}
            leftSection={<IconDatabase size={rem(16)} />}
            required
            size="sm"
            description="Choose the database to query for enrichment."
          />

          <TextInput
            label="Returned Rows Field"
            placeholder="e.g. inserted (optional)"
            description="Keeps what the statement returns — RETURNING on PostgreSQL, SQLite and MariaDB, OUTPUT on SQL Server — in the message under this name, so a generated id reads as inserted.id. Without it the statement runs and what it returns is dropped."
            value={config.resultField || ''}
            onChange={(e) => {
              const value = e.currentTarget.value;
              updateNodeConfig(nodeId, { resultField: value });
            }}
            size="sm"
          />

          {discardsReturnedRows && (
            <Alert
              data-testid="execute-sql-returning-hint"
              icon={<IconInfoCircle size={rem(18)} />}
              color="yellow"
              variant="light"
              radius="md"
            >
              <Text size="sm">
                This statement returns rows and nothing is keeping them, so the message — and
                the preview — will come back unchanged. Name a <strong>Returned Rows Field</strong>{' '}
                above to keep them.
              </Text>
            </Alert>
          )}

          {resultField && (
            <Select
              label="Rows to keep"
              description="The shape follows this choice, never the number of rows: an object, or a list (at most 1,000 rows) — even when one row comes back."
              data={[
                { value: 'first', label: 'First row (an object)' },
                { value: 'all', label: 'Every row (a list)' },
              ]}
              value={String(config.resultRows || '').toLowerCase() === 'all' ? 'all' : 'first'}
              onChange={(val) => updateNodeConfig(nodeId, { resultRows: val || 'first' })}
              allowDeselect={false}
              size="sm"
            />
          )}

          <TextInput
            label="Affected Rows Field"
            placeholder="e.g. rows_written (optional)"
            description="Writes the statement's affected-row count into the message under this name. Without it, a node that changed nothing looks exactly like one that changed a thousand rows. With returned rows kept, this is the number of rows returned."
            value={config.affectedRowsField || ''}
            onChange={(e) => updateNodeConfig(nodeId, { affectedRowsField: e.currentTarget.value })}
            size="sm"
          />

          <Select
            label="When a variable resolves to nothing"
            description="A {{ }} token with no matching field is bound as NULL. That is right for an optional field and identical to a typo — and on a write, a NULL variable is a statement that changes nothing, silently."
            data={[
              { value: 'null', label: 'Bind NULL and run the statement' },
              { value: 'fail', label: 'Fail the message' },
            ]}
            value={String(config.onUnresolved || '').toLowerCase() === 'fail' ? 'fail' : 'null'}
            onChange={(val) => updateNodeConfig(nodeId, { onUnresolved: val || 'null' })}
            allowDeselect={false}
            size="sm"
          />

          {targetIsCDC && (
            <Alert
              data-testid="execute-sql-cdc-warning"
              icon={<IconInfoCircle size={rem(18)} />}
              color="yellow"
              variant="light"
              radius="md"
            >
              <Text size="sm">
                <strong>{selectedSource?.name}</strong> has CDC enabled. This node writes, so any
                statement touching a table in that source's publication produces a change event
                that comes back into the pipeline — a loop that feeds itself. Writing to a table
                nobody streams is fine; check which tables are published before pointing this at
                one that is.
              </Text>
            </Alert>
          )}
        </Stack>
      </Card>

      <Stack gap="xs">
        <Group gap="xs">
          <IconSearch size={rem(18)} className="text-gray-500" />
          <Text size="sm" fw={600}>
            Query Configuration
          </Text>
        </Group>
        <Box
          style={{
            flex: 1,
            minHeight: 500,
            border: '1px solid var(--mantine-color-gray-3)',
            borderRadius: rem(8),
            overflow: 'hidden',
          }}
        >
          <Suspense fallback={<Text size="xs" p="md">Loading query builder...</Text>}>
            {(config.sourceId || config.sourceID) ? (
              <SQLQueryBuilder
                type="source"
                initialQuery={config.queryTemplate || config.query || ''}
                onQueryChange={(val: string) => {
                  updateNodeConfig(nodeId, { 
                    queryTemplate: val,
                    query: val // Keep both for backward compatibility
                  });
                }}
                config={selectedSource?.config || {}}
                sourceType={selectedSource?.type}
                availableFields={availableFields}
                sampleMessage={incomingPayload}
              />
            ) : (
              <Box p="xl" style={{ textAlign: 'center' }}>
                <Text size="sm" c="dimmed">
                  Please select a Database Source above to enable the Query Builder.
                </Text>
              </Box>
            )}
          </Suspense>
        </Box>
      </Stack>
    </Stack>
  );
}
