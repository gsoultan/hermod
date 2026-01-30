import { TextInput, Select, JsonInput } from '@mantine/core';

interface GoogleSheetsSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

export function GoogleSheetsSinkConfig({ config, updateConfig }: GoogleSheetsSinkConfigProps) {
  return (
    <>
      <TextInput 
        label="Spreadsheet ID" 
        placeholder="1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms or {{.spreadsheet_id}}" 
        value={config.spreadsheet_id || ''} 
        onChange={(e) => updateConfig('spreadsheet_id', e.target.value)} 
        required 
        description="Supports Go template syntax."
      />
      <TextInput 
        label="Range" 
        placeholder="Sheet1!A1:Z or {{.sheet_range}}" 
        value={config.range || ''} 
        onChange={(e) => updateConfig('range', e.target.value)} 
        required 
        description="A1 notation. Supports Go template syntax."
      />
      <Select 
        label="Operation" 
        placeholder="Select operation" 
        data={[
          { value: 'insert_row', label: 'Insert Row' },
          { value: 'insert_column', label: 'Insert Column' },
          { value: 'delete_row', label: 'Delete Row' },
          { value: 'delete_column', label: 'Delete Column' },
          { value: 'append_row', label: 'Append Row (Add to end)' }
        ]} 
        value={config.operation || 'append_row'} 
        onChange={(val) => updateConfig('operation', val || 'append_row')} 
        required 
      />
      <JsonInput
        label="Credentials JSON"
        placeholder='{ "type": "service_account", ... }'
        value={config.credentials_json || ''}
        onChange={(val: string) => updateConfig('credentials_json', val)}
        required
        minRows={5}
        formatOnBlur
      />
      {(config.operation === 'insert_row' || config.operation === 'delete_row') && (
        <TextInput 
          label="Row Index" 
          placeholder="0 or {{.row_idx}}" 
          value={config.row_index || ''} 
          onChange={(e) => updateConfig('row_index', e.target.value)} 
          description="Zero-based index. Supports Go template syntax."
        />
      )}
      {(config.operation === 'insert_column' || config.operation === 'delete_column') && (
        <TextInput 
          label="Column Index" 
          placeholder="0 or {{.col_idx}}" 
          value={config.column_index || ''} 
          onChange={(e) => updateConfig('column_index', e.target.value)} 
          description="Zero-based index. Supports Go template syntax."
        />
      )}
    </>
  );
}
