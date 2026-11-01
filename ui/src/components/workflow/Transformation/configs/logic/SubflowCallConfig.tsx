import { useMemo } from 'react';
import { useParams } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ActionIcon, Alert, Anchor, Autocomplete, Button, Group, Select, Stack, Text } from '@mantine/core';
import { IconArrowRight, IconBoxMultiple, IconExternalLink, IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import {
  isSubflow, subflowFields, subflowReturns, versionLabel,
  type SavedNode, type SubflowMapping,
} from '@/lib/subflow';

interface SubflowCallConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields?: any[];
}

interface SavedWorkflow {
  id: string;
  name: string;
  nodes?: SavedNode[];
}

interface SavedVersion {
  version: number;
  message?: string;
  nodes?: SavedNode[] | null;
}

/** One saved version with its nodes; the version list leaves them out. */
function versionQuery(subflowId: string, version: number) {
  return {
    queryKey: ['workflow-version', subflowId, version],
    queryFn: async (): Promise<SavedVersion> => {
      const res = await apiFetch(`/api/workflows/${encodeURIComponent(subflowId)}/versions/${version}`);
      if (!res.ok) throw new Error('Could not load the version');
      return res.json();
    },
    staleTime: Infinity, // a saved version never changes
  };
}

function mappings(v: unknown): SubflowMapping[] {
  return Array.isArray(v) ? v.map((m: any) => ({ from: String(m?.from ?? ''), to: String(m?.to ?? '') })) : [];
}

/**
 * The Run sub-flow step's editor: which sub-flow, which saved version of it,
 * and how this workflow's fields map onto its inputs and back from its result.
 */
