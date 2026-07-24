import { IconRefresh } from '@tabler/icons-react';
import { ActionIcon, Autocomplete, Group, TextInput, Switch, Stack, Divider, Select, Button, Loader, Checkbox } from '@mantine/core'
import { useState, type FC, useEffect } from 'react'
import { ColumnMappingEditor, type ColumnMapping } from './ColumnMappingEditor'
import { apiFetch } from '@/api'
import { notifications } from '@mantine/notifications'

export type PostgresSinkConfigProps = {
  type: 'postgres' | 'yugabyte'
  config: any
  tables: string[]
  discoveredDatabases: string[]
  isFetchingDBs: boolean
  loadingTables: boolean
  tablesError: string | null
  updateConfig: (key: string, value: any) => void
  fetchDatabases: () => void
  discoverTables: (force?: boolean) => void
  availableFields?: any[]
  upstreamSource?: any
}

export const PostgresSinkConfig: FC<PostgresSinkConfigProps> = ({
  type,
  config,
  tables,
  discoveredDatabases,
  isFetchingDBs,
  loadingTables,
  tablesError,
  updateConfig,
  fetchDatabases,
  discoverTables,
  availableFields = [],
  upstreamSource
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
          sink: { type, config },
          table: config.table
        })
      });
      if (res.ok) {
        const columns = await res.json();
        const newMappings: ColumnMapping[] = columns.map((col: any) => {
          // Try to find a matching field in availableFields (case-insensitive)
          // Handles both FieldInfo objects and potential CDC 'after.' prefixes.
          const field = availableFields.find(f => {
            const path = typeof f === 'string' ? f : f.path;
            if (!path) return false;
            const normalized = path.toLowerCase();
            const colLower = col.name.toLowerCase();
            return normalized === colLower || normalized === `after.${colLower}`;
          });

          return {
            source_field: (typeof field === 'object' ? field.path : field) || '',
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
        const newMappings: ColumnMapping[] = availableFields.map(f => {
          const path = typeof f === 'string' ? f : f.path;
          return {
            source_field: path,
            target_column: path.includes('.') ? path.split('.').pop() || path : path,
            is_nullable: true,
            is_identity: false
          };
        });
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

  // Auto-discover columns when use_existing_table is enabled
  useEffect(() => {
    if (config.use_existing_table === 'true' && config.table && mappings.length === 0) {
      handleSmartMap();
    }
  }, [config.use_existing_table, config.table]);

  return (
    <Stack gap="sm">
      <Group grow>
        <TextInput 
          label="Host" 
          placeholder="localhost" 
          value={config.host || ''} 
          onChange={(e) => updateConfig('host', e.target.value)} 
          required 
          description="Postgres server host"
          mih={80}
        />
        <TextInput
          label="Port"
          placeholder="5432"
          value={config.port || ''}
          onChange={(e) => updateConfig('port', e.target.value)}
          required
          description="Postgres server port"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput 
          label="User" 
          placeholder="user" 
          value={config.user || ''} 
          onChange={(e) => updateConfig('user', e.target.value)} 
          required 
          description="Database username"
          mih={80}
        />
        <TextInput 
          label="Password" 
          type="password" 
          placeholder="password" 
          value={config.password || ''} 
          onChange={(e) => updateConfig('password', e.target.value)} 
          required 
          description="Database password"
          mih={80}
        />
      </Group>
      <Group align="flex-end" gap="xs">
        <Autocomplete
          label="Database"
          placeholder="dbname"
          data={[...new Set([...(discoveredDatabases || []), config.dbname].filter(Boolean))]}
          value={config.dbname || ''}
          onChange={(val) => {
            updateConfig('dbname', val)
            if (val) discoverTables()
          }}
          required
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
      <Group align="flex-end" gap="xs">
        <Autocomplete
          label="Target Table"
          placeholder="Select or type table name"
          data={tables || []}
          value={config.table || ''}
          onChange={(val) => updateConfig('table', val || '')}
          rightSection={loadingTables ? <Loader size={16} /> : null}
          error={tablesError}
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={() => discoverTables(true)} loading={loadingTables} title="Refresh Tables">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>

      <Switch 
        label="Use existing table" 
        description="If disabled, Hermod will attempt to create the table if it doesn't exist"
        checked={config.use_existing_table === 'true'} 
        onChange={(e) => updateConfig('use_existing_table', e.currentTarget.checked ? 'true' : 'false')} 
      />

      <Group grow>
        <Switch 
          label="Truncate Table (on start)" 
          description="Truncate table on startup"
          checked={config.truncate_table === 'true'} 
          onChange={(e) => updateConfig('truncate_table', e.currentTarget.checked ? 'true' : 'false')} 
          mih={60}
        />
        <Switch 
          label="Sync Columns (on start)" 
          description="Sync columns on startup"
          checked={config.sync_columns === 'true'} 
          onChange={(e) => updateConfig('sync_columns', e.currentTarget.checked ? 'true' : 'false')} 
          mih={60}
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
              const body = JSON.stringify({ sink: { type, config }, table: config.table });
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
        sinkType={type}
      />

      {(type === 'postgres' || type === 'yugabyte') && (
        <Group grow>
          <TextInput label="SSL Mode" placeholder="disable" value={config.sslmode || ''} onChange={(e) => updateConfig('sslmode', e.target.value)} />
          <Checkbox
            mt="xl"
            label="Using PgBouncer"
            description="Transaction/Statement mode safety"
            checked={config.pgbouncer === 'true'}
            onChange={(e) => updateConfig('pgbouncer', e.target.checked ? 'true' : 'false')}
          />
        </Group>
      )}
      <TextInput
        label="OR Connection String"
        placeholder={
          type === 'postgres' || type === 'yugabyte' ? 'postgres://user:pass@host:5432/dbname' :
          (type as string) === 'mssql' ? 'sqlserver://user:pass@host:1433?database=dbname' :
          (type as string) === 'oracle' ? 'oracle://user:pass@host:1521/service' :
          'postgres://...'
        }
        value={config.connection_string || ''}
        onChange={(e) => updateConfig('connection_string', e.target.value)}
        description={type === 'postgres' ? "Add ?pgbouncer=true for transaction/statement mode." : undefined}
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
                  description="Column to mark as deleted"
                  mih={80}
                />
                <TextInput
                  label="Soft Delete Value"
                  placeholder="true"
                  value={config.soft_delete_value || ''}
                  onChange={(e) => updateConfig('soft_delete_value', e.target.value)}
                  description="Value to set on delete"
                  mih={80}
                />
              </Group>
            )}
          </Stack>
        </>
      )}
    </Stack>
  )
}


