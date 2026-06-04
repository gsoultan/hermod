import { Suspense, lazy } from 'react';
import { Stack, Select, Text, Box } from '@mantine/core';
import { IconDatabase } from '@tabler/icons-react';

const SQLQueryBuilder = lazy(() =>
  import('../../../../forms/SQLQueryBuilder').then((m) => ({ default: m.SQLQueryBuilder }))
);

interface SQLConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  sources: any[];
}

export function SQLConfig({ config, updateNodeConfig, nodeId, sources }: SQLConfigProps) {
  const dbSources = (Array.isArray(sources) ? sources : [])
    .filter((s: any) => ['postgres', 'mysql', 'mssql', 'sqlite', 'mariadb', 'oracle', 'db2', 'mongodb', 'yugabyte', 'clickhouse'].includes(s.type))
    .map((s: any) => ({ label: s.name, value: s.id }));

  return (
    <Stack gap="md">
      <Select
        label="Database Source"
        placeholder="Select source"
        data={dbSources}
        value={config.sourceID || ''}
        onChange={(val) => updateNodeConfig(nodeId, { sourceID: val })}
        leftSection={<IconDatabase size="1rem" />}
        required
      />
      <Box style={{ flex: 1, minHeight: 400 }}>
        <Suspense fallback={<Text size="xs">Loading query builder...</Text>}>
          <SQLQueryBuilder 
            type="source"
            initialQuery={config.query || ''} 
            onQueryChange={(val: string) => updateNodeConfig(nodeId, { query: val })}
            config={(Array.isArray(sources) ? sources : []).find(s => s.id === config.sourceID)?.config || {}}
            sourceType={(Array.isArray(sources) ? sources : []).find(s => s.id === config.sourceID)?.type}
          />
        </Suspense>
      </Box>
    </Stack>
  );
}