export function SubflowCallConfig({ config, updateNodeConfig, nodeId, availableFields = [] }: SubflowCallConfigProps) {
  const vhost = useWorkflowStore((s) => s.vhost);
  const { id: currentId } = useParams({ strict: false }) as { id?: string };
  const fieldPaths = useMemo(
    () => (availableFields || []).map((f: any) => (typeof f === 'string' ? f : f.path)).filter(Boolean),
    [availableFields],
  );

  const { data: workflows = [], isLoading } = useQuery({
    queryKey: ['subflows', vhost],
    queryFn: async (): Promise<SavedWorkflow[]> => {
      const res = await apiFetch(`/api/workflows?limit=500&vhost=${encodeURIComponent(vhost || '')}`);
      if (!res.ok) throw new Error('Could not load workflows');
      const body = await res.json();
      return (body?.data ?? []) as SavedWorkflow[];
    },
  });
  const subflows = workflows.filter((w) => isSubflow(w.nodes) && w.id !== currentId);
  const chosen = subflows.find((w) => w.id === config.subflow_id);

  const { data: versions = [] } = useQuery({
    queryKey: ['versions', config.subflow_id],
    queryFn: async (): Promise<SavedVersion[]> => {
      const res = await apiFetch(`/api/workflows/${encodeURIComponent(config.subflow_id)}/versions`);
      if (!res.ok) throw new Error('Could not load versions');
      return res.json();
    },
    enabled: !!config.subflow_id,
  });

  const queryClient = useQueryClient();
  const pinned = typeof config.version === 'number' && config.subflow_id ? config.version : 0;
  const { data: pinnedVersion } = useQuery({ ...versionQuery(config.subflow_id, pinned), enabled: pinned > 0 });
  const pinnedNodes: SavedNode[] | null | undefined = config.version === 'latest' ? chosen?.nodes : pinnedVersion?.nodes;
  const fields = subflowFields(pinnedNodes);
  const inputs = mappings(config.inputs);
  const outputs = mappings(config.outputs);

  const pickVersion = async (value: string | null) => {
    const version = value === 'latest' ? 'latest' : value ? Number(value) : undefined;
    let nodes: SavedNode[] | null | undefined = chosen?.nodes;
    if (typeof version === 'number') {
      nodes = (await queryClient.fetchQuery(versionQuery(config.subflow_id, version))).nodes;
    } else if (version === undefined) {
      nodes = undefined;
    }
    // One row per declared input, keeping what was already mapped to it.
    const next = subflowFields(nodes).map((f) => ({
      from: inputs.find((m) => m.to === f.name)?.from ?? '',
      to: f.name,
    }));
    updateNodeConfig(nodeId, { version, returns: subflowReturns(nodes), inputs: next });
  };

  const setInput = (to: string, from: string) => {
    const rest = inputs.filter((m) => m.to !== to);
    updateNodeConfig(nodeId, { inputs: [...rest, { from, to }] });
  };
  const setOutput = (i: number, patch: Partial<SubflowMapping>) => {
    updateNodeConfig(nodeId, { outputs: outputs.map((m, j) => (j === i ? { ...m, ...patch } : m)) });
  };

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">
          Runs a sub-flow on each record and waits for it. The sub-flow sees only the inputs you map here, and
          its result comes back into this record. Pin a saved version so a later change to the sub-flow does
          not change this workflow until you choose it.
        </Text>
      </Alert>

      <Select
        label="Sub-flow"
        placeholder={isLoading ? 'Loading…' : subflows.length ? 'Choose a sub-flow' : 'No sub-flows in this vhost yet'}
        leftSection={<IconBoxMultiple size="1rem" />}
        data={subflows.map((w) => ({ value: w.id, label: w.name || w.id }))}
        value={config.subflow_id || null}
        onChange={(value) => {
          const w = subflows.find((s) => s.id === value);
          updateNodeConfig(nodeId, {
            subflow_id: value || '', subflow_name: w?.name || '', version: undefined, returns: [], inputs: [],
          });
        }}
        searchable
        nothingFoundMessage="No sub-flow by that name"
        description="A sub-flow is a workflow that starts at a Sub-flow input step."
        required
      />
      {config.subflow_id && (
        <Anchor href={`/workflows/${config.subflow_id}`} target="_blank" size="sm">
          <Group gap={4}>Open the sub-flow <IconExternalLink size="0.9rem" /></Group>
        </Anchor>
      )}

      {config.subflow_id && (
        <Select
          label="Version"
          placeholder="Pick a saved version"
          data={[
            ...[...versions].sort((a, b) => b.version - a.version).map((v) => ({
              value: String(v.version),
              label: v.message ? `v${v.version} · ${v.message}` : `v${v.version}`,
            })),
            { value: 'latest', label: versionLabel('latest') },
          ]}
          value={config.version == null ? null : String(config.version)}
          onChange={pickVersion}
          description="Each save of the sub-flow is a version. A pinned version never changes under you."
          required
        />
      )}

      {pinnedNodes && (
        <Stack gap="xs">
          <Text size="sm" fw={500}>Inputs</Text>
          {fields.length === 0 && <Text size="sm" c="dimmed">This sub-flow declares no inputs.</Text>}
          {fields.map((f) => (
            <Group key={f.name} gap="xs" wrap="nowrap" align="flex-end">
              <Autocomplete
                style={{ flex: 1 }}
                aria-label={`Field for ${f.name}`}
                placeholder="customer.zip"
                data={fieldPaths}
                value={inputs.find((m) => m.to === f.name)?.from ?? ''}
                onChange={(v) => setInput(f.name, v)}
                error={f.required && !inputs.find((m) => m.to === f.name)?.from ? 'Required' : undefined}
              />
              <IconArrowRight size="1rem" style={{ marginBottom: 10 }} />
              <Text size="sm" ff="monospace" style={{ flex: 1, marginBottom: 8 }}>
                {f.name}{f.required ? ' *' : ''}
              </Text>
            </Group>
          ))}

          <Text size="sm" fw={500} mt="sm">Outputs</Text>
          <Text size="xs" c="dimmed">
            With no outputs, the whole result is added under <code>subflow</code>.
          </Text>
          {outputs.map((m, i) => (
            <Group key={i} gap="xs" wrap="nowrap">
              <Autocomplete
                style={{ flex: 1 }}
                aria-label="Result field"
                placeholder="normalized"
                data={[]}
                value={m.from}
                onChange={(v) => setOutput(i, { from: v })}
              />
              <IconArrowRight size="1rem" />
              <Autocomplete
                style={{ flex: 1 }}
                aria-label="Field to write"
                placeholder="customer.address"
                data={fieldPaths}
                value={m.to}
                onChange={(v) => setOutput(i, { to: v })}
              />
              <ActionIcon
                variant="subtle"
                color="red"
                aria-label="Remove output"
                onClick={() => updateNodeConfig(nodeId, { outputs: outputs.filter((_, j) => j !== i) })}
              >
                <IconTrash size="1rem" />
              </ActionIcon>
            </Group>
          ))}
          <Button
            variant="light"
            size="xs"
            leftSection={<IconPlus size="0.9rem" />}
            onClick={() => updateNodeConfig(nodeId, { outputs: [...outputs, { from: '', to: '' }] })}
            style={{ alignSelf: 'flex-start' }}
          >
            Add output
          </Button>
        </Stack>
      )}
    </Stack>
  );
}
