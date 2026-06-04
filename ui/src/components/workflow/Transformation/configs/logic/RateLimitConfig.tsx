import { Stack, Group, NumberInput, Select, Autocomplete, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface RateLimitConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: string[];
}

export function RateLimitConfig({ config, updateNodeConfig, nodeId, availableFields }: RateLimitConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        <Text size="sm">Control the flow of messages to prevent overwhelming downstream systems.</Text>
      </Alert>
      <Group grow>
        <NumberInput label="MPS" description="Messages Per Second" min={0.1} value={config.mps || 100} onChange={(val) => updateNodeConfig(nodeId, { mps: val })} />
        <NumberInput label="Burst" description="Max burst size" min={1} value={config.burst || 100} onChange={(val) => updateNodeConfig(nodeId, { burst: val })} />
      </Group>
      <Select 
        label="Strategy"
        data={[{ label: 'Wait (Block)', value: 'wait' }, { label: 'Drop (Discard)', value: 'drop' }]}
        value={config.strategy || 'wait'}
        onChange={(val) => updateNodeConfig(nodeId, { strategy: val || 'wait' })}
      />
      <Autocomplete 
        label="Key Field (Optional)" 
        placeholder="e.g. user_id" 
        data={availableFields || []}
        value={config.keyField || ''} 
        onChange={(val) => updateNodeConfig(nodeId, { keyField: val })} 
        description="Apply limits per unique value of this field."
      />
    </Stack>
  );
}
