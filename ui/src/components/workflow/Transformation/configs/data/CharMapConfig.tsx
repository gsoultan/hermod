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
        // `operation` is the key the node reads. This wrote `op`, which it never
        // read, so every Character Map node passed its field through unchanged.
        // Stored configs still hold `op`, so it is read here and cleared on the
        // next edit rather than left behind to contradict the new key.
        value={config.operation || config.op || 'uppercase'}
        onChange={(val) => updateNodeConfig(nodeId, { operation: val || 'uppercase', op: undefined })}
      />
    </Stack>
  
  );
}
