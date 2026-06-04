import { SimpleGrid, Select, TextInput, Divider, Stack } from '@mantine/core';

interface ErrorHandlingConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function ErrorHandlingConfig({ config, updateNodeConfig, nodeId }: ErrorHandlingConfigProps) {
  return (
    <Stack gap="md">
      <Divider label="Error Handling" labelPosition="center" mt="xl" mb="md" />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <Select 
          label="On Error"
          description="Action to take when an error occurs."
          data={[
            {label: 'Fail Workflow', value: 'fail'}, 
            {label: 'Continue', value: 'continue'}, 
            {label: 'Drop Message', value: 'drop'}
          ]}
          value={config.onError || 'fail'}
          onChange={(val) => updateNodeConfig(nodeId, { onError: val || 'fail' })}
        />
        <TextInput 
          label="Status Field"
          placeholder="e.g. _trans_status"
          value={config.statusField || ''}
          onChange={(e) => updateNodeConfig(nodeId, { statusField: e.target.value })}
          description="Field to store success/error status."
        />
      </SimpleGrid>
    </Stack>
  );
}
