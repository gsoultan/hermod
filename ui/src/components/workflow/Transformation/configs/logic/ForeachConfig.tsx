import { TextInput, Stack, Alert, Text } from '@mantine/core';
import { IconCircles, IconInfoCircle } from '@tabler/icons-react';

interface ForeachConfigProps {
  config: any;
  updateNodeConfig: (nodeId: string, config: any) => void;
  nodeId: string;
}

export function ForeachConfig({ config, updateNodeConfig, nodeId }: ForeachConfigProps) {
  return (
    <Stack gap="md">
      {(!config.arrayPath || String(config.arrayPath).trim() === '') && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="red">
          <Text size="sm">Array Path is required to perform fan-out. Please provide a valid path.</Text>
        </Alert>
      )}
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        <Text size="sm">
          Execution-level Fan-out: Splits one message into multiple independent messages based on an array field.
          Downstream nodes will be executed once for each item.
        </Text>
      </Alert>
      <TextInput
        label="Array Path"
        placeholder="e.g. results, data.items"
        description="Path to the array field in the message data"
        value={config.arrayPath || ''}
        onChange={(e) => updateNodeConfig(nodeId, { arrayPath: e.currentTarget.value })}
        leftSection={<IconCircles size="1rem" />}
        required
      />
      <Text size="xs" c="dimmed">
        Each message will have <code>_item</code> and <code>_index</code> fields added.
      </Text>
    </Stack>
  );
}
