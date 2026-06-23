import { IconPlus, IconTrash, IconFilter, IconInfoCircle } from '@tabler/icons-react';
import {
  Autocomplete,
  Button,
  Group,
  Select,
  Stack,
  Text,
  ActionIcon,
  Badge,
  rem,
  Paper,
  Tooltip,
  Box,
} from '@mantine/core';
import { TemplateField } from '../../shared/TemplateField';

export interface Condition {
  field: string;
  operator: string;
  value: string;
}

interface FilterEditorProps {
  conditions: Condition[];
  availableFields: string[];
  onChange: (next: Condition[]) => void;
}

export function FilterEditor({ conditions = [], availableFields = [], onChange }: FilterEditorProps) {
  const updateCondition = (index: number, field: keyof Condition, value: string) => {
    const next = [...conditions];
    next[index] = { ...next[index], [field]: value };
    onChange(next);
  };

  const removeCondition = (index: number) => {
    const next = conditions.filter((_, i) => i !== index);
    onChange(next);
  };

  const addCondition = () => {
    onChange([...conditions, { field: '', operator: '=', value: '' }]);
  };

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Group gap="xs">
          <IconFilter size={rem(18)} className="text-indigo-500" />
          <Text size="sm" fw={600}>
            Filter Conditions
          </Text>
          <Badge variant="dot" color="indigo" size="xs">
            AND
          </Badge>
        </Group>
      </Group>

      {conditions.length === 0 ? (
        <Paper
          withBorder
          p="md"
          radius="md"
          bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))"
          style={{ borderStyle: 'dashed', textAlign: 'center' }}
        >
          <Text size="xs" c="dimmed">
            No filters defined. All messages will pass.
          </Text>
        </Paper>
      ) : (
        <Stack gap="xs">
          {conditions.map((cond, index) => (
            <Paper key={index} withBorder p="xs" radius="md" className="hover:border-indigo-200 transition-colors">
              <Group grow gap="xs" align="flex-end">
                <Box style={{ flex: 2 }}>
                  <Group gap={4} mb={2}>
                    <Text size="10px" fw={700} c="dimmed" tt="uppercase" ml={2}>
                      Field
                    </Text>
                    <Tooltip label="Field or expression to evaluate. Supports functions like lower(), todate(), etc. Use source. prefix for nested fields.">
                      <IconInfoCircle size={rem(10)} style={{ cursor: 'help' }} />
                    </Tooltip>
                  </Group>
                  <Autocomplete
                    placeholder="e.g. status, lower(source.name)"
                    data={availableFields}
                    size="xs"
                    value={cond.field || ''}
                    onChange={(val) => updateCondition(index, 'field', val)}
                    styles={{ input: { fontFamily: 'monospace' } }}
                  />
                </Box>
                <Box style={{ flex: 1.5 }}>
                  <Text size="10px" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Operator
                  </Text>
                  <Select
                    data={[
                      { label: '=', value: '=' },
                      { label: '!=', value: '!=' },
                      { label: '>', value: '>' },
                      { label: '>=', value: '>=' },
                      { label: '<', value: '<' },
                      { label: '<=', value: '<=' },
                      { label: 'Contains', value: 'contains' },
                      { label: 'Not Contains', value: 'not_contains' },
                      { label: 'Regex', value: 'regex' },
                      { label: 'Not Regex', value: 'not_regex' },
                    ]}
                    size="xs"
                    value={cond.operator || '='}
                    onChange={(val) => updateCondition(index, 'operator', val || '=')}
                  />
                </Box>
                <Box style={{ flex: 2 }}>
                  <Text size="10px" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Value
                  </Text>
                  <TemplateField
                    placeholder="Value..."
                    value={cond.value || ''}
                    onChange={(val) => updateCondition(index, 'value', val)}
                    availableFields={availableFields}
                  />
                </Box>
                <Box style={{ flex: 'none' }}>
                  <Tooltip label="Remove filter">
                    <ActionIcon
                      color="red"
                      variant="subtle"
                      onClick={() => removeCondition(index)}
                      size="sm"
                      aria-label="Remove condition"
                    >
                      <IconTrash size={rem(16)} />
                    </ActionIcon>
                  </Tooltip>
                </Box>
              </Group>
            </Paper>
          ))}
        </Stack>
      )}

      <Button
        size="xs"
        variant="light"
        leftSection={<IconPlus size={rem(16)} />}
        onClick={addCondition}
        fullWidth
      >
        Add Filter Condition
      </Button>
    </Stack>
  );
}


