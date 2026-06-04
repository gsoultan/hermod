import { Stack, Alert, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface CollectConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function CollectConfig({ config, updateNodeConfig, nodeId }: CollectConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        <Text size="sm">
          Fan-in (Collect): Wait for all messages belonging to a fan-out group (created by a Foreach node) to arrive before continuing with a single merged message.
        </Text>
      </Alert>
      <Text size="sm" c="dimmed">
        This node automatically detects the fan-out group from message metadata. No manual configuration is strictly required, but you can specify a target field for the collection.
      </Text>
      <TextInput
        label="Target Field"
        placeholder="_items"
        description="The field where the collected array will be stored."
        value={config.targetField || '_items'}
        onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
      />
    </Stack>
  );
}
