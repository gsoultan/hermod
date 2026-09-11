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
  SegmentedControl,
  Tooltip,
  Switch,
  Box,
} from '@mantine/core';
import {
  IconDatabase,
  IconSettings,
  IconPlayerPlay,
  IconInfoCircle,
  IconTable,
  IconCode,
  IconArrowRight,
  IconHelp,
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
  incomingPayload?: any;
}

export function DBLookupConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  sources,
  onTest,
  testing,
  incomingPayload,
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

  const lookupMode = config.mode || (config.queryTemplate ? 'query' : 'table');

  // Mirrors resolveMissPolicy in pkg/comm/transformer/lookup/onmiss.go, inference
  // included: an unset onMiss means "default" when a defaultValue is configured
  // and "passthrough" otherwise, and an unrecognised value falls back rather
  // than erroring. Showing anything else would name a policy the pipeline is not
  // actually running.
  const hasDefaultValue = String(config.defaultValue || '') !== '';
  const missPolicy = useMemo(() => {
    const chosen = String(config.onMiss || '').trim().toLowerCase();
    if (chosen === 'fail' || chosen === 'default' || chosen === 'passthrough') return chosen;
    return hasDefaultValue ? 'default' : 'passthrough';
  }, [config.onMiss, hasDefaultValue]);

  // Value Column(s), Target Field and Flatten Result combine into four different
  // output shapes, and which one you get is not obvious from the three inputs.
  // Naming the resulting paths here is the difference between "the lookup did
  // nothing" and "the columns are one level down from where I looked".
  const outputShape = useMemo(() => {
    const target = String(config.targetField || '').trim() || 'result';
    const columns = String(config.valueColumn || '')
      .split(',')
      .map((c: string) => c.trim())
      .filter(Boolean);
    const wholeRow = columns.length === 0 || (columns.length === 1 && columns[0] === '*');
    const single = !wholeRow && columns.length === 1;

    const flattenInto = String(config.flattenInto || '').trim();
    const prefix = flattenInto === '.' ? '' : flattenInto.replace(/\.$/, '');

    return {
      target,
      columns,
      wholeRow,
      single,
      flattening: flattenInto !== '',
      nestedPaths: columns.map((c: string) => `${target}.${c}`).join(', '),
      flatPaths: columns.map((c: string) => (prefix ? `${prefix}.${c}` : c)).join(', '),
      flatPrefix: prefix,
    };
  }, [config.targetField, config.valueColumn, config.flattenInto]);

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
          Choose <b>Table Lookup</b> for simple key-value retrieval or <b>SQL Query</b> for advanced logic.
        </Text>
      </Alert>

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

      <Tabs defaultValue="config" variant="outline" radius="md">
        <Tabs.List mb="md">
          <Tabs.Tab value="config" leftSection={<IconSettings size={rem(16)} />}>
            Lookup Config
          </Tabs.Tab>
          <Tabs.Tab value="output" leftSection={<IconArrowRight size={rem(16)} />}>
            Output Mapping
          </Tabs.Tab>
          <Tabs.Tab value="advanced" leftSection={<IconSettings size={rem(16)} />}>
            Advanced & Test
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="config">
          <Stack gap="md">
            <Box>
              <Text size="xs" fw={500} mb={4} c="dimmed">LOOKUP METHOD</Text>
              <SegmentedControl
                fullWidth
                value={lookupMode}
                onChange={(val) => updateNodeConfig(nodeId, { mode: val })}
                data={[
                  {
                    label: (
                      <Group gap="xs" justify="center">
                        <IconTable size={16} />
                        <Text size="sm">Table Lookup</Text>
                      </Group>
                    ),
                    value: 'table',
                  },
                  {
                    label: (
                      <Group gap="xs" justify="center">
                        <IconCode size={16} />
                        <Text size="sm">SQL Query</Text>
                      </Group>
                    ),
                    value: 'query',
                  },
                ]}
              />
            </Box>

            {lookupMode === 'table' ? (
              <Stack gap="sm">
                <TextInput
                  label="Target Table"
                  placeholder="e.g. users"
                  value={config.table || ''}
                  onChange={(e) => updateNodeConfig(nodeId, { table: e.currentTarget.value })}
                  size="sm"
                  required
                />

                <Group grow align="flex-start">
                  <TextInput
                    label="Key Column (DB)"
                    placeholder="e.g. id"
                    value={config.keyColumn || ''}
                    onChange={(e) => updateNodeConfig(nodeId, { keyColumn: e.currentTarget.value })}
                    size="sm"
                    required
                    description="Column in DB to match against"
                  />
                  <Autocomplete
                    label="Key Field (Message)"
                    placeholder="e.g. user_id"
                    data={fieldPaths || []}
                    value={config.keyField || ''}
                    onChange={(val) => updateNodeConfig(nodeId, { keyField: val })}
                    size="sm"
                    required
                    description="Field in message containing the key"
                  />
                </Group>

                <TemplateField
                  label="Additional Filter (Where Clause)"
                  placeholder="status = 'active' AND type = 'customer'"
                  value={config.whereClause || ''}
                  onChange={(val: string) => updateNodeConfig(nodeId, { whereClause: val })}
                  availableFields={availableFields}
                  multiline
                  description="Optional SQL conditions appended to the lookup."
                />
              </Stack>
            ) : (
              <Stack gap="sm">
                <Stack gap={4}>
                  <Group justify="space-between" align="center">
                    <Text size="sm" fw={500}>
                      SQL Query Template
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
                  <Text size="xs" c="dimmed">
                    Use <code>{`{{.field}}`}</code> to inject values from the message.
                  </Text>
                </Stack>
              </Stack>
            )}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="output">
          <Stack gap="sm">
            <Group grow align="flex-start">
              <TextInput
                label={
                  <Group gap={4}>
                    <Text size="sm" fw={500}>Value Column(s)</Text>
                    <Tooltip label="Comma-separated list of columns to fetch. Use * for all columns. If using SQL Query mode, this acts as a filter on the returned columns.">
                      <IconHelp size={14} style={{ cursor: 'help' }} />
                    </Tooltip>
                  </Group>
                }
                placeholder={lookupMode === 'table' ? "e.g. email, name" : "Leave blank to get all columns"}
                value={config.valueColumn || ''}
                onChange={(e) => updateNodeConfig(nodeId, { valueColumn: e.currentTarget.value })}
                size="sm"
                description={lookupMode === 'table' 
                  ? "Columns to retrieve from the database." 
                  : "Optional: name of the column to extract from the query result."}
              />
              <Autocomplete
                label="Target Field (Message)"
                placeholder="e.g. user_details"
                data={fieldPaths}
                value={config.targetField || ''}
                onChange={(val) => updateNodeConfig(nodeId, { targetField: val })}
                size="sm"
                required
                description="Path in message to store the result."
              />
            </Group>

            <Divider mt="xs" />

            <Box p="sm" style={{ background: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))', borderRadius: rem(8), border: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
              <Group justify="space-between" align="flex-start" wrap="nowrap">
                <Stack gap={2}>
                  <Text size="sm" fw={600}>Flatten Result</Text>
                  <Text size="xs" c="dimmed">Automatically merge lookup columns into the message.</Text>
                </Stack>
                <Switch 
                  size="md"
                  checked={!!config.flattenInto}
                  onChange={(e) => updateNodeConfig(nodeId, { flattenInto: e.currentTarget.checked ? '.' : '' })}
                />
              </Group>

              {(config.flattenInto !== undefined && config.flattenInto !== '') && (
                <Stack gap="xs" mt="sm">
                  <TextInput
                    label="Prefix / Target Path"
                    placeholder="e.g. user_info (leave as . for top level)"
                    value={config.flattenInto}
                    onChange={(e) => updateNodeConfig(nodeId, { flattenInto: e.currentTarget.value })}
                    size="sm"
                    description="The path where flattened fields will be placed."
                    leftSection={<Text size="xs" c="dimmed">Map to:</Text>}
                    leftSectionWidth={60}
                  />
                  <Group gap={4}>
                    <Button 
                      variant="subtle" 
                      size="compact-xs" 
                      color="gray"
                      onClick={() => updateNodeConfig(nodeId, { flattenInto: '.' })}
                    >
                      Top Level (.)
                    </Button>
                    {config.targetField && config.targetField !== '.' && (
                      <Button 
                        variant="subtle" 
                        size="compact-xs" 
                        color="gray"
                        onClick={() => updateNodeConfig(nodeId, { flattenInto: config.targetField })}
                      >
                        Match Target Field
                      </Button>
                    )}
                  </Group>
                </Stack>
              )}
            </Box>
            
            <Alert color="indigo" variant="light" data-testid="lookup-output-shape">
              <Text size="xs" fw={700} mb={4}>
                What the next node will see
              </Text>
              <Stack gap={2}>
                {outputShape.single && (
                  <Text size="xs">
                    A single value from <code>{outputShape.columns[0]}</code>, at{' '}
                    <code>{outputShape.target}</code>.
                  </Text>
                )}
                {outputShape.wholeRow && (
                  <Text size="xs">
                    An object holding every column of the matched row, at{' '}
                    <code>{outputShape.target}</code> — address one column as{' '}
                    <code>{outputShape.target}.&lt;column&gt;</code>.
                  </Text>
                )}
                {!outputShape.single && !outputShape.wholeRow && (
                  <Text size="xs">
                    An object at <code>{outputShape.target}</code>:{' '}
                    <code>{outputShape.nestedPaths}</code>.
                  </Text>
                )}

                {outputShape.flattening && outputShape.single && (
                  <Text size="xs" c="orange">
                    Flatten Result does nothing here: it splits a row into fields, and a single
                    column is already one value.
                  </Text>
                )}
                {outputShape.flattening && outputShape.wholeRow && (
                  <Text size="xs">
                    Also written as one field per column{' '}
                    {outputShape.flatPrefix
                      ? <>under <code>{outputShape.flatPrefix}</code></>
                      : 'at the top level of the message'}
                    .
                  </Text>
                )}
                {outputShape.flattening && !outputShape.single && !outputShape.wholeRow && (
                  <Text size="xs">Flattened to: <code>{outputShape.flatPaths}</code>.</Text>
                )}
              </Stack>
            </Alert>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="advanced">
          <Stack gap="sm">
            <Select
              label="When no row matches"
              description="A miss is not automatically a bug, but it should be a decision — without one, an enriched message and an un-enriched one reach the sink looking identical."
              data={[
                { value: 'passthrough', label: 'Pass the message through unchanged' },
                { value: 'default', label: 'Write the default value' },
                { value: 'fail', label: 'Fail the message' },
              ]}
              value={missPolicy}
              onChange={(val) => updateNodeConfig(nodeId, { onMiss: val || 'passthrough' })}
              allowDeselect={false}
              size="sm"
            />

            {missPolicy === 'default' && !hasDefaultValue && (
              <Alert color="orange" variant="light" data-testid="lookup-miss-warning">
                <Text size="xs">
                  Nothing will be written on a miss: this policy needs a <b>Default Value</b>.
                  Without one it behaves exactly like passing the message through.
                </Text>
              </Alert>
            )}
            {missPolicy === 'fail' && (
              <Text size="xs" c="dimmed">
                The message follows the normal retry and dead-letter path.{' '}
                <b>On Error</b>, below the configuration, decides whether that fails the
                workflow, continues, or drops the message.
              </Text>
            )}

            <Group grow>
              <TextInput
                label="Default Value"
                placeholder="Value if not found"
                value={config.defaultValue || ''}
                onChange={(e) => updateNodeConfig(nodeId, { defaultValue: e.currentTarget.value })}
                size="sm"
                description={
                  missPolicy === 'default'
                    ? 'JSON or string written to the target field on a miss.'
                    : 'JSON or string to use as fallback. Only used by the "Write the default value" policy.'
                }
              />
              <TextInput
                label="Cache TTL"
                placeholder="e.g. 5m, 1h"
                value={config.ttl || ''}
                onChange={(e) => updateNodeConfig(nodeId, { ttl: e.currentTarget.value })}
                size="sm"
                description="How long to cache results in memory. Empty means results are cached for the lifetime of the process."
              />
            </Group>

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
              sampleMessage={incomingPayload}
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
