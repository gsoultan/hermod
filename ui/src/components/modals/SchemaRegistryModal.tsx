import { useState } from 'react';
import { 
  Modal, Stack, Group, Text, Button, Table, Badge, ScrollArea, TextInput, 
  Select, Textarea, ThemeIcon, Alert, Tabs, Title
} from '@mantine/core';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { formatDateTime } from '@/utils/dateUtils';
import { IconAlertCircle, IconCheck, IconCloudUpload, IconCode, IconDatabase, IconHistory, IconPlus, IconRefresh } from '@tabler/icons-react';
import type { LineageImpact, LineageNode } from '@/lib/lineage';
import { ImpactPanel } from '@/pages/monitoring/lineage/ImpactPanel';

/** POST /api/ai/analyze-schema: the AI verdict, and what is downstream of the workflow. */
interface SchemaAnalysis {
  breaking: boolean;
  suggestion: string;
  impacted_nodes?: string[];
  downstream?: Omit<LineageImpact, 'vhost'>;
}

/** The schema a textarea holds, or an error naming it. */
function parseSchema(form: FormData, field: string, label: string): Record<string, unknown> {
  try {
    return JSON.parse(String(form.get(field) ?? ''));
  } catch {
    throw new Error(`${label} is not valid JSON`);
  }
}

/**
 * The sinks and workflows a change to the workflow's data reaches. The server
 * leaves it out for an unsaved workflow or one the caller may not see.
 */
function Downstream({ analysis, workflowId }: { analysis: SchemaAnalysis; workflowId?: string }) {
  const impact = analysis.downstream;
  if (!workflowId) {
    return <Text size="sm" c="dimmed">Save the workflow to see what is downstream of it.</Text>;
  }
  if (!impact) return null;
  if (impact.impacted.length === 0) {
    return <Text size="sm" c="dimmed">No sink or workflow downstream of {impact.root.label} is known to Hermod.</Text>;
  }
  const labels = new Map<string, LineageNode>([[impact.root.id, impact.root]]);
  for (const it of impact.impacted) labels.set(it.node.id, it.node);
  return <ImpactPanel impact={{ vhost: '', ...impact }} loading={false} error={null} labels={labels} />;
}

