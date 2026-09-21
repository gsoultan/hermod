import { Stack, Alert, Text, TextInput, Button, Group, ActionIcon, ScrollArea, Box, Divider, Select, Autocomplete } from '@mantine/core';
import { useMemo } from 'react';
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react';

interface SwitchConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
}

export function SwitchConfig({ config, updateNodeConfig, nodeId, availableFields = [] }: SwitchConfigProps) {
  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  // `cases` arrives in either shape: this editor saves a plain array, while
  // the engine's tests, workflow bundles and anything built through the API
  // carry the JSON string form. MiscNodes.tsx and transformationUtils.ts both
  // already read either one; reading only the array here meant a
  // string-shaped workflow showed no cases, and the first edit saved that
  // emptiness over the user's configuration.
  const cases = useMemo(() => {
    if (Array.isArray(config.cases)) return config.cases;
    if (typeof config.cases === 'string' && config.cases.trim()) {
      try {
        const parsed = JSON.parse(config.cases);
        return Array.isArray(parsed) ? parsed : [];
      } catch {
        return [];
      }
    }
    return [];
  }, [config.cases]);

  const updateCase = (index: number, val: any) => {
    const newCases = [...cases];
    newCases[index] = { ...newCases[index], ...val };
    updateNodeConfig(nodeId, { cases: newCases });
  };

  const addCase = () => {
    updateNodeConfig(nodeId, { cases: [...cases, { label: `case_${cases.length + 1}`, value: '', operator: '=' }] });
  };

  const removeCase = (index: number) => {
    updateNodeConfig(nodeId, { cases: cases.filter((_: any, i: number) => i !== index) });
  };

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="orange">
        <Text size="sm">Branch by comparing a field value using various operators. If no match is found, 'default' is followed.</Text>
      </Alert>
      <Autocomplete
        label="Switch Field"
        placeholder="e.g. status, type"
        data={fieldPaths}
        value={config.field || ''}
        onChange={(val) => updateNodeConfig(nodeId, { field: val })}
        required
        description="Field or expression to evaluate for branching."
      />
      <Divider label="Cases" labelPosition="center" />
      <ScrollArea.Autosize mah={400}>
        <Stack gap="sm">
          {cases.map((c: any, i: number) => (
            <Box key={i} p="xs" style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}>
              <Stack gap="xs">
                <Group grow gap="xs">
                  <Select
                    label="Operator"
                    placeholder="Select operator"
                    value={c.operator || '='}
                    onChange={(val) => updateCase(i, { operator: val })}
                    data={[
                      { label: 'Equals (=)', value: '=' },
                      { label: 'Not Equals (!=)', value: '!=' },
                      { label: 'Greater Than (>)', value: '>' },
                      { label: 'Greater or Equal (>=)', value: '>=' },
                      { label: 'Less Than (<)', value: '<' },
                      { label: 'Less or Equal (<=)', value: '<=' },
                      { label: 'Contains', value: 'contains' },
                      { label: 'Not Contains', value: 'not_contains' },
                      { label: 'Regex Match', value: 'regex' },
                      { label: 'Not Regex Match', value: 'not_regex' },
                    ]}
                    size="sm"
                  />
                  <TextInput 
                    label="Value" 
                    placeholder="Match value" 
                    value={c.value || ''} 
                    onChange={(e) => updateCase(i, { value: e.currentTarget.value })} 
                    description="Comparison value"
                    size="sm"
                  />
                </Group>
                <Group grow gap="xs" align="flex-end">
                  <TextInput 
                    label="Branch Label" 
                    placeholder="Branch name" 
                    value={c.label || ''} 
                    onChange={(e) => updateCase(i, { label: e.currentTarget.value })} 
                    description="Output node connection"
                    size="sm"
                  />
                  <Group justify="flex-end">
                    <ActionIcon aria-label={`Remove case ${i + 1}`} color="red" variant="subtle" onClick={() => removeCase(i)}>
                      <IconTrash size="1rem" />
                    </ActionIcon>
                  </Group>
                </Group>
              </Stack>
            </Box>
          ))}
        </Stack>
      </ScrollArea.Autosize>
      <Button variant="light" leftSection={<IconPlus size="1rem" />} onClick={addCase}>Add Case</Button>
    </Stack>
  );
}
