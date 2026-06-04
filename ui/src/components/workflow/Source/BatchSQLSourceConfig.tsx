import { TextInput, Stack, Select, Text, Divider, Button, ActionIcon, Group, Paper, Badge, Modal } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { CronInput } from '../../shared/CronInput';
import { SQLQueryBuilder } from '../../forms/SQLQueryBuilder';
import { IconPlus, IconTrash, IconDatabase } from '@tabler/icons-react';
import { useState } from 'react';
import type { Source } from '@/types';

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
    } catch (e) {
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

  return (
    <Stack gap="md">
      <Select 
        label="Database Source" 
        placeholder="Select source to run queries against"
        data={allSources
          .filter((s: Source) => ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'sqlite', 'clickhouse'].includes(s.type))
          .map((s: Source) => ({ value: s.id, label: `${s.name} (${s.type})` }))}
        value={config.source_id}
        onChange={(val) => updateConfig('source_id', val || '')}
        required
      />
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
                  <ActionIcon color="red" variant="subtle" size="sm" onClick={() => removeQuery(i)}>
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
