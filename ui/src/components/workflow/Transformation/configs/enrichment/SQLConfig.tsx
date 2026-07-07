import { Suspense, lazy } from 'react';
import { Stack, Select, Text, Box, Card, Group, rem, ThemeIcon, Alert } from '@mantine/core';
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
}

export function SQLConfig({ config, updateNodeConfig, nodeId, sources, availableFields = [] }: SQLConfigProps) {
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
