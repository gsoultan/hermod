import { ActionIcon, Alert, Anchor, Badge, Button, Checkbox, Group, Stack, Text } from '@mantine/core';
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react';
import { useParams } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { versionLabel } from '@/lib/subflow';

/** A Run sub-flow step that runs this sub-flow (GET /api/workflows/{id}/callers). */
interface SubflowCaller {
  workflow_id: string;
  workflow_name: string;
  node_id: string;
  step: string;
  version: number;
  latest: boolean;
}

/** Which workflows run this sub-flow, and which version each runs. */
function UsedBy({ id }: { id: string }) {
  const { data: callers, isLoading, isError } = useQuery({
    queryKey: ['subflow-callers', id],
    queryFn: async (): Promise<SubflowCaller[]> => {
      const res = await apiFetch(`/api/workflows/${encodeURIComponent(id)}/callers`, { silent: true });
      if (!res.ok) throw new Error('Could not load the workflows that use this sub-flow');
      return (await res.json())?.data ?? [];
    },
  });

  return (
    <Stack gap={6}>
      <Text size="sm" fw={500}>Used by</Text>
      {isLoading && <Text size="sm" c="dimmed">Loading…</Text>}
      {isError && <Text size="sm" c="red">Could not load the workflows that use this sub-flow.</Text>}
      {callers && callers.length === 0 && (
        <Text size="sm" c="dimmed">No workflow runs this sub-flow yet.</Text>
      )}
      {callers?.map((c) => (
        <Group key={`${c.workflow_id}/${c.node_id}`} gap="xs" wrap="nowrap" justify="space-between">
          <Stack gap={0} style={{ minWidth: 0 }}>
            <Anchor href={`/workflows/${c.workflow_id}`} size="sm" truncate>{c.workflow_name || c.workflow_id}</Anchor>
            <Text size="xs" c="dimmed" truncate>at step {c.step}</Text>
          </Stack>
          <Badge variant="light" color={c.latest ? 'orange' : 'gray'} tt="none" radius="sm" style={{ flex: 'none' }}>
            {versionLabel(c.latest ? 'latest' : c.version || undefined)}
          </Badge>
        </Group>
      ))}
      {callers?.some((c) => c.latest) && (
        <Text size="xs" c="dimmed">
          A workflow on the latest save picks up each save of this sub-flow when it next starts. A save that would
          stop one from starting is refused.
        </Text>
      )}
    </Stack>
  );
}

interface SubflowInputConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

/** The fields a sub-flow takes. A caller must map every required one. */
export function SubflowInputConfig({ config, updateNodeConfig, nodeId }: SubflowInputConfigProps) {
  const { id } = useParams({ strict: false }) as { id?: string };
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
      {id && id !== 'new' && <UsedBy id={id} />}
    </Stack>
  );
}
