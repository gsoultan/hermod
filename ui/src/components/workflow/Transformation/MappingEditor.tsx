import {
  ActionIcon,
  Autocomplete,
  Button,
  Group,
  JsonInput,
  Select,
  Stack,
  Text,
  TextInput,
  Card,
  Badge,
  Tooltip,
  Box,
  Paper,
  rem,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { getValByPath } from '../../../utils/transformationUtils';
import {
  IconArrowRight,
  IconPlus,
  IconTrash,
  IconSparkles,
  IconSettings,
  IconList,
  IconCode,
} from '@tabler/icons-react';

interface MappingEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: string[];
  incomingPayload?: any;
}

export function MappingEditor({
  selectedNode,
  updateNodeConfig,
  availableFields = [],
  incomingPayload,
}: MappingEditorProps) {
  const renderMappingRules = () => {
    let mapping: Record<string, string> = {};
    try {
      mapping = JSON.parse(selectedNode.data.mapping || '{}');
    } catch (e) {
      return (
        <Text size="xs" c="red" fw={500}>
          Invalid JSON mapping. Use the raw editor below to fix.
        </Text>
      );
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
      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group justify="space-between" align="center">
            <Group gap="xs">
              <IconList size={rem(18)} className="text-indigo-500" />
              <Text size="sm" fw={600}>
                Value Mapping Rules
              </Text>
              <Badge variant="light" color="indigo" size="xs">
                {mappingEntries.length} rules
              </Badge>
            </Group>
            {incomingPayload && selectedNode.data.field && (
              <Button
                size="compact-xs"
                variant="light"
                color="orange"
                leftSection={<IconPlus size={rem(14)} />}
                onClick={addCurrentValue}
              >
                Add current: {String(getValByPath(incomingPayload, selectedNode.data.field))}
              </Button>
            )}
          </Group>

          <Text size="xs" c="dimmed">
            Define how source values should be transformed into target values.
          </Text>

          {mappingEntries.length === 0 ? (
            <Paper
              withBorder
              p="xl"
              radius="md"
              bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))"
              style={{ borderStyle: 'dashed', textAlign: 'center' }}
            >
              <Text size="xs" c="dimmed">
                No mapping rules defined. Click "Add Mapping Rule" or use "Suggest Mapping".
              </Text>
            </Paper>
          ) : (
            <Stack gap="xs">
              {mappingEntries.map(([oldVal, newVal], index) => (
                <Group key={index} grow gap="xs">
                  <TextInput
                    placeholder="Source Value"
                    value={oldVal}
                    size="sm"
                    onChange={(e) => updateKey(oldVal, e.target.value)}
                    styles={{ input: { fontFamily: 'monospace' } }}
                  />
                  <Box style={{ flex: 'none' }}>
                    <IconArrowRight size={rem(16)} color="var(--mantine-color-gray-5)" />
                  </Box>
                  <TextInput
                    placeholder="Target Value"
                    value={newVal}
                    size="sm"
                    onChange={(e) => updateValue(oldVal, e.target.value)}
                    styles={{ input: { fontFamily: 'monospace' } }}
                  />
                  <Box style={{ flex: 'none' }}>
                    <Tooltip label="Remove mapping">
                      <ActionIcon
                        aria-label="Remove entry"
                        color="red"
                        variant="subtle"
                        onClick={() => removeEntry(oldVal)}
                      >
                        <IconTrash size={rem(16)} />
                      </ActionIcon>
                    </Tooltip>
                  </Box>
                </Group>
              ))}
            </Stack>
          )}

          <Button
            size="xs"
            variant="light"
            fullWidth
            leftSection={<IconPlus size={rem(16)} />}
            onClick={addEntry}
          >
            Add Mapping Rule
          </Button>
        </Stack>
      </Card>
    );
  };

  const handleSuggestMapping = async () => {
    try {
      const res = await fetch('/api/ai/suggest-mapping', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source_fields: availableFields, target_fields: availableFields }),
      });
      if (!res.ok) {
        notifications.show({ title: 'Suggest Mapping Failed', message: 'Server error', color: 'red' });
        return;
      }
      const data = await res.json();
      const suggestions = data?.suggestions || {};
      let current: Record<string, any> = {};
      try {
        current = JSON.parse(selectedNode.data.mapping || '{}');
      } catch {}
      const merged = { ...suggestions, ...current };
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(merged, null, 2) });
      notifications.show({
        title: 'Suggestions Applied',
        message: 'Suggested mappings merged. Review and adjust as needed.',
        color: 'green',
      });
    } catch (e: any) {
      notifications.show({ title: 'Error', message: e.message, color: 'red' });
    }
  };

  return (
    <Stack gap="lg">
      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <IconSettings size={rem(18)} className="text-indigo-500" />
            <Text size="sm" fw={600}>
              General Configuration
            </Text>
          </Group>

          <Autocomplete
            label="Source Field"
            placeholder="e.g. status"
            data={availableFields}
            value={selectedNode.data.field || ''}
            onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })}
            description="Field to transform. Supports nested paths (e.g. user.profile.id)."
            size="sm"
          />

          <Select
            label="Mapping Strategy"
            data={[
              { label: 'Exact Match', value: 'exact' },
              { label: 'Numeric Range', value: 'range' },
              { label: 'Regular Expression', value: 'regex' },
            ]}
            value={selectedNode.data.mappingType || 'exact'}
            onChange={(val) => updateNodeConfig(selectedNode.id, { mappingType: val || 'exact' })}
            description="Select how source values are matched against rules."
            size="sm"
          />
        </Stack>
      </Card>

      {renderMappingRules()}

      <Paper withBorder p="md" radius="md" bg="var(--mantine-color-blue-light)" border-color="var(--mantine-color-blue-light-color)">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <IconSparkles size={rem(20)} color="var(--mantine-color-blue-6)" />
            <Stack gap={0}>
              <Text size="sm" fw={600}>
                AI Suggestions
              </Text>
              <Text size="xs" c="dimmed">
                Automatically generate mapping rules based on available fields.
              </Text>
            </Stack>
          </Group>
          <Button size="xs" color="blue" variant="filled" onClick={handleSuggestMapping} leftSection={<IconSparkles size={rem(14)} />}>
            Suggest Mapping
          </Button>
        </Group>
      </Paper>

      <Stack gap="xs">
        <Group gap="xs">
          <IconCode size={rem(18)} className="text-gray-500" />
          <Text size="sm" fw={600}>
            Raw Mapping (JSON)
          </Text>
        </Group>
        <JsonInput
          placeholder='{"1": "Active", "0": "Inactive"}'
          value={selectedNode.data.mapping || ''}
          onChange={(val) => updateNodeConfig(selectedNode.id, { mapping: val })}
          formatOnBlur
          minRows={6}
          size="sm"
          styles={{ input: { fontFamily: 'monospace' } }}
        />
      </Stack>
    </Stack>
  );
}


