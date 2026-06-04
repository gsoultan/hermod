import { Stack, Alert, Text, JsonInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface ApprovalConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function ApprovalConfig({ config, updateNodeConfig, nodeId }: ApprovalConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="green">
        <Text size="sm">Pause execution until a user manually approves or rejects the request. Ideal for high-stakes decisions.</Text>
      </Alert>
      <JsonInput
        label="Custom Form Definition (JSON)"
        placeholder='{"fields": [{"name": "reason", "type": "text", "label": "Reason"}]}'
        description="Define fields for the manual approval form"
        value={typeof config.form === 'string' ? config.form : JSON.stringify(config.form || {}, null, 2)}
        onChange={(val) => {
          try {
            updateNodeConfig(nodeId, { form: JSON.parse(val) });
          } catch {
            updateNodeConfig(nodeId, { form: val });
          }
        }}
        minRows={10}
        autosize
        formatOnBlur
      />
    </Stack>
  );
}
