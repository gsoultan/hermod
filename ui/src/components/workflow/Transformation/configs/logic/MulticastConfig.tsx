import { Stack, Alert, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface MulticastConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function MulticastConfig({ config, updateNodeConfig, nodeId }: MulticastConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">Send the same message to multiple downstream branches simultaneously.</Text>
      </Alert>
      <TextInput
        label="Labels (Comma separated)"
        placeholder="e.g. branch1, branch2"
        value={config.labels || ''}
        onChange={(e) => updateNodeConfig(nodeId, { labels: e.currentTarget.value })}
        description="Downstream edges should match these labels."
      />
    </Stack>
  );
}
