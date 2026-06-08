import { Autocomplete, Loader, TagsInput, Alert, Stack, Text, Group } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { DatabaseSourceConfig } from './DatabaseSourceConfig';
import { SocialSourceConfig } from './SocialSourceConfig';
import { MessagingSourceConfig } from './MessagingSourceConfig';
import { FileSourceConfig } from './FileSourceConfig';
import { SapSourceConfig } from './SapSourceConfig';
import { Dynamics365SourceConfig } from './Dynamics365SourceConfig';
import { MainframeSourceConfig } from './MainframeSourceConfig';
import { BatchSQLSourceConfig } from './BatchSQLSourceConfig';
import { OtherSourceConfig } from './OtherSourceConfig';
import { ExcelSourceConfig } from './ExcelSourceConfig';
import type { FC } from 'react';
import type { Source } from '@/types';

interface SourceConfigFieldsProps {
  source: Source;
  updateConfig: (key: string, value: any) => void;
  discoveredTables: string[];
  discoveredDatabases: string[];
  isFetchingTables: boolean;
  isFetchingDBs: boolean;
  fetchTables: (dbName?: string) => void;
  fetchDatabases: () => void;
  handleFileUpload: (file: File | null) => void;
  uploading: boolean;
  allSources: Source[];
}

export const SourceConfigFields: FC<SourceConfigFieldsProps> = ({
  source,
  updateConfig,
  discoveredTables,
  discoveredDatabases,
  isFetchingTables,
  isFetchingDBs,
  fetchTables,
  fetchDatabases,
  handleFileUpload,
  uploading,
  allSources
}) => {
  const isDatabaseSource = (type: string) => {
    return ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'yugabyte', 'mariadb', 'db2', 'cassandra', 'scylladb', 'clickhouse', 'sqlite', 'eventstore'].includes(type);
  };

  if (isDatabaseSource(source.type)) {
    const tablesValue = (source.config?.tables || '')
      .split(',')
      .map((t: string) => t.trim())
      .filter((t: string) => t.length > 0);

    const useCDC = source.config?.use_cdc !== 'false';
    const showCDCWarning = source.type === 'postgres' && useCDC;

    const tablesInput = (
      <Stack gap="xs">
        <TagsInput
          label="Tables"
          placeholder="Type a table name and press Enter"
          data={discoveredTables || []}
          value={tablesValue}
          onChange={(vals) => updateConfig('tables', vals.map((t) => t.trim()).filter((t) => t.length > 0).join(','))}
          description="Specify one or more tables to monitor for changes."
          clearable
          rightSection={isFetchingTables ? <Loader size="xs" /> : null}
          onDropdownOpen={() => fetchTables()}
        />
        {showCDCWarning && (
          <Alert color="orange" variant="light" py="xs">
            <Group gap="xs">
              <IconInfoCircle size={16} />
              <Text size="xs">
                {tablesValue.length === 0 
                  ? "No tables selected. For PostgreSQL, this defaults to 'ALL TABLES' unless previously configured with specific tables."
                  : "Updating this list will synchronize the database publication. Removing all tables will delete the replication slot and publication."}
              </Text>
            </Group>
          </Alert>
        )}
      </Stack>
    );

    const databaseInput = (
      <Autocomplete
        label="Database Name"
        placeholder="postgres"
        data={discoveredDatabases || []}
        value={source.config?.dbname || ''}
        onChange={(val) => updateConfig('dbname', val)}
        required
        rightSection={isFetchingDBs ? <Loader size="xs" /> : null}
        onDropdownOpen={() => fetchDatabases()}
      />
    );

    return (
      <DatabaseSourceConfig 
        type={source.type}
        config={source.config}
        updateConfig={updateConfig}
        tablesInput={tablesInput}
        databaseInput={databaseInput}
      />
    );
  }

  if (['discord', 'slack', 'twitter', 'facebook', 'instagram', 'linkedin', 'tiktok'].includes(source.type)) {
    return <SocialSourceConfig type={source.type} config={source.config} updateConfig={updateConfig} />;
  }

  if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'mqtt'].includes(source.type)) {
    return <MessagingSourceConfig type={source.type} config={source.config} updateConfig={updateConfig} />;
  }

  if (source.type === 'file') {
    return (
      <FileSourceConfig 
        config={source.config} 
        updateConfig={updateConfig} 
        handleFileUpload={handleFileUpload} 
        uploading={uploading} 
      />
    );
  }

  if (source.type === 'excel') {
    return (
      <ExcelSourceConfig 
        config={source.config}
        updateConfig={updateConfig}
        handleFileUpload={handleFileUpload}
        uploading={uploading}
      />
    );
  }

  if (source.type === 'sap') {
    return <SapSourceConfig config={source.config} updateConfig={updateConfig} />;
  }

  if (source.type === 'dynamics365') {
    return <Dynamics365SourceConfig config={source.config} updateConfig={updateConfig} />;
  }

  if (source.type === 'mainframe') {
    return <MainframeSourceConfig config={source.config} updateConfig={updateConfig} />;
  }

  if (source.type === 'batch_sql') {
    return (
      <BatchSQLSourceConfig 
        config={source.config} 
        updateConfig={updateConfig} 
        allSources={allSources} 
      />
    );
  }

  return (
    <OtherSourceConfig 
      config={source.config} 
      updateConfig={updateConfig} 
      sourceType={source.type} 
    />
  );
};
