import { Alert, Stack, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { TextInput } from '@/components/shared/ReferenceInputs';

interface SubflowReturnConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

/** One way a sub-flow ends. With several, each is its own way out of the calling step. */
export function SubflowReturnConfig({ config, updateNodeConfig, nodeId }: SubflowReturnConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">
          A record that reaches this step goes back to the workflow that called the sub-flow. Give each Return a
          name, such as valid or needs_review, when there is more than one: the calling step gets one way out
          for each name.
        </Text>
      </Alert>
      <TextInput
        label="Name"
        placeholder="valid"
        value={config.name ?? ''}
        onChange={(e) => updateNodeConfig(nodeId, { name: e.currentTarget.value.trim() })}
      />
    </Stack>
  );
}
