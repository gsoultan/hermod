import { NumberInput, Stack, Switch, TextInput } from '@mantine/core';

interface RowCountConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function RowCountConfig({ config, updateNodeConfig, nodeId }: RowCountConfigProps) {
  return (

    <Stack gap="xs">
      <TextInput
        label="Target Field"
        placeholder="total_count"
        value={config.targetField || 'total_count'}
        onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
      />
      <NumberInput
        label="Increment"
        value={config.increment || 1}
        onChange={(val) => updateNodeConfig(nodeId, { increment: val })}
      />
      <Switch
        label="Persistent State"
        checked={config.persistent !== false}
        onChange={(e) => updateNodeConfig(nodeId, { persistent: e.currentTarget.checked })}
        description="Save count across workflow restarts"
      />
    </Stack>
  
  );
}
