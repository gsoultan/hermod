import { ActionIcon, Alert, Button, Checkbox, Group, Stack, Text } from '@mantine/core';
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react';
import { TextInput } from '@/components/shared/ReferenceInputs';

interface SubflowInputConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

/** The fields a sub-flow takes. A caller must map every required one. */
export function SubflowInputConfig({ config, updateNodeConfig, nodeId }: SubflowInputConfigProps) {
  const fields: { name: string; required?: boolean }[] = Array.isArray(config.fields) ? config.fields : [];
  const set = (next: typeof fields) => updateNodeConfig(nodeId, { fields: next });

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">
          This makes the workflow a sub-flow: it has no source, and runs inside the workflows that call it with a
          Run sub-flow step. List the fields it needs. Each caller maps its own fields onto these, and the sub-flow
          sees nothing else.
        </Text>
      </Alert>
      {fields.map((f, i) => (
        <Group key={i} gap="xs" wrap="nowrap" align="center">
          <TextInput
            style={{ flex: 1 }}
            aria-label="Input name"
            placeholder="zip"
            value={f.name ?? ''}
            onChange={(e) => set(fields.map((x, j) => (j === i ? { ...x, name: e.currentTarget.value } : x)))}
          />
          <Checkbox
            label="Required"
            checked={f.required === true}
            onChange={(e) => set(fields.map((x, j) => (j === i ? { ...x, required: e.currentTarget.checked } : x)))}
          />
          <ActionIcon variant="subtle" color="red" aria-label="Remove input" onClick={() => set(fields.filter((_, j) => j !== i))}>
            <IconTrash size="1rem" />
          </ActionIcon>
        </Group>
      ))}
      <Button
        variant="light"
        size="xs"
        leftSection={<IconPlus size="0.9rem" />}
        onClick={() => set([...fields, { name: '', required: true }])}
        style={{ alignSelf: 'flex-start' }}
      >
        Add input
      </Button>
    </Stack>
  );
}
