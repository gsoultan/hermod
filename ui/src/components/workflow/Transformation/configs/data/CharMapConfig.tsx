import { Autocomplete, Select, Stack } from '@mantine/core';

interface CharMapConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

export function CharMapConfig({ config, updateNodeConfig, nodeId, fieldPaths }: CharMapConfigProps) {
  return (

    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="user.name"
        data={fieldPaths || []}
        value={config.field || ''}
        onChange={(val) => updateNodeConfig(nodeId, { field: val })}
        required
      />
      <Select
        label="Operation"
        data={[
          { value: 'uppercase', label: 'UPPERCASE' },
          { value: 'lowercase', label: 'lowercase' },
          { value: 'trim', label: 'Trim whitespace' },
          { value: 'trim_left', label: 'Trim Left' },
          { value: 'trim_right', label: 'Trim Right' },
        ]}
        value={config.op || 'uppercase'}
        onChange={(val) => updateNodeConfig(nodeId, { op: val || 'uppercase' })}
      />
    </Stack>
  
  );
}
