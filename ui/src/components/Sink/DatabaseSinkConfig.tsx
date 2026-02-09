import { TextInput, Group, ActionIcon, Title, Stack, Badge, Autocomplete, Switch, Divider, SimpleGrid, Select, Button } from '@mantine/core';
import { IconRefresh, IconInfoCircle } from '@tabler/icons-react';
import { useState, type FC } from 'react';
import { ColumnMappingEditor, type ColumnMapping } from './ColumnMappingEditor';
import { apiFetch } from '../../api';
import { notifications } from '@mantine/notifications';

interface DatabaseSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: any) => void;
  tables: string[];
  loadingTables?: boolean;
  discoverTables?: () => void;
  discoveredDatabases?: string[];
  isFetchingDBs?: boolean;
  fetchDatabases?: () => void;
  availableFields?: string[];
  tablesError?: string | null;
  upstreamSource?: any;
}

export const DatabaseSinkConfig: FC<DatabaseSinkConfigProps> = ({ 
  type, config, updateConfig, tables, loadingTables, discoverTables, 
  discoveredDatabases, isFetchingDBs, fetchDatabases, availableFields = [],
  tablesError, upstreamSource
}) => {
  const [discoveringColumns, setDiscoveringColumns] = useState(false);
  const [discoveringSource, setDiscoveringSource] = useState(false);

  const dbTypes = ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'];
  if (!dbTypes.includes(type)) return null;

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
      // Find table name from upstream source config
      const sourceTable = upstreamSource.config?.table || upstreamSource.config?.collection || '';
      if (!sourceTable) {
        // Fallback: Map available fields directly
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

  const renderConnectionFields = () => {
    if (type === 'sqlite') {
      return (
        <TextInput 
          label="Database Path" 
          placeholder="hermod.db" 
          value={config.db_path || config.connection_string || ''} 
          onChange={(e) => updateConfig('db_path', e.target.value)} 
          required 
        />
      );
    }

    if (type === 'mongodb') {
      return (
        <>
          <TextInput label="Connection URI" placeholder="mongodb://localhost:27017" value={config.uri || ''} onChange={(e) => updateConfig('uri', e.target.value)} required />
          <TextInput label="Database" placeholder="my_db" value={config.database || ''} onChange={(e) => updateConfig('database', e.target.value)} required />
        </>
      );
    }

    if (type === 'cassandra') {
      return (
        <>
          <TextInput label="Hosts" placeholder="localhost:9042" value={config.hosts || ''} onChange={(e) => updateConfig('hosts', e.target.value)} required />
          <TextInput label="Keyspace" placeholder="my_keyspace" value={config.keyspace || ''} onChange={(e) => updateConfig('keyspace', e.target.value)} required />
        </>
      );
    }

    if (type === 'clickhouse') {
      return (
        <>
          <TextInput label="Address" placeholder="localhost:9000" value={config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
          <Group align="flex-end" gap="xs">
            <Autocomplete 
              label="Database" 
              placeholder="default" 
              data={[...new Set([...(discoveredDatabases || []), config.database].filter(Boolean))]} 
              value={config.database || ''} 
              onChange={(val) => {
                updateConfig('database', val);
                if (val && discoverTables) discoverTables();
              }} 
              required 
              style={{ flex: 1 }}
            />
            {fetchDatabases && (
              <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={fetchDatabases} loading={isFetchingDBs}>
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            )}
          </Group>
        </>
      );
    }

    return (
      <>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} />
          <TextInput 
            label="Port" 
            placeholder={type === 'mysql' || type === 'mariadb' ? '3306' : (type === 'mssql' ? '1433' : '5432')} 
            value={config.port || ''} 
            onChange={(e) => updateConfig('port', e.target.value)} 
          />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="User" placeholder="root" value={config.user || ''} onChange={(e) => updateConfig('user', e.target.value)} />
          <TextInput label="Password" type="password" placeholder="secret" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
        </SimpleGrid>
        <Group align="flex-end" gap="xs">
          <Autocomplete 
            label="Database" 
            placeholder="Select or enter name" 
            data={[...new Set([...(discoveredDatabases || []), config.dbname].filter(Boolean))]} 
            value={config.dbname || ''} 
            onChange={(val) => {
              updateConfig('dbname', val || '');
              if (val && discoverTables) discoverTables();
            }} 
            required 
            style={{ flex: 1 }}
          />
          {fetchDatabases && (
            <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={fetchDatabases} loading={isFetchingDBs}>
              <IconRefresh size="1.2rem" />
            </ActionIcon>
          )}
        </Group>
        <TextInput 
          label="OR Connection String" 
          placeholder="Directly use a DSN for maximum flexibility" 
          value={config.connection_string || ''} 
          onChange={(e) => updateConfig('connection_string', e.target.value)} 
        />
      </>
    );
  };

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Title order={5}>{type === 'mariadb' ? 'MariaDB' : (type === 'mssql' ? 'SQL Server' : type.charAt(0).toUpperCase() + type.slice(1))} Sink</Title>
        <Badge variant="light" color="blue">{type}</Badge>
      </Group>

      {renderConnectionFields()}

      <Group align="flex-end" gap="xs">
        <Autocomplete 
          label={type === 'mongodb' ? 'Target Collection' : 'Target Table'} 
          placeholder="Select or type name" 
          data={tables || []} 
          value={config.table || config.collection || ''} 
          onChange={(val) => {
            updateConfig('table', val || '');
            if (type === 'mongodb') updateConfig('collection', val || '');
          }} 
          required 
          style={{ flex: 1 }}
          rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
          error={tablesError}
        />
        {discoverTables && (
          <ActionIcon aria-label="Refresh" variant="light" size="lg" onClick={discoverTables} loading={loadingTables}>
            <IconRefresh size="1.2rem" />
          </ActionIcon>
        )}
      </Group>

      {type !== 'mongodb' && (
        <>
          <Switch 
            label="Use existing table" 
            description="If disabled, Hermod will attempt to create the table if it doesn't exist"
            checked={config.use_existing_table === 'true'} 
            onChange={(e) => updateConfig('use_existing_table', e.currentTarget.checked ? 'true' : 'false')} 
          />

          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
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
          </SimpleGrid>

          <Group justify="flex-start">
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
        </>
      )}
    </Stack>
  );
}


