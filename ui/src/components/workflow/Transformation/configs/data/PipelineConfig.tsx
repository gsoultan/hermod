import { Stack, Text, JsonInput } from '@mantine/core';

interface PipelineConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function PipelineConfig({ config, updateNodeConfig, nodeId }: PipelineConfigProps) {
  return (
    <Stack gap="xs" style={{ flex: 1 }}>
      <Text size="sm" fw={500}>Steps</Text>
      <JsonInput 
        label="Steps (JSON Array)" 
        placeholder='[{"transType": "mask", "field": "email", "maskType": "email"}, {"transType": "set", "column.processed": true}]' 
        value={config.steps || '[]'} 
        onChange={(val) => updateNodeConfig(nodeId, { steps: val })} 
        formatOnBlur
        minRows={20}
        styles={{ 
          root: { flex: 1, display: 'flex', flexDirection: 'column' },
          wrapper: { flex: 1, display: 'flex', flexDirection: 'column' },
          input: { flex: 1, fontFamily: 'monospace', fontSize: '11px' } 
        }}
        description="List of transformation steps to execute in order."
      />
    </Stack>
  );
}
