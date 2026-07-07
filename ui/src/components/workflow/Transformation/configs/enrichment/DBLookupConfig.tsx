import { Suspense, lazy, useState, useMemo } from 'react';
import {
  Stack,
  Tabs,
  Group,
  Select,
  TextInput,
  Button,
  Divider,
  Text,
  Modal,
  rem,
  Alert,
  Autocomplete,
} from '@mantine/core';
import {
  IconDatabase,
  IconSettings,
  IconPlayerPlay,
  IconInfoCircle,
} from '@tabler/icons-react';
import { TemplateField } from '../../../../shared/TemplateField';

const SQLQueryBuilder = lazy(() =>
  import('../../../../forms/SQLQueryBuilder').then((m) => ({ default: m.SQLQueryBuilder }))
);

interface DBLookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
  sources: any[];
  onTest: () => void;
  testing: boolean;
}

export function DBLookupConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  sources,
  onTest,
  testing,
}: DBLookupConfigProps) {
  const [sqlExplorerOpened, setSqlExplorerOpened] = useState(false);

  const dbSources = (Array.isArray(sources) ? sources : [])
    .filter((s: any) =>
      [
        'postgres',
        'mysql',
        'mssql',
        'sqlite',
        'mariadb',
        'oracle',
        'mongodb',
        'clickhouse',
      ].includes(s.type)
    )
    .map((s: any) => ({ label: s.name, value: s.id }));

  const selectedSource = (Array.isArray(sources) ? sources : []).find(
    (s) => s.id === config.sourceId
  );

  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="blue"
        variant="light"
        radius="md"
        title="Database Lookup"
      >
        <Text size="sm">
          Enrich your message by looking up data in an external database. 
          You can use a simple table lookup or a full SQL query template.
        </Text>
      </Alert>

      <Tabs defaultValue="query" variant="pills" radius="md">
        <Tabs.List grow mb="md">
          <Tabs.Tab value="query" leftSection={<IconDatabase size={rem(16)} />}>
            Query Configuration
          </Tabs.Tab>
          <Tabs.Tab value="settings" leftSection={<IconSettings size={rem(16)} />}>
            Settings & Test
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="query">
          <Stack gap="sm">
            <Select
              label="Database Source"
              placeholder="Select a configured database source"
              data={dbSources}
              value={config.sourceId || ''}
              onChange={(val) => updateNodeConfig(nodeId, { sourceId: val })}
              leftSection={<IconDatabase size={rem(16)} />}
              required
              size="sm"
            />

            <TextInput
              label="Target Table"
              placeholder="e.g. users"
              value={config.table || ''}
              onChange={(e) => updateNodeConfig(nodeId, { table: e.currentTarget.value })}
              size="sm"
            />

            <Group grow>
              <Autocomplete
                label="Key Field (Message)"
                placeholder="e.g. user_id"
                data={fieldPaths || []}
                value={config.keyField || ''}
                onChange={(val) => updateNodeConfig(nodeId, { keyField: val })}
                size="sm"
              />
              <TextInput
                label="Key Column (DB)"
                placeholder="e.g. id"
                value={config.keyColumn || ''}
                onChange={(e) => updateNodeConfig(nodeId, { keyColumn: e.currentTarget.value })}
                size="sm"
              />
            </Group>

            <Group grow>
              <TextInput
                label="Value Column (DB)"
                placeholder="e.g. full_name"
                value={config.valueColumn || ''}
                onChange={(e) => updateNodeConfig(nodeId, { valueColumn: e.currentTarget.value })}
                size="sm"
              />
              <TextInput
                label="Target Field (Message)"
                placeholder="e.g. user_name"
                value={config.targetField || ''}
                onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
                size="sm"
              />
            </Group>

            <Divider label="Or use a full Query Template" labelPosition="center" my="sm" />

            <Stack gap={4}>
              <Group justify="space-between" align="center">
                <Text size="sm" fw={500}>
                  Query Template (SQL)
                </Text>
                {config.sourceId && (
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="blue"
                    leftSection={<IconDatabase size={14} />}
                    onClick={() => setSqlExplorerOpened(true)}
                  >
                    SQL Query Builder
                  </Button>
                )}
              </Group>
              <TemplateField
                placeholder="SELECT * FROM users WHERE tenant_id = {{.tenant_id}} AND status = 'active'"
                value={config.queryTemplate || ''}
                onChange={(val: string) => updateNodeConfig(nodeId, { queryTemplate: val })}
                availableFields={availableFields}
                multiline
              />
            </Stack>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="settings">
          <Stack gap="sm">
            <TemplateField
              label="Where Clause"
              placeholder="status = 'active' AND id = {{.user_id}}"
              value={config.whereClause || ''}
              onChange={(val: string) => updateNodeConfig(nodeId, { whereClause: val })}
              availableFields={availableFields}
              multiline
              description="Additional filter for simple table lookup."
            />
            
            <Group grow align="flex-end">
              <TextInput
                label="Default Value"
                placeholder="Value if not found"
                value={config.defaultValue || ''}
                onChange={(e) => updateNodeConfig(nodeId, { defaultValue: e.currentTarget.value })}
                size="sm"
              />
              <TextInput
                label="Cache TTL"
                placeholder="e.g. 5m, 1h"
                value={config.ttl || ''}
                onChange={(e) => updateNodeConfig(nodeId, { ttl: e.currentTarget.value })}
                size="sm"
              />
            </Group>

            <TextInput
              label="Flatten Into"
              placeholder="e.g. customer_flat or '.' for top level"
              value={config.flattenInto || ''}
              onChange={(e) => updateNodeConfig(nodeId, { flattenInto: e.currentTarget.value })}
              description="If the result is an object, copy its fields into this path."
              size="sm"
            />

            <Button
              variant="filled"
              color="indigo"
              mt="md"
              leftSection={<IconPlayerPlay size={rem(16)} />}
              onClick={onTest}
              loading={testing}
              fullWidth
            >
              Test Lookup
            </Button>
          </Stack>
        </Tabs.Panel>
      </Tabs>

      <Modal
        opened={sqlExplorerOpened}
        onClose={() => setSqlExplorerOpened(false)}
        title="SQL Query Builder"
        size="90%"
        radius="md"
      >
        <Suspense fallback={<Text size="xs" p="md">Loading SQL builder...</Text>}>
          {config.sourceId && (
            <SQLQueryBuilder
              type="source"
              sourceType={selectedSource?.type}
              config={selectedSource?.config || {}}
              initialQuery={config.queryTemplate || ''}
              onQueryChange={(val: string) => updateNodeConfig(nodeId, { queryTemplate: val })}
              availableFields={availableFields as any}
            />
          )}
        </Suspense>
        <Group justify="flex-end" mt="md">
          <Button onClick={() => setSqlExplorerOpened(false)}>Use Query & Close</Button>
        </Group>
      </Modal>
    </Stack>
  );
}
