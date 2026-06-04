import { Stack, Alert, Text, TextInput, Button, Group, ActionIcon, ScrollArea, Box, Divider } from '@mantine/core';
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react';

interface SwitchConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function SwitchConfig({ config, updateNodeConfig, nodeId }: SwitchConfigProps) {
  const cases = Array.isArray(config.cases) ? config.cases : [];
  
  const updateCase = (index: number, val: any) => {
    const newCases = [...cases];
    newCases[index] = { ...newCases[index], ...val };
    updateNodeConfig(nodeId, { cases: newCases });
  };

  const addCase = () => {
    updateNodeConfig(nodeId, { cases: [...cases, { label: `case_${cases.length + 1}`, value: '' }] });
  };

  const removeCase = (index: number) => {
    updateNodeConfig(nodeId, { cases: cases.filter((_: any, i: number) => i !== index) });
  };

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="orange">
        <Text size="sm">Branch by exact value match of a field. If no match is found, 'default' is followed.</Text>
      </Alert>
      <TextInput
        label="Switch Field"
        placeholder="e.g. status, type"
        value={config.field || ''}
        onChange={(e) => updateNodeConfig(nodeId, { field: e.currentTarget.value })}
        required
      />
      <Divider label="Cases" labelPosition="center" />
      <ScrollArea.Autosize mah={400}>
        <Stack gap="sm">
          {cases.map((c: any, i: number) => (
            <Box key={i} p="xs" style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}>
              <Group grow gap="xs">
                <TextInput label="Value" placeholder="Match value" value={c.value || ''} onChange={(e) => updateCase(i, { value: e.currentTarget.value })} />
                <TextInput label="Branch Label" placeholder="Branch name" value={c.label || ''} onChange={(e) => updateCase(i, { label: e.currentTarget.value })} />
                <ActionIcon color="red" variant="subtle" mt="xl" onClick={() => removeCase(i)}><IconTrash size="1rem" /></ActionIcon>
              </Group>
            </Box>
          ))}
        </Stack>
      </ScrollArea.Autosize>
      <Button variant="light" leftSection={<IconPlus size="1rem" />} onClick={addCase}>Add Case</Button>
    </Stack>
  );
}
