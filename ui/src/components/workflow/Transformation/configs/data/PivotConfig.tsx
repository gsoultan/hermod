import { Alert, Code, Select, Stack, TagsInput, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface PivotConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function PivotConfig({ config, updateNodeConfig, nodeId }: PivotConfigProps) {
  return (

    <Stack gap="xs">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="About Pivot">
        <Text size="xs">
          The Pivot transformation rotates attribute/value rows into columns. 
          Use it to convert long-format data into wide-format.
        </Text>
        <Text size="xs" mt="xs" fw={700}>Example:</Text>
        <Code block mt={5}>
          {`// Before:
{ "id": 1, "attr": "temp", "val": 22 }
{ "id": 1, "attr": "hum",  "val": 45 }

// After (pivoted):
{ "id": 1, "temp": 22, "hum": 45 }`}
        </Code>
      </Alert>

      <TagsInput
        label="Index Keys"
        placeholder="e.g. id, branch_id"
        description="Fields used to identify unique groups of data"
        value={Array.isArray(config.indexKeys) ? config.indexKeys : (config.indexKeys?.split(',').filter(Boolean) || [])}
        onChange={(val) => updateNodeConfig(nodeId, { indexKeys: val })}
        required
      />

      <TextInput
        label="Attribute Field"
        placeholder="attribute"
        description="The field containing the name of the new column"
        value={config.attributeField || 'attribute'}
        onChange={(e) => updateNodeConfig(nodeId, { attributeField: e.currentTarget.value })}
        required
      />

      <TextInput
        label="Value Field"
        placeholder="value"
        description="The field containing the value for the new column"
        value={config.valueField || 'value'}
        onChange={(e) => updateNodeConfig(nodeId, { valueField: e.currentTarget.value })}
        required
      />

      <Select
        label="Aggregation Strategy"
        description="How to handle multiple values for the same attribute and index keys"
        data={[
          { value: 'first', label: 'First (Keep first encountered)' }, 
          { value: 'concat', label: 'Concat (Join values as string)' }
        ]}
        value={config.strategy || 'first'}
        onChange={(val) => updateNodeConfig(nodeId, { strategy: val || 'first' })}
      />

      <TextInput
        label="Target Field"
        placeholder="Leave empty to merge into root"
        description="Optional: Nest the pivoted data under this field"
        value={config.targetField || ''}
        onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
      />
    </Stack>
  
  );
}
