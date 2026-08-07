import { Alert, Stack, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface AuditConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function AuditConfig({ config, updateNodeConfig, nodeId }: AuditConfigProps) {
  return (

    <Stack gap="xs">
      <TextInput
        label="Prefix"
        placeholder="audit_"
        value={config.prefix || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { prefix: e.currentTarget.value })}
        description="Optional prefix for injected metadata fields (e.g. audit_workflow_id)"
      />
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        This node injects: workflow_id, node_id, machine_name, timestamp, and message_id.
      </Alert>
    </Stack>
  
  );
}
