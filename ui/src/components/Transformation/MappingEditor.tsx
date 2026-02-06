import { ActionIcon, Autocomplete, Button, Divider, Group, JsonInput, Select, Stack, Text, TextInput } from '@mantine/core';import { notifications } from '@mantine/notifications';
import { getValByPath } from '../../utils/transformationUtils';import { IconArrowRight, IconPlus, IconTrash } from '@tabler/icons-react';
interface MappingEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: string[];
  incomingPayload?: any;
}

export function MappingEditor({ selectedNode, updateNodeConfig, availableFields = [], incomingPayload }: MappingEditorProps) {

  const renderMappingRules = () => {
    let mapping: Record<string, string> = {};
    try {
      mapping = JSON.parse(selectedNode.data.mapping || '{}');
    } catch (e) {
      return <Text size="xs" c="red">Invalid JSON mapping. Use the raw editor below to fix.</Text>;
    }

    const mappingEntries = Object.entries(mapping);

    const updateKey = (oldKey: string, newKey: string) => {
      const next = { ...mapping };
      const val = next[oldKey];
      delete next[oldKey];
      next[newKey] = val;
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const updateValue = (key: string, val: string) => {
      const next = { ...mapping };
      next[key] = val;
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const removeEntry = (key: string) => {
      const next = { ...mapping };
      delete next[key];
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const addEntry = () => {
      const next = { ...mapping };
      next[`new_key_${mappingEntries.length}`] = '';
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const addCurrentValue = () => {
      if (!incomingPayload || !selectedNode.data.field) return;
      const val = getValByPath(incomingPayload, selectedNode.data.field);
      if (val === undefined) return;
      const key = String(val);
      const next = { ...mapping };
      if (next[key] === undefined) {
        next[key] = '';
        updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
      }
    };

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={500}>Value Mapping Rules</Text>
          {incomingPayload && selectedNode.data.field && (
             <Button 
               size="compact-xs" 
               variant="subtle" 
               color="orange"
               leftSection={<IconPlus size="0.8rem" />}
               onClick={addCurrentValue}
             >
               Add current: {String(getValByPath(incomingPayload, selectedNode.data.field))}
             </Button>
          )}
        </Group>
        {mappingEntries.map(([oldVal, newVal], index) => (
          <Group key={index} grow gap="xs">
            <TextInput
              placeholder="Source Value"
              value={oldVal}
              onChange={(e) => updateKey(oldVal, e.target.value)}
            />
            <IconArrowRight size="1rem" style={{ flex: 'none' }} />
            <TextInput
              placeholder="Target Value"
              value={newVal}
              onChange={(e) => updateValue(oldVal, e.target.value)}
            />
            <ActionIcon aria-label="Remove entry" color="red" variant="subtle" onClick={() => removeEntry(oldVal)}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Button 
          size="xs" 
          variant="light" 
          leftSection={<IconPlus size="1rem" />}
          onClick={addEntry}
        >
          Add Mapping Rule
        </Button>
      </Stack>
    );
  };

  const handleSuggestMapping = async () => {
    try {
      const res = await fetch('/api/ai/suggest-mapping', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source_fields: availableFields, target_fields: availableFields })
      });
      if (!res.ok) {
        notifications.show({ title: 'Suggest Mapping Failed', message: 'Server error', color: 'red' });
        return;
      }
      const data = await res.json();
      const suggestions = data?.suggestions || {};
      let current: Record<string, any> = {};
      try { current = JSON.parse(selectedNode.data.mapping || '{}'); } catch {}
      const merged = { ...suggestions, ...current };
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(merged, null, 2) });
      notifications.show({ title: 'Suggestions Applied', message: 'Suggested mappings merged. Review and adjust as needed.', color: 'green' });
    } catch (e: any) {
      notifications.show({ title: 'Error', message: e.message, color: 'red' });
    }
  };

  return (
    <Stack gap="md">
      <Autocomplete 
        label="Field" 
        placeholder="e.g. status" 
        data={availableFields}
        value={selectedNode.data.field || ''} 
        onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
        description="Field to map. Supports nested objects and arrays."
      />
      <Select
        label="Mapping Type"
        data={[{label: 'Exact Match', value: 'exact'}, {label: 'Numeric Range', value: 'range'}, {label: 'Regular Expression', value: 'regex'}]}
        value={selectedNode.data.mappingType || 'exact'}
        onChange={(val) => updateNodeConfig(selectedNode.id, { mappingType: val || 'exact' })}
        mb="xs"
        description="Exact: '1' -> 'Active'. Range: '0-10' -> 'Low'. Regex: '^A.*' -> 'Starts with A'."
      />
      {renderMappingRules()}
      <Group justify="space-between" align="center" my="xs">
        <Text size="sm" fw={500}>Mapping Suggestions</Text>
        <Button size="xs" variant="light" onClick={handleSuggestMapping}>Suggest Mapping</Button>
      </Group>
      <Divider label="Raw JSON" labelPosition="center" />
      <JsonInput 
        label="Mapping (JSON)" 
        placeholder='{"1": "Active", "0": "Inactive"}' 
        value={selectedNode.data.mapping || ''} 
        onChange={(val) => updateNodeConfig(selectedNode.id, { mapping: val })} 
        formatOnBlur
        minRows={10}
      />
    </Stack>
  );
}


