import { TextInput, Stack, Group, Divider, Checkbox, PasswordInput, NumberInput, Fieldset, Switch, Text, SimpleGrid } from '@mantine/core';
import { PostgresReplicationConfig } from './PostgresReplicationConfig';

interface DatabaseSourceConfigProps {
  type: string;
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  tablesInput?: React.ReactNode;
  databaseInput?: React.ReactNode;
}

export function DatabaseSourceConfig({ type, config, updateConfig, tablesInput, databaseInput }: DatabaseSourceConfigProps) {
  const useCDCChecked = config.use_cdc !== 'false';
  
  const cdcSwitch = (
    <Group justify="space-between" mb="sm">
      <Stack gap={0}>
        <Text size="sm" fw={500}>Capture Data Changes (CDC)</Text>
        <Text size="xs" c="dimmed">Enable real-time change tracking vs periodic polling</Text>
      </Stack>
      <Switch 
        checked={useCDCChecked}
        onChange={(e) => updateConfig('use_cdc', e.target.checked ? 'true' : 'false')}
      />
    </Group>
  );

  if (type === 'sqlite') {
    return (
      <Stack gap="md">
        {cdcSwitch}
        <TextInput 
          label="Database File Path" 
          placeholder="hermod.db" 
          value={config.path || config.connection_string || ''} 
          onChange={(e) => updateConfig('path', e.target.value)} 
          required 
        />
        {tablesInput}
        <Divider label="Initial Snapshot" labelPosition="center" />
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <NumberInput 
            label="Batch Size" 
            value={parseInt(config.snapshot_batch_size) || 1000} 
            onChange={(val) => updateConfig('snapshot_batch_size', val.toString())} 
            description="Number of records per batch"
            mih={80}
          />
          <NumberInput 
            label="Parallelism" 
            value={parseInt(config.snapshot_parallelism) || 1} 
            onChange={(val) => updateConfig('snapshot_parallelism', val.toString())} 
            description="Number of parallel threads"
            mih={80}
          />
        </SimpleGrid>
      </Stack>
    );
  }

  return (
    <Stack gap="md">
      {cdcSwitch}
      
      <Fieldset legend="Connection Details">
        <Stack gap="sm">
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            {type === 'cassandra' || type === 'scylladb' ? (
              <TextInput 
                label="Hosts" 
                placeholder="localhost:9042, localhost:9043" 
                value={config.hosts || ''} 
                onChange={(e) => updateConfig('hosts', e.target.value)} 
                description="Comma-separated contact points"
                required 
                mih={80}
              />
            ) : (
              <>
                <TextInput 
                  label="Host" 
                  placeholder="localhost" 
                  value={config.host || ''} 
                  onChange={(e) => updateConfig('host', e.target.value)} 
                  required 
                  description="Database server host"
                  mih={80}
                />
                <TextInput 
                  label="Port" 
                  placeholder="5432" 
                  value={config.port || ''} 
                  onChange={(e) => updateConfig('port', e.target.value)} 
                  description="Database port number"
                  mih={80}
                />
              </>
            )}
          </SimpleGrid>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <TextInput 
              label="User" 
              placeholder="user" 
              value={config.user || ''} 
              onChange={(e) => updateConfig('user', e.target.value)} 
              description="Database username"
              mih={80}
            />
            <PasswordInput 
              label="Password" 
              placeholder="password" 
              value={config.password || ''} 
              onChange={(e) => updateConfig('password', e.target.value)} 
              description="Database password"
              mih={80}
            />
          </SimpleGrid>
          {type === 'mongodb' ? (
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
              <TextInput 
                label="Database" 
                placeholder="my-db" 
                value={config.database || ''} 
                onChange={(e) => updateConfig('database', e.target.value)} 
                required 
                description="Target database name"
                mih={80}
              />
              <TextInput 
                label="Collection" 
                placeholder="my-collection" 
                value={config.collection || ''} 
                onChange={(e) => updateConfig('collection', e.target.value)} 
                required 
                description="Target collection name"
                mih={80}
              />
            </SimpleGrid>
          ) : databaseInput}
          
          {type === 'mongodb' && (
             <TextInput 
              label="OR Connection URI (Overrides individual fields)" 
              placeholder="mongodb://..." 
              value={config.uri || ''}
              onChange={(e) => updateConfig('uri', e.target.value)}
            />
          )}
          
          {type === 'postgres' && (
            <TextInput label="SSL Mode" placeholder="disable" value={config.sslmode || 'disable'} onChange={(e) => updateConfig('sslmode', e.target.value)} />
          )}
        </Stack>
      </Fieldset>

      <Fieldset legend="Tracking & Performance">
        <Stack gap="sm">
          {tablesInput}
          
          {useCDCChecked ? (
            <>
              {type === 'postgres' && (
                <PostgresReplicationConfig type={type} config={config} updateConfig={updateConfig} />
              )}
              {type === 'mssql' && (
                <Checkbox 
                  label="Auto Enable CDC" 
                  checked={config.auto_enable_cdc !== 'false'} 
                  onChange={(e) => updateConfig('auto_enable_cdc', e.target.checked ? 'true' : 'false')} 
                  description="Automatically enable CDC on database/tables if not set"
                />
              )}
            </>
          ) : (
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
              <TextInput 
                label="Incremental ID Field" 
                placeholder="id" 
                value={config.id_field || ''} 
                onChange={(e) => updateConfig('id_field', e.target.value)} 
                description="Primary key or auto-inc field"
                mih={80}
              />
              <TextInput 
                label="Poll Interval" 
                placeholder="5s" 
                value={config.poll_interval || '5s'} 
                onChange={(e) => updateConfig('poll_interval', e.target.value)} 
                description="Delay between polls"
                mih={80}
              />
            </SimpleGrid>
          )}

          <Divider label="Initial Snapshot" labelPosition="center" />
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <NumberInput 
              label="Batch Size" 
              value={parseInt(config.snapshot_batch_size) || 1000} 
              onChange={(val) => updateConfig('snapshot_batch_size', val.toString())} 
              description="Records per batch"
              mih={80}
            />
            <NumberInput 
              label="Parallelism" 
              value={parseInt(config.snapshot_parallelism) || 1} 
              onChange={(val) => updateConfig('snapshot_parallelism', val.toString())} 
              description="Parallel fetch threads"
              mih={80}
            />
          </SimpleGrid>
        </Stack>
      </Fieldset>
    </Stack>
  );
}
