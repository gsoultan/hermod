import { TextInput, Group, Select, ActionIcon, Title, Stack, Badge, Autocomplete } from '@mantine/core';import { IconRefresh } from '@tabler/icons-react';
interface DatabaseSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: string) => void;
  tables: string[];
  loadingTables?: boolean;
  discoverTables?: () => void;
  discoveredDatabases?: string[];
  isFetchingDBs?: boolean;
  fetchDatabases?: () => void;
}

export function DatabaseSinkConfig({ 
  type, config, updateConfig, tables, loadingTables, discoverTables, 
  discoveredDatabases, isFetchingDBs, fetchDatabases 
}: DatabaseSinkConfigProps) {
  const dbTypes = ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'];
  if (!dbTypes.includes(type)) return null;

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Title order={5}>{type === 'mariadb' ? 'MariaDB' : (type === 'mssql' ? 'SQL Server' : type.charAt(0).toUpperCase() + type.slice(1))} Sink</Title>
        <Badge variant="light" color="blue">{type}</Badge>
      </Group>

      {type === 'sqlite' ? (
        <TextInput 
          label="Database Path" 
          placeholder="hermod.db" 
          value={config.db_path || ''} 
          onChange={(e) => updateConfig('db_path', e.target.value)} 
          required 
        />
      ) : type === 'mongodb' ? (
        <>
          <TextInput label="Connection URI" placeholder="mongodb://localhost:27017" value={config.uri || ''} onChange={(e) => updateConfig('uri', e.target.value)} required />
          <TextInput label="Database" placeholder="my_db" value={config.database || ''} onChange={(e) => updateConfig('database', e.target.value)} required />
          <TextInput label="Collection" placeholder="my_collection" value={config.collection || ''} onChange={(e) => updateConfig('collection', e.target.value)} required />
        </>
      ) : type === 'cassandra' ? (
         <>
          <TextInput label="Hosts" placeholder="localhost:9042" value={config.hosts || ''} onChange={(e) => updateConfig('hosts', e.target.value)} required />
          <TextInput label="Keyspace" placeholder="my_keyspace" value={config.keyspace || ''} onChange={(e) => updateConfig('keyspace', e.target.value)} required />
          <TextInput label="Table" placeholder="my_table" value={config.table || ''} onChange={(e) => updateConfig('table', e.target.value)} required />
          <Group grow>
            <TextInput label="Username" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
        </>
      ) : (
        <>
          <TextInput 
            label="Connection String" 
            placeholder={type === 'postgres' ? 'postgres://user:pass@localhost:5432/db' : 'user:pass@tcp(localhost:3306)/db'} 
            value={config.connection_string || ''} 
            onChange={(e) => updateConfig('connection_string', e.target.value)} 
            description="Directly use a DSN for maximum flexibility"
          />
          <Group grow>
            <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} />
            <TextInput label="Port" placeholder="3306" value={config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="User" placeholder="root" value={config.user || ''} onChange={(e) => updateConfig('user', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="secret" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <Group align="flex-end">
            <Autocomplete 
              label="Database" 
              placeholder="Select or enter name" 
              data={discoveredDatabases || []} 
              value={config.dbname || ''} 
              onChange={(val) => updateConfig('dbname', val || '')} 
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
      )}

      {type !== 'cassandra' && type !== 'mongodb' && (
        <Group align="flex-end">
          <Select 
            label="Target Table" 
            placeholder="Select a table" 
            data={tables || []} 
            value={config.table || ''} 
            onChange={(val) => updateConfig('table', val || '')} 
            required 
            style={{ flex: 1 }}
            searchable
            clearable
          />
          {discoverTables && (
            <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={discoverTables} loading={loadingTables}>
              <IconRefresh size="1.2rem" />
            </ActionIcon>
          )}
        </Group>
      )}
    </Stack>
  );
}


