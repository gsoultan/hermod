import { Stack, Group, Divider, Checkbox, NumberInput, Fieldset, Switch, Text, SimpleGrid, Radio } from '@mantine/core';
import { PasswordInput, TextInput } from '@/components/shared/ReferenceInputs';
import { PostgresReplicationConfig } from './PostgresReplicationConfig';
import { ConnectionUrlPaste } from '../../shared/ConnectionUrlPaste';

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
      {/*
        The visible text sits in a sibling Stack for layout, so it is not the
        switch's accessible name — screen readers announced this control as an
        unlabelled toggle, and it could not be targeted by its label at all.
        aria-label restates the visible text so the two agree.
      */}
      <Switch
        checked={useCDCChecked}
        onChange={(e) => updateConfig('use_cdc', e.target.checked ? 'true' : 'false')}
        aria-label="Capture Data Changes (CDC)"
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
          {type !== 'cassandra' && type !== 'scylladb' && (
            <ConnectionUrlPaste
              updateConfig={updateConfig}
              applyKeys={
                type === 'mongodb'
                  ? ['host', 'port', 'user', 'password', 'database', 'uri']
                  : ['host', 'port', 'user', 'password', 'database', 'sslmode']
              }
            />
          )}
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
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
              <TextInput label="SSL Mode" placeholder="disable" value={config.sslmode || 'disable'} onChange={(e) => updateConfig('sslmode', e.target.value)} />
              <Checkbox
                mt="xl"
                label="Using PgBouncer"
                description="Transaction/Statement mode safety"
                checked={config.pgbouncer === 'true'}
                onChange={(e) => updateConfig('pgbouncer', e.target.checked ? 'true' : 'false')}
              />
            </SimpleGrid>
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
              {type === 'oracle' && (
                <OracleCdcMethod config={config} updateConfig={updateConfig} />
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

// Oracle CDC either polls for rows above the last id seen, which sees inserts
// only, or reads inserts, updates and deletes from the redo logs with LogMiner.
// The factory reads cdc_method; empty means incremental, as before.
function OracleCdcMethod({ config, updateConfig }: { config: Record<string, any>; updateConfig: (key: string, value: any) => void }) {
  const method = config.cdc_method === 'logminer' ? 'logminer' : 'incremental';
  return (
    <Stack gap="sm">
      <Radio.Group
        label="Change capture method"
        value={method}
        onChange={(v) => updateConfig('cdc_method', v)}
      >
        <Stack gap="xs" mt="xs">
          <Radio value="incremental" label="Incremental polling" description="Polls each table for rows above the last ID seen. Sees inserts only." />
          <Radio value="logminer" label="LogMiner" description="Reads inserts, updates and deletes from the redo logs. Needs archive logging and supplemental logging." />
        </Stack>
      </Radio.Group>
      {method === 'logminer' ? (
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput
            label="Pluggable database (PDB)"
            placeholder="ORCLPDB1"
            value={config.pdb_name || ''}
            onChange={(e) => updateConfig('pdb_name', e.target.value)}
            description="Only when connected to CDB$ROOT. Leave empty when connected to the PDB itself."
            mih={80}
          />
          <TextInput
            label="Poll Interval"
            placeholder="5s"
            value={config.poll_interval || '5s'}
            onChange={(e) => updateConfig('poll_interval', e.target.value)}
            description="Delay between reads of the redo logs when idle"
            mih={80}
          />
          <Checkbox
            label="Load existing rows first"
            description="Emit the tables' current rows as snapshots before the first change"
            checked={config.initial_load === 'true'}
            onChange={(e) => updateConfig('initial_load', e.target.checked ? 'true' : 'false')}
          />
        </SimpleGrid>
      ) : (
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput
            label="Incremental ID Field"
            placeholder="ID"
            value={config.id_field || ''}
            onChange={(e) => updateConfig('id_field', e.target.value)}
            description="An increasing column such as the primary key"
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
    </Stack>
  );
}
