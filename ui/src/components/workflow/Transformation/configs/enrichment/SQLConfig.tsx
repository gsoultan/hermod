import { Suspense, lazy, type ReactNode } from 'react';
import { Stack, Select, Text, TextInput, rem, Alert, Title, Paper, SimpleGrid, Divider } from '@mantine/core';
import { sourceAllowsDirectQueries } from '@/lib/sourceCdc';
import { IconDatabase, IconInfoCircle } from '@tabler/icons-react';

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

// One decision of the form: a numbered title, a line saying what is being
// decided, and the controls that decide it.
function Section({ step, title, description, children }: {
  step: number;
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <Stack gap="sm" component="section">
      <Stack gap={2}>
        <Title order={4} size="sm">{`${step}. ${title}`}</Title>
        <Text size="xs" c="dimmed">{description}</Text>
      </Stack>
      {children}
    </Stack>
  );
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

  const sourceId = config.sourceId || config.sourceID || '';

  return (
    <Stack gap="lg">
      <Section step={1} title="Database" description="Where the statement runs.">
        <Select
          label="Database Source"
          placeholder="Select a configured database source"
          data={dbSources}
          value={sourceId}
          onChange={(val) => {
            updateNodeConfig(nodeId, {
              sourceId: val,
              sourceID: val // Keep both for backward compatibility
            });
          }}
          leftSection={<IconDatabase size={rem(16)} />}
          required
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
      </Section>

      <Divider />

      <Section
        step={2}
        title="Statement"
        description="Runs once for each message. A {{.field}} variable is bound to that message's value, never pasted into the SQL."
      >
        {sourceId ? (
          <Suspense fallback={<Text size="xs" c="dimmed">Loading the editor…</Text>}>
            {/* Keyed by the database: its tables, columns and results belong
                to the one that was open. */}
            <SQLQueryBuilder
              key={sourceId}
              type="source"
              intent="write"
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
            <Text size="xs" c="dimmed" data-testid="execute-sql-manual-preview">
              Live Preview does not run this node while you edit, because running it writes. Press
              Run Preview to run it once on the sample message.
            </Text>
          </Suspense>
        ) : (
          <Paper withBorder radius="md" p="lg">
            <Text size="sm" c="dimmed" ta="center">
              Pick a database above and the editor opens on its tables.
            </Text>
          </Paper>
        )}
      </Section>

      <Divider />

      <Section
        step={3}
        title="Returned data"
        description="What the node adds to the message once the statement has run. Both are optional."
      >
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
              below to keep them.
            </Text>
          </Alert>
        )}

        <SimpleGrid type="container" cols={{ base: 1, '520px': 2 }} spacing="md">
          <TextInput
            label="Returned Rows Field"
            placeholder="e.g. inserted (optional)"
            description="Keeps what the statement returns — RETURNING on PostgreSQL, SQLite and MariaDB, OUTPUT on SQL Server — under this name, so a generated id reads as inserted.id. Without it, what is returned is dropped."
            value={config.resultField || ''}
            onChange={(e) => {
              const value = e.currentTarget.value;
              updateNodeConfig(nodeId, { resultField: value });
            }}
            size="sm"
          />

          <TextInput
            label="Affected Rows Field"
            placeholder="e.g. rows_written (optional)"
            description="Writes the statement's affected-row count under this name. Without it, a node that changed nothing looks exactly like one that changed a thousand rows. With returned rows kept, this is the number of rows returned."
            value={config.affectedRowsField || ''}
            onChange={(e) => updateNodeConfig(nodeId, { affectedRowsField: e.currentTarget.value })}
            size="sm"
          />
        </SimpleGrid>

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
      </Section>

      <Divider />

      <Section
        step={4}
        title="Missing values"
        description="What to do when a message has no value for a variable the statement uses."
      >
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
      </Section>
    </Stack>
  );
}
