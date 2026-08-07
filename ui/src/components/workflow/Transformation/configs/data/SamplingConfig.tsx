import { NumberInput, Select, Stack } from '@mantine/core';

interface SamplingConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function SamplingConfig({ config, updateNodeConfig, nodeId }: SamplingConfigProps) {
  return (

    <Stack gap="xs">
      <Select
        label="Sampling Type"
        data={[
          { value: 'percentage', label: 'Percentage (%)' },
          { value: 'row', label: 'Nth Row (Every N)' },
        ]}
        value={config.type || 'percentage'}
        onChange={(val: string | null) => updateNodeConfig(nodeId, { type: val || 'percentage' })}
      />
      <NumberInput
        label={config.type === 'row' ? 'Every Nth Row' : 'Percentage (0-100)'}
        value={config.value || 10}
        min={0.00001}
        max={config.type === 'row' ? undefined : 100}
        onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { value: val })}
      />
      {config.type === 'row' && (
        <NumberInput
          label="Limit (Optional)"
          placeholder="Max rows to emit"
          value={config.limit || 0}
          onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { limit: val })}
        />
      )}
    </Stack>
  
  );
}
