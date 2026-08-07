import { Alert, Code, Stack, TagsInput, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface UnpivotConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function UnpivotConfig({ config, updateNodeConfig, nodeId }: UnpivotConfigProps) {
  return (

    <Stack gap="xs">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="About Unpivot">
        <Text size="xs">
          The Unpivot transformation rotates columns into attribute/value rows.
          Use it to convert wide-format data into long-format.
        </Text>
        <Text size="xs" mt="xs" fw={700}>Example:</Text>
        <Code block mt={5}>
          {`// Before:
{ "id": 1, "temp": 22, "hum": 45 }

// After (unpivoted):
[
  { "id": 1, "attribute": "temp", "value": 22 },
  { "id": 1, "attribute": "hum",  "value": 45 }
]`}
        </Code>
      </Alert>

      <TagsInput
        label="Columns to Unpivot"
        placeholder="e.g. Jan, Feb, Mar"
        description="The columns you want to turn into rows"
        value={config.pivotColumns || []}
        onChange={(val) => updateNodeConfig(nodeId, { pivotColumns: val })}
        required
      />

      <TextInput
        label="Attribute Field"
        placeholder="attribute"
        description="Name of the field that will store the column name"
        value={config.attributeField || 'attribute'}
        onChange={(e) => updateNodeConfig(nodeId, { attributeField: e.currentTarget.value })}
      />

      <TextInput
        label="Value Field"
        placeholder="value"
        description="Name of the field that will store the column value"
        value={config.valueField || 'value'}
        onChange={(e) => updateNodeConfig(nodeId, { valueField: e.currentTarget.value })}
      />

      <TextInput
        label="Target Field"
        placeholder="_fanout"
        description="The field where the resulting array will be stored"
        value={config.resultField || '_fanout'}
        onChange={(e) => updateNodeConfig(nodeId, { resultField: e.currentTarget.value })}
      />
    </Stack>
  
  );
}
