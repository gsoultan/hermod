import { Stack, TextInput, Select } from '@mantine/core';

interface LogConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function LogConfig({ config, updateNodeConfig, nodeId }: LogConfigProps) {
  return (
    <Stack gap="md">
      <TextInput
        label="Log Message"
        placeholder="e.g. Processing order..."
        value={config.message || ''}
        onChange={(e) => updateNodeConfig(nodeId, { message: e.currentTarget.value })}
      />
      <Select
        label="Log Level"
        data={[
          { value: 'DEBUG', label: 'Debug' },
          { value: 'INFO', label: 'Info' },
          { value: 'WARN', label: 'Warning' },
          { value: 'ERROR', label: 'Error' },
        ]}
        value={config.level || 'INFO'}
        onChange={(val) => updateNodeConfig(nodeId, { level: val })}
      />
      <TextInput
        label="Data Path (Optional)"
        placeholder="e.g. order.id"
        description="Specific field to log. If empty, logs full message data."
        value={config.path || ''}
        onChange={(e) => updateNodeConfig(nodeId, { path: e.currentTarget.value })}
      />
    </Stack>
  );
}
