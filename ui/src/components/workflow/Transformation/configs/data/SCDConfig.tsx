import { Select, Stack, TextInput } from '@mantine/core';

interface SCDConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  sources?: any[];
}

export function SCDConfig({ config, updateNodeConfig, nodeId, sources }: SCDConfigProps) {
  return (

    <Stack gap="xs">
      <Select
        label="Target Source"
        data={(Array.isArray(sources) ? sources : [])
          .filter(s => ['postgres', 'mysql', 'mariadb', 'sqlite', 'mssql', 'sqlserver'].includes(s.type))
          .map((s: any) => ({ value: s.id, label: s.name }))}
        value={config.targetSourceId || ''}
        onChange={(val: string | null) => updateNodeConfig(nodeId, { targetSourceId: val || '' })}
        placeholder="Select a database source"
        required
      />
      <TextInput
        label="Target Table"
        placeholder="e.g. dim_users"
        value={config.targetTable || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { targetTable: e.currentTarget.value })}
        required
      />
      <Select
        label="SCD Type"
        data={[
          { value: '0', label: 'Type 0 (Fixed/Retain Original)' },
          { value: '1', label: 'Type 1 (Overwrite)' },
          { value: '2', label: 'Type 2 (History/Add Row)' },
          { value: '3', label: 'Type 3 (Previous Value Column)' },
          { value: '4', label: 'Type 4 (History Table)' },
          { value: '6', label: 'Type 6 (Hybrid 1+2)' },
        ]}
        value={config.type || '1'}
        onChange={(val: string | null) => updateNodeConfig(nodeId, { type: val || '1' })}
      />
      <TextInput
        label="Business Keys (Comma separated)"
        placeholder="id,email"
        value={config.keys || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { keys: e.currentTarget.value })}
        required
      />
      <TextInput
        label="Monitored Columns (Comma separated)"
        placeholder="name,address,phone"
        value={config.columns || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { columns: e.currentTarget.value })}
        description="Columns to check for changes"
      />
      {config.type === '3' && (
        <TextInput
          label="Column Mappings"
          placeholder="current:previous,email:old_email"
          value={config.mappings || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { mappings: e.currentTarget.value })}
          description="Mapping of current columns to their historical counterparts"
        />
      )}
      {config.type === '4' && (
        <TextInput
          label="History Table"
          placeholder="e.g. dim_users_history"
          value={config.historyTable || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { historyTable: e.currentTarget.value })}
          required
        />
      )}
      {config.type === '6' && (
        <>
          <TextInput
            label="Type 1 Columns (Overwrite)"
            placeholder="email,phone"
            value={config.type1Columns || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { type1Columns: e.currentTarget.value })}
            description="Columns that should be overwritten in all history rows"
          />
          <TextInput
            label="Type 2 Columns (Add Row)"
            placeholder="address,department"
            value={config.type2Columns || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { type2Columns: e.currentTarget.value })}
            description="Columns that trigger a new history row"
          />
        </>
      )}
      {(config.type === '2' || config.type === '6') && (
        <>
          <TextInput
            label="Start Date Column"
            placeholder="start_date"
            value={config.startDateColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { startDateColumn: e.currentTarget.value })}
          />
          <TextInput
            label="End Date Column"
            placeholder="end_date"
            value={config.endDateColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { endDateColumn: e.currentTarget.value })}
          />
          <TextInput
            label="Current Flag Column (Optional)"
            placeholder="is_current"
            value={config.currentFlagColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { currentFlagColumn: e.currentTarget.value })}
          />
        </>
      )}
    </Stack>
  
  );
}
