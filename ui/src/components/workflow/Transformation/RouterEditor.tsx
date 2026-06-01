import { ActionIcon, Button, Group, Stack, Text, TextInput, Alert, Divider } from '@mantine/core';import { FilterEditor } from './FilterEditor';import { IconInfoCircle, IconPlus, IconSettings, IconTrash } from '@tabler/icons-react';
interface RouterEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: string[];
}

export function RouterEditor({ selectedNode, updateNodeConfig, availableFields = [] }: RouterEditorProps) {
  let rules: any[] = [];
  try {
    rules = typeof selectedNode.data.rules === 'string' 
      ? JSON.parse(selectedNode.data.rules || '[]') 
      : (selectedNode.data.rules || []);
  } catch (e) {
    rules = [];
  }

  const updateRules = (next: any[]) => {
    updateNodeConfig(selectedNode.id, { rules: JSON.stringify(next) });
  };

  const addRule = () => {
    const next = [...rules, { label: `path_${rules.length + 1}`, conditions: [] }];
    updateRules(next);
  };

  const removeRule = (index: number) => {
    const next = [...rules];
    next.splice(index, 1);
    updateRules(next);
  };

  const updateRuleLabel = (index: number, label: string) => {
    const next = [...rules];
    next[index] = { ...next[index], label };
    updateRules(next);
  };

  const updateRuleConditions = (index: number, conditions: any[]) => {
    const next = [...rules];
    next[index] = { ...next[index], conditions };
    updateRules(next);
  };

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="Content-Based Router">
        Rules are evaluated in order. The first rule that matches will determine the branch.
        If no rules match, the message follows the "default" branch.
      </Alert>

      {rules.map((rule, idx) => (
        <Stack key={idx} gap="xs" style={{ border: '1px solid var(--mantine-color-gray-3)', padding: 12, borderRadius: 8 }}>
          <Group justify="space-between">
            <Group gap="xs">
               <IconSettings size="1rem" color="gray" />
               <Text size="sm" fw={700}>RULE #{idx + 1}</Text>
            </Group>
            <ActionIcon color="red" variant="subtle" onClick={() => removeRule(idx)}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>

          <TextInput
            label="Branch Label"
            placeholder="e.g. high_priority"
            value={rule.label || ''}
            onChange={(e) => updateRuleLabel(idx, e.target.value)}
            description="The label used for the outgoing edge in the workflow."
          />

          <Divider label="Conditions" labelPosition="center" />
          
          <FilterEditor
            conditions={rule.conditions || []}
            availableFields={availableFields}
            onChange={(conds) => updateRuleConditions(idx, conds)}
          />
        </Stack>
      ))}

      <Button variant="outline" leftSection={<IconPlus size="1rem" />} onClick={addRule}>
        Add Routing Rule
      </Button>
    </Stack>
  );
}


