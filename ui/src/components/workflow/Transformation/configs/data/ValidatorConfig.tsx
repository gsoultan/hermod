import { Stack, JsonInput, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface ValidatorConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function ValidatorConfig({ config, updateNodeConfig, nodeId }: ValidatorConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">Validate incoming messages against a JSON schema or set of rules.</Text>
      </Alert>
      <JsonInput 
        label="Validation Rules (JSON)" 
        placeholder='{"field.path": "string"}' 
        value={config.schema || ''} 
        onChange={(val) => updateNodeConfig(nodeId, { schema: val })} 
        formatOnBlur
        minRows={10}
        description="Define expected types or values for fields."
      />
    </Stack>
  );
}
