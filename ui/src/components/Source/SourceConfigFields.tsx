import { Autocomplete, Loader } from '@mantine/core';
import { DatabaseSourceConfig } from './DatabaseSourceConfig';
import { SocialSourceConfig } from './SocialSourceConfig';
import { MessagingSourceConfig } from './MessagingSourceConfig';
import { FileSourceConfig } from './FileSourceConfig';
import { SapSourceConfig } from './SapSourceConfig';
import { Dynamics365SourceConfig } from './Dynamics365SourceConfig';
import { MainframeSourceConfig } from './MainframeSourceConfig';
import { OtherSourceConfig } from './OtherSourceConfig';
import { ExcelSourceConfig } from './ExcelSourceConfig';
import type { FC } from 'react';
import type { Source } from '../../types';

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
    const tablesInput = (
      <Autocomplete
        label="Tables (Comma separated)"
        placeholder="users, orders"
        data={discoveredTables || []}
        value={source.config?.tables || ''}
        onChange={(val) => updateConfig('tables', val)}
        description="Specify which tables to monitor for changes."
        required
        rightSection={isFetchingTables ? <Loader size="xs" /> : null}
        onDropdownOpen={() => fetchTables()}
      />
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

  return (
    <OtherSourceConfig 
      config={source.config} 
      updateConfig={updateConfig} 
      sourceType={source.type} 
      allSources={allSources} 
    />
  );
};
