import { Autocomplete, Select, Stack, TextInput } from '@mantine/core';

interface DataConversionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

export function DataConversionConfig({ config, updateNodeConfig, nodeId, fieldPaths }: DataConversionConfigProps) {
  return (

    <Stack gap="xs">
      <Autocomplete
        label="Field"
        placeholder="amount"
        data={fieldPaths || []}
        value={config.field || ''}
        onChange={(val) => updateNodeConfig(nodeId, { field: val })}
        required
        description="Field or expression to convert (e.g. amount, lower(source.status))."
      />
      <Select
        label="Target Type"
        data={[
          { value: 'int', label: 'Integer' },
          { value: 'float', label: 'Float' },
          { value: 'string', label: 'String' },
          { value: 'bool', label: 'Boolean' },
          { value: 'date', label: 'Date' },
        ]}
        value={config.targetType || 'string'}
        onChange={(val) => updateNodeConfig(nodeId, { targetType: val || 'string' })}
      />
      {config.targetType === 'date' && (
        <TextInput
          label="Date Format"
          placeholder="2006-01-02"
          value={config.format || ''}
          onChange={(e) => updateNodeConfig(nodeId, { format: e.currentTarget.value })}
          description="Go date format (e.g. 2006-01-02)"
        />
      )}
      <Select
        label="On Error"
        description="How to handle values that cannot be converted to the target type."
        data={[
          { value: 'fail', label: 'Fail (Error output)' },
          { value: 'null', label: 'Set to NULL' },
          { value: 'keep', label: 'Keep original' },
        ]}
        value={config.errorBehavior || 'fail'}
        onChange={(val) => updateNodeConfig(nodeId, { errorBehavior: val || 'fail' })}
      />
      <TextInput
        label="Target Field (Optional)"
        placeholder="Defaults to source field"
        value={config.targetField || ''}
        onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
      />
    </Stack>
  
  );
}
