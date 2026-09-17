import { TextInput, Textarea, Stack, Select, Text, Divider, Button, ActionIcon, Group, Paper, Badge, Modal, Alert } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { CronInput } from '../../shared/CronInput';
import { SQLQueryBuilder } from '../../forms/SQLQueryBuilder';
import { IconPlus, IconTrash, IconDatabase, IconInfoCircle } from '@tabler/icons-react';
import { useState } from 'react';
import type { Source } from '@/types';
import { sourceAllowsDirectQueries } from '@/lib/sourceCdc';

interface BatchSQLSourceConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  allSources: Source[];
}

export function BatchSQLSourceConfig({ config, updateConfig, allSources }: BatchSQLSourceConfigProps) {
  const [opened, { open, close }] = useDisclosure(false);
  const [currentQuery, setCurrentQuery] = useState('');
  
  const queries = (() => {
    try {
      const q = typeof config.queries === 'string' ? JSON.parse(config.queries) : config.queries;
      return Array.isArray(q) ? q : [config.queries].filter(Boolean);
    } catch {
      return [config.queries].filter(Boolean);
    }
  })();

  const setQueries = (newQueries: string[]) => {
    updateConfig('queries', JSON.stringify(newQueries));
  };

  const addQuery = (q: string) => {
    if (!q.trim()) return;
    setQueries([...queries, q.trim()]);
    setCurrentQuery('');
  };

  const removeQuery = (index: number) => {
    const newQueries = [...queries];
    newQueries.splice(index, 1);
    setQueries(newQueries);
  };

  const selectedSource = allSources.find((s: Source) => s.id === config.source_id);

  // A batch_sql source borrows this source's database and runs whole queries
  // over it on a cron, so the engine refuses to build one whose delegate is
  // serving change data capture (Registry.requireNonCDCDelegate). Beyond the
  // load, a delegate that is also a source node delivers every row twice --
  // once streamed, once batched. lib/sourceCdc holds the rule so the picker
  // cannot drift from the backend's reading of use_cdc.
  const delegateIsCDC = !!selectedSource && !sourceAllowsDirectQueries(selectedSource);

  // The backend refuses to run a query whose parameters blob will not decode,
  // rather than reading it as "no parameters" and dropping every filter
  // (batchsql.parameters). Saying so here is cheaper than finding out on the
  // next cron tick.
  const parametersError = (() => {
    const raw = (config.parameters || '').trim();
    if (!raw) return undefined;
    try {
      const parsed = JSON.parse(raw);
      if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return 'Parameters must be a valid JSON object of name/value pairs.';
      }
    } catch {
      return 'Parameters must be a valid JSON object of name/value pairs.';
    }
    return undefined;
  })();

  return (
    <Stack gap="md">
      <Select
        label="Database Source"
        placeholder="Select source to run queries against"
        data={allSources
          .filter((s: Source) => ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'sqlite', 'clickhouse'].includes(s.type))
          // Disabled rather than filtered out: an entry that simply vanishes
          // reads as a missing source, and nothing says CDC is the reason.
          .map((s: Source) =>
            sourceAllowsDirectQueries(s)
              ? { value: s.id, label: `${s.name} (${s.type})` }
              : { value: s.id, label: `${s.name} (${s.type}) — CDC enabled, not available for batch queries`, disabled: true }
          )}
        value={config.source_id}
        onChange={(val) => updateConfig('source_id', val || '')}
        required
        error={delegateIsCDC ? 'CDC is enabled on this source.' : undefined}
      />

      {delegateIsCDC && (
        <Alert
          data-testid="batch-sql-source-cdc-error"
          icon={<IconInfoCircle size={18} />}
          color="red"
          variant="light"
          radius="md"
        >
          <Text size="sm">
            <strong>{selectedSource?.name}</strong> has CDC enabled, so this batch source will not
            start: scheduled queries have to run against a non-CDC source. Turn CDC off on that
            source, or register a second, non-CDC source for the same database and point this one at
            it. If the same table is also streamed by a CDC source node, batching it here would
            deliver every row twice.
          </Text>
        </Alert>
      )}

      <CronInput
        label="Cron Schedule" 
        placeholder="*/5 * * * *" 
        value={config.cron} 
        onChange={(val) => updateConfig('cron', val)} 
        required
        description="Standard cron expression (e.g. */5 * * * * for every 5 minutes)"
      />
      <TextInput
        label="Incremental Column"
        placeholder="id or created_at"
        value={config.incremental_column}
        onChange={(e) => updateConfig('incremental_column', e.target.value)}
        description="Column used to track progress between runs"
      />

      <Textarea
        label="Query Parameters"
        placeholder={'{"ids": ["a1", "b2"], "status": "active"}'}
        value={config.parameters || ''}
        onChange={(e) => updateConfig('parameters', e.currentTarget.value)}
        error={parametersError}
        autosize
        minRows={2}
        styles={{ input: { fontFamily: 'monospace' } }}
        description={
          'A JSON object of named values bound into the queries above as {{.name}}. A batch source has no inbound message, so this is where its variables come from. A list expands inside an IN list: id IN ({{.ids}}). {{.last_value}} is the incremental watermark and needs no entry here.'
        }
      />

      <Divider label="Query Management" labelPosition="center" />
      
      <Stack gap="xs">
        <Text size="sm" fw={500}>Active Queries ({queries.length})</Text>
        {queries.length === 0 ? (
          <Text size="xs" c="dimmed" fs="italic">No queries added yet. Use the builder below to create and add queries.</Text>
        ) : (
          <Stack gap="xs">
            {queries.map((q, i) => (
              <Paper key={i} withBorder p="xs" radius="sm">
                <Group justify="space-between" align="flex-start" wrap="nowrap">
                  <Text size="xs" style={{ fontFamily: 'monospace', wordBreak: 'break-all', flex: 1 }}>{q}</Text>
                  <ActionIcon aria-label={`Remove query ${i + 1}`} color="red" variant="subtle" size="sm" onClick={() => removeQuery(i)}>
                    <IconTrash size={14} />
                  </ActionIcon>
                </Group>
              </Paper>
            ))}
          </Stack>
        )}
      </Stack>

      {config.source_id ? (
        <Stack gap="xs">
          <Button 
            leftSection={<IconDatabase size={16} />}
            onClick={open}
            variant="light"
            fullWidth
          >
            Open SQL Query Builder
          </Button>
          
          <Modal 
            opened={opened} 
            onClose={close} 
            title="SQL Query Builder" 
            size="80%"
            radius="md"
          >
            <Stack gap="md">
              <SQLQueryBuilder 
                type="source" 
                sourceType={selectedSource?.type}
                config={selectedSource?.config || {}} 
                initialQuery={currentQuery}
                onQueryChange={setCurrentQuery}
                availableFields={[{ path: 'last_value', type: 'any' }]}
              />
              <Group justify="flex-end">
                <Button variant="subtle" onClick={close}>Cancel</Button>
                <Button 
                  leftSection={<IconPlus size={16} />}
                  onClick={() => {
                    addQuery(currentQuery);
                    close();
                  }}
                  disabled={!currentQuery.trim()}
                >
                  Add to Batch
                </Button>
              </Group>
            </Stack>
          </Modal>

          <Text size="xs" c="dimmed">
            Use <Badge size="xs" variant="outline">{"{{.last_value}}"}</Badge> in your query to reference the incremental column's last value.
          </Text>
        </Stack>
      ) : (
        <Text size="xs" c="orange">Select a database source to enable the query builder.</Text>
      )}
    </Stack>
  );
}
