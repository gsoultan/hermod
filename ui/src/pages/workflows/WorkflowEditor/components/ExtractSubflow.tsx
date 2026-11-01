import { useMemo, useState } from 'react';
import { Alert, Button, Group, Modal, Stack, TagsInput, Text, TextInput } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconBoxMultiple, IconInfoCircle } from '@tabler/icons-react';
import { useQueryClient } from '@tanstack/react-query';
import { useShallow } from 'zustand/react/shallow';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { extractSubflow, guessInputs, type CanvasEdge, type CanvasNode } from '@/lib/subflowExtract';
import { SUBFLOW_INPUT, SUBFLOW_RETURN } from '@/lib/subflow';

const NOT_MOVABLE = new Set(['source', SUBFLOW_INPUT, SUBFLOW_RETURN]);

/** Every field path in a sample record: `customer`, `customer.zip`, … */
function paths(value: unknown, prefix = ''): string[] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
  return Object.entries(value as Record<string, unknown>).flatMap(([k, v]) => {
    const p = prefix ? `${prefix}.${k}` : k;
    return [p, ...paths(v, p)];
  });
}

/**
 * "Turn N steps into a sub-flow", over the canvas while steps are selected.
 * It saves the selected steps as a new sub-flow (version 1) and puts a Run
 * sub-flow step pinned to it in their place. The caller's canvas then has
 * unsaved changes, like any other edit.
 */
export function ExtractSubflowButton() {
  const { nodes, edges, vhost, workspaceID, testResults, setNodes, setEdges } = useWorkflowStore(useShallow((s) => ({
    nodes: s.nodes, edges: s.edges, vhost: s.vhost, workspaceID: s.workspaceID, testResults: s.testResults,
    setNodes: s.setNodes, setEdges: s.setEdges,
  })));
  const queryClient = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [name, setName] = useState('');
  const [inputs, setInputs] = useState<string[]>([]);
  const [outputs, setOutputs] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const selected = nodes.filter((n) => n.selected);
  const steps = selected.filter((n) => n.type !== 'note');
  const movable = steps.length > 0 && !selected.some((n) => NOT_MOVABLE.has(n.type ?? ''));

  // Fields the steps feeding the selection last emitted in a test run, for
  // suggestions and a first guess at the inputs.
  const fields = useMemo(() => {
    const ids = new Set(selected.map((n) => n.id));
    const feeding = new Set(edges.filter((e) => ids.has(e.target) && !ids.has(e.source)).map((e) => e.source));
    const out = new Set<string>();
    for (const r of (testResults as any[] | null) ?? []) {
      if (feeding.has(r?.node_id)) paths(r?.payload).forEach((p) => out.add(p));
    }
    return [...out];
  }, [selected, edges, testResults]);

  if (!movable) return null;

  const open = () => {
    const guess = guessInputs(selected as CanvasNode[], fields);
    setName('');
    setInputs(guess);
    setOutputs(guess);
    setError(null);
    setOpened(true);
  };

  const create = async () => {
    const subflowId = crypto.randomUUID();
    const result = extractSubflow(nodes as CanvasNode[], edges as CanvasEdge[], selected.map((n) => n.id), {
      callId: `call_subflow_${Date.now()}`, subflowId, name: name.trim(), inputs, outputs,
    });
    if ('error' in result) {
      setError(result.error);
      return;
    }
    setSaving(true);
    try {
      const res = await apiFetch('/api/workflows', {
        method: 'POST',
        silent: true,
        body: JSON.stringify({
          id: subflowId, name: name.trim(), vhost, workspace_id: workspaceID,
          nodes: result.subflow.nodes, edges: result.subflow.edges,
        }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        setError(body?.error || body?.message || 'The sub-flow could not be saved.');
        return;
      }
      setNodes(result.nodes as any);
      setEdges(result.edges as any);
      queryClient.invalidateQueries({ queryKey: ['subflows', vhost] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setOpened(false);
      notifications.show({
        title: 'Sub-flow created',
        message: `"${name.trim()}" is saved as version 1. Save this workflow to run it from here.`,
        color: 'green',
      });
    } finally {
      setSaving(false);
    }
  };

  const count = steps.length === 1 ? '1 step' : `${steps.length} steps`;
  return (
    <>
      <Button variant="default" leftSection={<IconBoxMultiple size="1rem" />} onClick={open}>
        Turn {count} into a sub-flow
      </Button>
      <Modal opened={opened} onClose={() => setOpened(false)} title="Turn the selection into a sub-flow" size="lg">
        <Stack gap="md">
          <Text size="sm">
            The {count} move into a new sub-flow, saved as version 1, and a Run sub-flow step pinned to it takes
            their place here. The sub-flow sees only the inputs listed below, so list every field the steps read.
          </Text>
          <TextInput
            label="Sub-flow name"
            placeholder="Clean address"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
            data-autofocus
          />
          <TagsInput
            label="Inputs it needs"
            description="Fields of this workflow the steps read. Each becomes a required input of the same name."
            placeholder="customer.zip"
            data={fields}
            value={inputs}
            onChange={setInputs}
            clearable
          />
          <TagsInput
            label="Fields it returns"
            description="Fields of the sub-flow's result copied back onto this workflow's record. Anything else the steps set stays inside the sub-flow."
            placeholder="customer.zip"
            data={fields}
            value={outputs}
            onChange={setOutputs}
            clearable
          />
          {error && <Alert color="red" icon={<IconInfoCircle size="1rem" />}>{error}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpened(false)}>Cancel</Button>
            <Button onClick={create} loading={saving} disabled={!name.trim()}>Create sub-flow</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
