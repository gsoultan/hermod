import { ActionIcon, Badge, Button, Group, Stack, Text, TextInput, Tooltip as MantineTooltip, Alert, Autocomplete } from '@mantine/core';import { IconArrowRight, IconBracketsContain, IconCode, IconInfoCircle, IconPlus, IconTrash, IconVariable } from '@tabler/icons-react';
interface SetFieldEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: string[];
  incomingPayload?: any;
  transType: string;
  onAddFromSource: (path: string) => void;
  addField: (path?: string, value?: string) => void;
}

export function SetFieldEditor({ selectedNode, updateNodeConfig, availableFields = [], incomingPayload, transType, onAddFromSource, addField }: SetFieldEditorProps) {
  const fields = Object.entries(selectedNode.data)
    .filter(([k]) => k.startsWith('column.'))
    .map(([k, v]) => ({ fullKey: k, path: k.replace('column.', ''), value: v }));

  const updateFieldPath = (oldFullKey: string, newPath: string) => {
    const baseData = Object.fromEntries(
      Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.'))
    );
    const otherFields = Object.fromEntries(
      Object.entries(selectedNode.data).filter(([k]) => k.startsWith('column.') && k !== oldFullKey)
    );
    updateNodeConfig(selectedNode.id, { ...baseData, ...otherFields, [`column.${newPath}`]: selectedNode.data[oldFullKey] }, true);
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
    <Stack gap="xs">
      <Group justify="space-between">
        <Text size="sm" fw={500}>{isAdvanced ? 'Transformation Rules' : 'Fields to Set'}</Text>
        {incomingPayload && (
          <Group gap="xs">
            <Text size="xs" c="dimmed">Quick add from source:</Text>
            <Group gap={4}>
              {availableFields.slice(0, 5).map(f => (
                <Badge 
                  key={f} 
                  size="xs" 
                  variant="light" 
                  color="blue"
                  style={{ cursor: 'pointer', textTransform: 'none' }}
                  onClick={() => onAddFromSource(f)}
                >
                  + {f}
                </Badge>
              ))}
            </Group>
          </Group>
        )}
      </Group>

      {fields.length === 0 && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="gray" variant="outline">
          <Text size="xs">No fields defined yet. Click "Add Field" or use the quick-add badges above to start.</Text>
        </Alert>
      )}

      {fields.map((field, index) => (
        <Group key={index} grow gap="xs" style={{ background: 'var(--mantine-color-gray-0)', padding: 8, borderRadius: 8 }}>
          <Autocomplete
            placeholder="Target Path"
            data={availableFields}
            size="xs"
            leftSection={<IconBracketsContain size="0.8rem" />}
            value={field.path}
            onChange={(val) => updateFieldPath(field.fullKey, val)}
          />
          <TextInput
            placeholder={isAdvanced ? "Expression (e.g. upper(source.field))" : "Value (literal or source.path)"}
            size="xs"
            leftSection={isAdvanced ? <IconCode size="0.8rem" /> : <IconVariable size="0.8rem" />}
            value={String(field.value || '')}
            onChange={(e) => updateFieldValue(field.fullKey, e.target.value)}
            rightSection={
              incomingPayload && (
                <Group gap={2} px={4}>
                  <MantineTooltip label="Use source value" position="top">
                    <ActionIcon 
                      size="xs" 
                      variant="subtle" 
                      onClick={() => updateFieldValue(field.fullKey, `source.${field.path}`)}
                      disabled={!availableFields.includes(field.path)}
                    >
                      <IconArrowRight size="0.8rem" />
                    </ActionIcon>
                  </MantineTooltip>
                </Group>
              )
            }
          />
          <ActionIcon aria-label="Remove field" color="red" variant="subtle" onClick={() => removeField(field.fullKey)} style={{ flex: 'none' }}>
            <IconTrash size="1rem" />
          </ActionIcon>
        </Group>
      ))}
      <Button 
        size="xs" 
        variant="light" 
        fullWidth
        leftSection={<IconPlus size="1rem" />}
        onClick={() => addField()}
      >
        {isAdvanced ? 'Add Transformation Rule' : 'Add Field'}
      </Button>
    </Stack>
  );
}


