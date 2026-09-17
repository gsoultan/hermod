import { Suspense, lazy } from 'react';
import { Stack, Select, Text, Box, Card, Group, rem, ThemeIcon, Alert } from '@mantine/core';
import { sourceAllowsDirectQueries } from '@/lib/sourceCdc';
import { IconDatabase, IconInfoCircle, IconSearch } from '@tabler/icons-react';

const SQLQueryBuilder = lazy(() =>
  import('../../../../forms/SQLQueryBuilder').then((m) => ({ default: m.SQLQueryBuilder }))
);

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
  // logical replication. execute_sql only writes -- it runs ExecContext and can
  // hand back nothing but a row count -- so its hazard is a feedback loop
  // instead: a write into a published table produces a change event that comes
  // back round the pipeline. That is scoped to the table while use_cdc is
  // scoped to the source, so blocking the source would break the ordinary case
  // of writing an audit or status row nobody streams. Name the risk and leave
  // the choice.
  const targetIsCDC = !!selectedSource && !sourceAllowsDirectQueries(selectedSource);

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
