import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Stack,
  Text,
  Autocomplete,
  rem,
  Paper,
  Box,
  Tooltip,
} from '@mantine/core';
import { useMemo } from 'react';
import {
  IconBracketsContain,
  IconPlus,
  IconTrash,
  IconCirclePlus,
  IconEdit,
} from '@tabler/icons-react';
import { TemplateField } from '../../shared/TemplateField';

interface SetFieldEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: any[];
  incomingPayload?: any;
  transType: string;
  onAddFromSource: (path: string) => void;
  addField: (path?: string, value?: string) => void;
}

export function SetFieldEditor({
  selectedNode,
  updateNodeConfig,
  availableFields = [],
  incomingPayload,
  transType,
  onAddFromSource,
  addField,
}: SetFieldEditorProps) {
  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  const fields = Object.entries(selectedNode.data)
    .filter(([k]) => k.startsWith('column.'))
    .map(([k, v]) => ({ fullKey: k, path: k.replace('column.', ''), value: v }));

  const updateFieldPath = (oldFullKey: string, newPath: string) => {
    const baseData = Object.fromEntries(
      Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.'))
    );
    const otherFields = Object.fromEntries(
      Object.entries(selectedNode.data).filter(
        ([k]) => k.startsWith('column.') && k !== oldFullKey
      )
    );
    updateNodeConfig(
      selectedNode.id,
      { ...baseData, ...otherFields, [`column.${newPath}`]: selectedNode.data[oldFullKey] },
      true
    );
  };

  const updateFieldValue = (fullKey: string, newValue: any) => {
    updateNodeConfig(selectedNode.id, { [fullKey]: newValue });
  };

  const removeField = (fullKey: string) => {
    const baseData = Object.fromEntries(
      Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.'))
    );
    const remainingFields = Object.fromEntries(
      Object.entries(selectedNode.data).filter(([k]) => k.startsWith('column.') && k !== fullKey)
    );
    updateNodeConfig(selectedNode.id, { ...baseData, ...remainingFields }, true);
  };

  const isAdvanced = transType === 'advanced';

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start">
        <Stack gap={0}>
          <Group gap="xs">
            <IconEdit size={rem(18)} className="text-indigo-500" />
            <Text size="sm" fw={600}>
              {isAdvanced ? 'Transformation Rules' : 'Field Mappings'}
            </Text>
          </Group>
          <Text size="xs" c="dimmed">
            {isAdvanced
              ? 'Define complex transformations using expressions.'
              : 'Set or update fields in the outgoing payload.'}
          </Text>
        </Stack>

        {incomingPayload && (
          <Box>
            <Text size="10px" fw={700} c="dimmed" mb={4} tt="uppercase" ta="right">
              Quick add from source
            </Text>
            <Group gap={4} justify="flex-end">
              {fieldPaths.slice(0, 5).map((f) => (
                <Badge
                  key={f}
                  size="xs"
                  variant="light"
                  color="blue"
                  className="hover:scale-105 transition-transform cursor-pointer"
                  onClick={() => onAddFromSource(f)}
                  leftSection={<IconPlus size={rem(10)} />}
                  styles={{ label: { textTransform: 'none' } }}
                >
                  {f}
                </Badge>
              ))}
            </Group>
          </Box>
        )}
      </Group>

      {fields.length === 0 ? (
        <Paper
          withBorder
          p="xl"
          radius="md"
          bg="var(--mantine-color-gray-0)"
          className="dark:bg-dark-7"
          style={{ borderStyle: 'dashed', textAlign: 'center' }}
        >
          <Stack gap="xs" align="center">
            <IconCirclePlus size={rem(32)} className="text-gray-400" />
            <Text size="xs" c="dimmed">
              No fields defined yet. Click "Add Field" or use the quick-add badges above to start.
            </Text>
          </Stack>
        </Paper>
      ) : (
        <Stack gap="xs">
          {fields.map((field, index) => (
            <Paper key={index} withBorder p="xs" radius="md" className="hover:border-indigo-200 transition-colors">
              <Group grow gap="xs" align="flex-start">
                <Box style={{ flex: 1.5 }}>
                  <Text size="10px" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Target Path
                  </Text>
                  <Autocomplete
                    placeholder="e.g. user.id"
                    data={fieldPaths}
                    size="xs"
                    leftSection={<IconBracketsContain size={rem(14)} />}
                    value={field.path}
                    onChange={(val) => updateFieldPath(field.fullKey, val)}
                    styles={{ input: { fontFamily: 'monospace' } }}
                  />
                </Box>
                <Box style={{ flex: 3 }}>
                  <Text size="10px" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Value / Expression
                  </Text>
                  <TemplateField
                    placeholder="Value or expression (e.g. source.name, lower(source.name))"
                    value={String(field.value || '')}
                    onChange={(val) => updateFieldValue(field.fullKey, val)}
                    availableFields={availableFields}
                    buildToken={(p) => `source.${p}`}
                    multiline={isAdvanced}
                  />
                  <Text size="10px" c="dimmed" mt={2}>
                    Use functions and source paths. Click {"{x}"} to insert fields.
                  </Text>
                </Box>
                <Box style={{ flex: 'none', alignSelf: 'center' }}>
                  <Tooltip label="Remove field">
                    <ActionIcon
                      aria-label="Remove field"
                      color="red"
                      variant="subtle"
                      onClick={() => removeField(field.fullKey)}
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
        fullWidth
        leftSection={<IconPlus size={rem(16)} />}
        onClick={() => addField()}
      >
        {isAdvanced ? 'Add Transformation Rule' : 'Add New Field Mapping'}
      </Button>
    </Stack>
  );
}


