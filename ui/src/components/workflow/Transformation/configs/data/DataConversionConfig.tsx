import { Autocomplete, Select, Stack, Text, TextInput } from '@mantine/core';

interface DataConversionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

// Element types offered for an array conversion. Every entry other than the
// "leave as-is" sentinel must be a type convertScalar accepts
// (pkg/comm/transformer/core/conversion.go), or the editor offers a conversion
// the pipeline rejects at runtime.
const ELEMENT_TYPES = [
  { value: 'auto', label: 'Leave as-is' },
  { value: 'string', label: 'String' },
  { value: 'int', label: 'Integer' },
  { value: 'float', label: 'Float' },
  { value: 'bool', label: 'Boolean' },
  { value: 'uuid', label: 'UUID' },
];

export function DataConversionConfig({ config, updateNodeConfig, nodeId, fieldPaths }: DataConversionConfigProps) {
  const targetType = config.targetType || 'string';
  // The separator is what splits a scalar into a list and what joins a list
  // back into a scalar, so it belongs to both directions.
  const usesSeparator = targetType === 'array' || targetType === 'string';

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
          { value: 'array', label: 'Array' },
          { value: 'uuid', label: 'UUID' },
        ]}
        value={targetType}
        onChange={(val) => updateNodeConfig(nodeId, { targetType: val || 'string' })}
      />
      {targetType === 'date' && (
        <TextInput
          label="Date Format"
          placeholder="2006-01-02"
          value={config.format || ''}
          onChange={(e) => updateNodeConfig(nodeId, { format: e.currentTarget.value })}
          description="Go date format (e.g. 2006-01-02)"
        />
      )}
      {targetType === 'uuid' && (
        <Text size="xs" c="dimmed">
          Validates the value and normalises it to lower-case hyphenated form. Accepts hyphenated,
          bare hex, braced and <code>urn:uuid</code> forms, and the raw 16 bytes a uuid column
          decodes to. A value that is not a UUID follows On Error.
        </Text>
      )}
      {usesSeparator && (
        <TextInput
          label="Separator"
          placeholder=","
          value={config.separator || ''}
          onChange={(e) => updateNodeConfig(nodeId, { separator: e.currentTarget.value })}
          description={
            targetType === 'array'
              ? 'Splits a text value into elements. Defaults to a comma. A value that is already a list, or a JSON array, is used as-is.'
              : 'Joins a list into text. Defaults to a comma. Ignored for values that are not lists.'
          }
        />
      )}
      {targetType === 'array' && (
        <Select
          label="Element Type"
          data={ELEMENT_TYPES}
          value={config.elementType || 'auto'}
          onChange={(val) => updateNodeConfig(nodeId, { elementType: val === 'auto' || !val ? '' : val })}
          description="Coerce every element. Needed when the column is typed: splitting text yields strings, and a string does not match an integer or uuid column."
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