export function SchemaRegistryModal({ opened, onClose, workflowId }: {
  opened: boolean;
  onClose: () => void;
  /** The saved workflow the editor has open; unset for a new one. */
  workflowId?: string;
}) {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<string | null>('list');
  const [selectedSchema, setSelectedSchema] = useState<string | null>(null);
  const [evolutionAnalysis, setEvolutionAnalysis] = useState<SchemaAnalysis | null>(null);

  const { data: schemas, isLoading } = useQuery({
    queryKey: ['schemas'],
    queryFn: async () => {
      const res = await apiFetch('/api/schemas');
      return res.json();
    },
    enabled: opened
  });

  const { data: history } = useQuery({
    queryKey: ['schemas', selectedSchema],
    queryFn: async () => {
      const res = await apiFetch(`/api/schemas/${selectedSchema}`);
      return res.json();
    },
    enabled: !!selectedSchema
  });

  const registerMutation = useMutation({
    mutationFn: async (values: any) => {
      const res = await apiFetch('/api/schemas', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values)
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['schemas'] });
      setActiveTab('list');
    }
  });

  const analyzeEvolutionMutation = useMutation({
    mutationFn: async (form: FormData): Promise<SchemaAnalysis> => {
      const body = {
        ...(workflowId ? { workflow_id: workflowId } : {}),
        old_schema: parseSchema(form, 'old_schema', 'Old schema'),
        new_schema: parseSchema(form, 'new_schema', 'New schema'),
      };
      const res = await apiFetch('/api/ai/analyze-schema', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        silent: true
      });
      return res.json();
    },
    onMutate: () => setEvolutionAnalysis(null),
    onSuccess: (data) => {
      setEvolutionAnalysis(data);
    }
  });

  return (
    <Modal 
      opened={opened} 
      onClose={onClose} 
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="blue">
            <IconDatabase size="1.2rem" />
          </ThemeIcon>
          <Text fw={700}>Global Schema Registry</Text>
        </Group>
      }
      size="xl"
    >
      <Tabs value={activeTab} onChange={setActiveTab}>
        <Tabs.List>
          <Tabs.Tab value="list" leftSection={<IconCode size="1rem" />}>Schemas</Tabs.Tab>
          <Tabs.Tab value="register" leftSection={<IconPlus size="1rem" />}>Register New</Tabs.Tab>
          <Tabs.Tab value="evolution" leftSection={<IconRefresh size="1rem" />}>AI Evolution</Tabs.Tab>
          {selectedSchema && <Tabs.Tab value="history" leftSection={<IconHistory size="1rem" />}>History: {selectedSchema}</Tabs.Tab>}
        </Tabs.List>

        <Tabs.Panel value="list" pt="md">
          <ScrollArea h={400}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Type</Table.Th>
                  <Table.Th>Latest Version</Table.Th>
                  <Table.Th>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {isLoading ? (
                  <Table.Tr><Table.Td colSpan={4} align="center">Loading...</Table.Td></Table.Tr>
                ) : (schemas || []).map((s: any) => (
                  <Table.Tr key={s.id}>
                    <Table.Td><Text fw={500}>{s.name}</Text></Table.Td>
                    <Table.Td><Badge variant="light">{s.type}</Badge></Table.Td>
                    <Table.Td>v{s.version}</Table.Td>
                    <Table.Td>
                      <Button variant="subtle" size="xs" onClick={() => {
                        setSelectedSchema(s.name);
                        setActiveTab('history');
                      }}>View History</Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="register" pt="md">
          <form onSubmit={(e) => {
            e.preventDefault();
            const formData = new FormData(e.currentTarget);
            registerMutation.mutate({
              name: formData.get('name'),
              type: formData.get('type'),
              content: formData.get('content')
            });
          }}>
            <Stack gap="sm">
              <TextInput label="Schema Name" name="name" required placeholder="user_events" />
              <Select label="Type" name="type" required defaultValue="json" data={['json', 'avro', 'protobuf']} />
              <Textarea label="Schema Content" name="content" required placeholder='{"type": "object", ...}' minRows={10} styles={{ input: { fontFamily: 'monospace' } }} />
              <Button type="submit" loading={registerMutation.isPending} leftSection={<IconCloudUpload size="1rem" />}>Register Schema</Button>
              {registerMutation.error && <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{String(registerMutation.error)}</Alert>}
            </Stack>
          </form>
        </Tabs.Panel>

        <Tabs.Panel value="evolution" pt="md">
          <form onSubmit={(e) => {
            e.preventDefault();
            analyzeEvolutionMutation.mutate(new FormData(e.currentTarget));
          }}>
            <Stack gap="sm">
              <Group grow>
                <Textarea label="Old Schema (JSON)" name="old_schema" required minRows={8} styles={{ input: { fontFamily: 'monospace' } }} />
                <Textarea label="New Schema (JSON)" name="new_schema" required minRows={8} styles={{ input: { fontFamily: 'monospace' } }} />
              </Group>
              <Button type="submit" loading={analyzeEvolutionMutation.isPending} leftSection={<IconRefresh size="1rem" />}>Analyze Impact</Button>
              
              {evolutionAnalysis && (
                <Alert color={evolutionAnalysis.breaking ? 'red' : 'green'} icon={evolutionAnalysis.breaking ? <IconAlertCircle size="1rem" /> : <IconCheck size="1rem" />}>
                  <Text fw={700}>{evolutionAnalysis.breaking ? 'Breaking Changes Detected' : 'Safe to Proceed'}</Text>
                  <Text size="sm" mt="xs">{evolutionAnalysis.suggestion}</Text>
                    {(evolutionAnalysis.impacted_nodes?.length ?? 0) > 0 && (
                    <Text size="xs" mt="xs" color="dimmed">Impacted Nodes: {evolutionAnalysis.impacted_nodes?.join(', ')}</Text>
                  )}
                </Alert>
              )}
              {analyzeEvolutionMutation.error && (
                <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{analyzeEvolutionMutation.error.message}</Alert>
              )}
              {evolutionAnalysis && (
                <Stack gap="xs">
                  <Title order={5}>Affected downstream</Title>
                  <Downstream analysis={evolutionAnalysis} workflowId={workflowId} />
                </Stack>
              )}
            </Stack>
          </form>
        </Tabs.Panel>

        <Tabs.Panel value="history" pt="md">
          <ScrollArea h={400}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Version</Table.Th>
                  <Table.Th>Registered At</Table.Th>
                  <Table.Th>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(history || []).map((h: any) => (
                  <Table.Tr key={h.id}>
                    <Table.Td>v{h.version}</Table.Td>
                    <Table.Td>{formatDateTime(h.created_at)}</Table.Td>
                    <Table.Td>
                      <Button variant="subtle" size="xs">Copy Definition</Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Tabs.Panel>
      </Tabs>
    </Modal>
  );
}


