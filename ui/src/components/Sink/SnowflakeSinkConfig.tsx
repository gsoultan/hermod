import { TextInput, Stack, Group, Text, Code, Alert, Switch, Divider, Autocomplete, Select, Button } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { useState, type FC } from 'react';
import { ColumnMappingEditor, type ColumnMapping } from './ColumnMappingEditor';
import { apiFetch } from '../../api';
import { notifications } from '@mantine/notifications';

interface SnowflakeSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
  availableFields?: string[];
  tables: string[];
  upstreamSource?: any;
}

export const SnowflakeSinkConfig: FC<SnowflakeSinkConfigProps> = ({ 
  config, updateConfig, availableFields = [], tables, upstreamSource
}) => {
  const [discoveringColumns, setDiscoveringColumns] = useState(false);
  const [discoveringSource, setDiscoveringSource] = useState(false);

  const mappings: ColumnMapping[] = (() => {
    try {
      return config.column_mappings ? JSON.parse(config.column_mappings) : [];
    } catch (e) {
      return [];
    }
  })();

  const setMappings = (newMappings: ColumnMapping[]) => {
    updateConfig('column_mappings', JSON.stringify(newMappings));
  };

  const handleSmartMap = async () => {
    if (!config.table) return;
    setDiscoveringColumns(true);
    try {
      const res = await apiFetch('/api/sinks/discover/columns', {
        method: 'POST',
        body: JSON.stringify({
          sink: { type: 'snowflake', config },
          table: config.table
        })
      });
      if (res.ok) {
        const columns = await res.json();
        const newMappings: ColumnMapping[] = columns.map((col: any) => {
          const field = availableFields.find(f => f.toLowerCase() === col.name.toLowerCase());
          return {
            source_field: field || '',
            target_column: col.name,
            data_type: col.type,
            is_primary_key: col.is_pk,
            is_nullable: col.is_nullable,
            is_identity: col.is_identity
          };
        });
        setMappings(newMappings);
      }
    } catch (e) {
      console.error('Failed to discover columns', e);
    } finally {
      setDiscoveringColumns(false);
    }
  };

  const handleSmartMapFromSource = async () => {
    if (!upstreamSource) return;
    setDiscoveringSource(true);
    try {
      const sourceTable = upstreamSource.config?.table || upstreamSource.config?.collection || '';
      if (!sourceTable) {
        const newMappings: ColumnMapping[] = availableFields.map(field => ({
          source_field: field,
          target_column: field,
          is_nullable: true,
          is_identity: false
        }));
        setMappings(newMappings);
        return;
      }

      const res = await apiFetch('/api/sources/discover/columns', {
        method: 'POST',
        body: JSON.stringify({
          source: { type: upstreamSource.type, config: upstreamSource.config },
          table: sourceTable
        })
      });

      if (res.ok) {
        const columns = await res.json();
        const newMappings: ColumnMapping[] = columns.map((col: any) => ({
          source_field: col.name,
          target_column: col.name,
          data_type: col.type,
          is_primary_key: col.is_pk,
          is_nullable: col.is_nullable,
          is_identity: col.is_identity
        }));
        setMappings(newMappings);
      }
    } catch (e) {
      console.error('Failed to discover source columns', e);
    } finally {
      setDiscoveringSource(false);
    }
  };

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="Snowflake Connection">
        <Text size="xs">
          Use the Snowflake connection string format: <Code>user:password@account/database/schema?warehouse=wh</Code>
        </Text>
      </Alert>

      <TextInput
        label="Connection String"
        placeholder="user:pass@account/db/schema?warehouse=wh"
        required
        value={config.connection_string || ''}
        onChange={(e) => updateConfig('connection_string', e.target.value)}
      />

      <Group grow>
        <TextInput
          label="Database (Override)"
          placeholder="MY_DB"
          value={config.database || ''}
          onChange={(e) => updateConfig('database', e.target.value)}
        />
        <TextInput
          label="Schema (Override)"
          placeholder="PUBLIC"
          value={config.schema || ''}
          onChange={(e) => updateConfig('schema', e.target.value)}
        />
      </Group>

      <Group grow>
        <TextInput
          label="Warehouse"
          placeholder="COMPUTE_WH"
          value={config.warehouse || ''}
          onChange={(e) => updateConfig('warehouse', e.target.value)}
        />
        <TextInput
          label="Role"
          placeholder="ACCOUNTADMIN"
          value={config.role || ''}
          onChange={(e) => updateConfig('role', e.target.value)}
        />
      </Group>

      <Autocomplete 
        label="Target Table" 
        placeholder="Select or type name" 
        data={tables || []} 
        value={config.table || ''} 
        onChange={(val: string) => updateConfig('table', val || '')} 
        required 
      />

      <Switch 
        label="Use existing table" 
        description="If disabled, Hermod will attempt to create the table if it doesn't exist"
        checked={config.use_existing_table === 'true'} 
        onChange={(e) => updateConfig('use_existing_table', e.currentTarget.checked ? 'true' : 'false')} 
      />

      <Group grow>
        <Switch 
          label="Truncate Table (on start)" 
          description="If enabled, Hermod truncates the table when the workflow starts"
          checked={config.truncate_table === 'true'} 
          onChange={(e) => updateConfig('truncate_table', e.currentTarget.checked ? 'true' : 'false')} 
        />
        <Switch 
          label="Sync Columns (on start)" 
          description="Add/Modify columns to match the mappings on startup"
          checked={config.sync_columns === 'true'} 
          onChange={(e) => updateConfig('sync_columns', e.currentTarget.checked ? 'true' : 'false')} 
        />
      </Group>

      <Group>
        <Button
          variant="light"
          color="red"
          disabled={!config.table}
          onClick={async () => {
            if (!config.table) return;
            const ok = window.confirm(`This will immediately truncate table "${config.table}". Are you sure?`);
            if (!ok) return;
            try {
              const body = JSON.stringify({ sink: { type: 'snowflake', config }, table: config.table });
              await apiFetch('/api/sinks/truncate', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
              notifications.show({ title: 'Table truncated', message: `${config.table} has been truncated.`, color: 'green' });
            } catch (e: any) {
              notifications.show({ title: 'Truncate failed', message: e?.message || 'Unknown error', color: 'red' });
            }
          }}
        >
          Truncate Table Now
        </Button>
      </Group>

      <Divider label="Processing Options" labelPosition="center" />
      <Stack gap="xs">
        <Select
          label="Operation Mode"
          description="How should Hermod treat incoming data? 'Auto' follows the source, others force a specific operation."
          data={[
            { value: 'auto', label: 'Auto (Follow Source)' },
            { value: 'insert', label: 'Force Insert' },
            { value: 'upsert', label: 'Force Upsert (Merge)' },
            { value: 'update', label: 'Force Update' },
            { value: 'delete', label: 'Force Delete' },
          ]}
          value={config.operation_mode || 'auto'}
          onChange={(val) => updateConfig('operation_mode', val || 'auto')}
        />
      </Stack>

      <Divider label="Schema & Mapping" labelPosition="center" />

      <ColumnMappingEditor 
        mappings={mappings} 
        availableFields={availableFields} 
        onChange={setMappings}
        onSmartMap={config.table ? handleSmartMap : undefined}
        onSmartMapFromSource={upstreamSource ? handleSmartMapFromSource : undefined}
        loading={discoveringColumns}
        loadingSource={discoveringSource}
        sinkType="snowflake"
      />

      {(config.operation_mode === 'auto' || config.operation_mode === 'delete' || !config.operation_mode) && (
        <>
          <Divider label="Delete Strategy" labelPosition="center" />
          <Stack gap="xs">
            <Select
              label="Delete Strategy"
              placeholder="Choose how to handle deletes"
              description={config.operation_mode === 'delete' ? "Forced delete behavior" : "Behavior when source sends a delete operation"}
              data={[
                { value: 'hard_delete', label: 'Hard Delete (Physical)' },
                { value: 'soft_delete', label: 'Soft Delete (Update column)' },
                { value: 'ignore', label: 'Ignore (Do nothing)' },
              ]}
              value={config.delete_strategy || 'hard_delete'}
              onChange={(val) => updateConfig('delete_strategy', val || 'hard_delete')}
            />
            {config.delete_strategy === 'soft_delete' && (
              <Group grow>
                <TextInput
                  label="Soft Delete Column"
                  placeholder="is_deleted"
                  value={config.soft_delete_column || ''}
                  onChange={(e) => updateConfig('soft_delete_column', e.target.value)}
                  description="Column to update when a record is deleted"
                />
                <TextInput
                  label="Soft Delete Value"
                  placeholder="true"
                  value={config.soft_delete_value || ''}
                  onChange={(e) => updateConfig('soft_delete_value', e.target.value)}
                  description="Value to set"
                />
              </Group>
            )}
          </Stack>
        </>
      )}
    </Stack>
  );
}


