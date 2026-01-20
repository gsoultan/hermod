import { useState } from 'react';
import { 
  Container, Title, Button, Group, Table, ActionIcon, Text, Badge, Paper, 
  Stack, TextInput, Pagination, Tooltip, Modal, JsonInput, SimpleGrid, ThemeIcon,
  Card
} from '@mantine/core';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { 
  IconPlus, IconTrash, IconEdit, IconSearch, IconGitBranch, IconPlayerPlay, 
  IconPlayerStop, IconCopy, IconDownload, IconActivity, IconHierarchy, IconSend
} from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import { apiFetch } from '../api';
import { notifications } from '@mantine/notifications';
import { useDisclosure } from '@mantine/hooks';

const API_BASE = '/api';

const TEMPLATES = [
  {
    name: 'World Case: GDPR Masking & High-Value Routing',
    description: 'Protect PII and route high-value orders. Monitors PostgreSQL CDC, masks emails, maps status codes, and routes based on amount.',
    icon: IconGitBranch,
    color: 'blue',
    data: {
      "name": "World Case: GDPR Masking & High-Value Routing",
      "nodes": [
        { "id": "node_postgres", "type": "source", "config": { "label": "PostgreSQL CDC (Orders)", "type": "postgres", "ref_id": "new" }, "x": 100, "y": 250 },
        { "id": "node_transform", "type": "transformation", "config": { "label": "GDPR & Format", "transType": "pipeline", "steps": "[{\"transType\": \"mask\", \"field\": \"customer_email\", \"maskType\": \"email\"}, {\"transType\": \"mapping\", \"field\": \"status_id\", \"mapping\": \"{\\\"1\\\": \\\"PENDING\\\", \\\"2\\\": \\\"PAID\\\", \\\"3\\\": \\\"CANCELLED\\\"}\"}, {\"transType\": \"set\", \"column.processed_by\": \"Hermod-Worker-01\"}]" }, "x": 400, "y": 250 },
        { "id": "node_condition", "type": "condition", "config": { "label": "High Value? (> 1000)", "field": "total_amount", "operator": ">", "value": "1000" }, "x": 700, "y": 250 },
        { "id": "node_set_priority", "type": "transformation", "config": { "label": "Mark Priority", "transType": "set", "column.priority": "true" }, "x": 950, "y": 150 },
        { "id": "node_telegram", "type": "sink", "config": { "label": "Telegram Alert", "type": "telegram", "ref_id": "new" }, "x": 1200, "y": 100 },
        { "id": "node_kafka", "type": "sink", "config": { "label": "Kafka High Priority", "type": "kafka", "ref_id": "new" }, "x": 1200, "y": 200 },
        { "id": "node_mongodb", "type": "sink", "config": { "label": "MongoDB Archive", "type": "mongodb", "ref_id": "new" }, "x": 1200, "y": 400 }
      ],
      "edges": [
        { "id": "edge_1", "source_id": "node_postgres", "target_id": "node_transform" },
        { "id": "edge_2", "source_id": "node_transform", "target_id": "node_condition" },
        { "id": "edge_3", "source_id": "node_condition", "target_id": "node_set_priority", "config": { "label": "true" } },
        { "id": "edge_4", "source_id": "node_set_priority", "target_id": "node_telegram" },
        { "id": "edge_5", "source_id": "node_set_priority", "target_id": "node_kafka" },
        { "id": "edge_6", "source_id": "node_condition", "target_id": "node_mongodb", "config": { "label": "false" } }
      ]
    }
  },
  {
    "name": "HTTP Webhook to Slack",
    "description": "Receive data via HTTP Webhook and forward it to Slack/Discord.",
    "icon": IconSend,
    "color": "green",
    "data": {
      "name": "Webhook to Slack",
      "nodes": [
        { "id": "n1", "type": "source", "config": { "label": "Incoming Webhook", "type": "webhook", "ref_id": "new" }, "x": 100, "y": 100 },
        { "id": "n2", "type": "sink", "config": { "label": "Slack Webhook", "type": "http", "ref_id": "new" }, "x": 400, "y": 100 }
      ],
      "edges": [
        { "id": "e1", "source_id": "n1", "target_id": "n2" }
      ]
    }
  }
];

export default function WorkflowsPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [importOpened, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [templatesOpened, { open: openTemplates, close: closeTemplates }] = useDisclosure(false);
  const [importJson, setImportJson] = useState('');

  const { data: workflowsResponse, isLoading } = useQuery<any>({
    queryKey: ['workflows', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workflows?page=${activePage}&limit=${itemsPerPage}&search=${search}`);
      return res.json();
    }
  });

  const { data: workersResponse } = useQuery<any>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      return res.json();
    }
  });

  const workflows = workflowsResponse?.data || [];
  const totalItems = workflowsResponse?.total || 0;
  const workers = workersResponse?.data || [];

  const getWorkerName = (id: string) => {
    const worker = workers.find((w: any) => w.id === id);
    return worker ? worker.name : id;
  };

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`${API_BASE}/workflows/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const cloneMutation = useMutation({
    mutationFn: async (wf: any) => {
      const { id, status, active, ...clone } = wf;
      clone.name = `${clone.name} (Copy)`;
      await apiFetch(`${API_BASE}/workflows`, {
        method: 'POST',
        body: JSON.stringify(clone)
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const importMutation = useMutation({
    mutationFn: async (json: string) => {
      const data = JSON.parse(json);
      await apiFetch(`${API_BASE}/workflows`, {
        method: 'POST',
        body: JSON.stringify(data)
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      notifications.show({ title: 'Success', message: 'Workflow imported successfully', color: 'green' });
      closeImport();
      setImportJson('');
    },
    onError: (err: any) => {
      notifications.show({ title: 'Import Failed', message: err.message, color: 'red' });
    }
  });

  const toggleMutation = useMutation({
    mutationFn: async ({ id }: { id: string; active: boolean }) => {
       await apiFetch(`${API_BASE}/workflows/${id}/toggle`, {
         method: 'POST'
       });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  return (
    <Container size="xl">
      <Stack gap="lg">
        <Group justify="space-between">
          <Group>
            <IconGitBranch size="2rem" color="var(--mantine-color-indigo-6)" />
            <Title order={2}>Workflows</Title>
          </Group>
          <Group>
            <Button variant="light" color="indigo" onClick={openTemplates} leftSection={<IconHierarchy size="1rem" />}>
              Sample Library
            </Button>
            <Button variant="light" color="gray" onClick={openImport} leftSection={<IconDownload size="1rem" />}>
              Import JSON
            </Button>
            <Button component={Link} to="/workflows/new" leftSection={<IconPlus size="1rem" />}>
              Create Workflow
            </Button>
          </Group>
        </Group>

        <Modal opened={templatesOpened} onClose={closeTemplates} title="Workflow Sample Library" size="xl">
          <Stack>
            <Text size="sm" c="dimmed">Choose a pre-built template to jumpstart your workflow development.</Text>
            <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
              {TEMPLATES.map((template, index) => (
                <Card key={index} withBorder shadow="sm" radius="md" padding="lg">
                  <Stack gap="sm">
                    <Group justify="space-between">
                      <ThemeIcon size="lg" radius="md" variant="light" color={template.color}>
                        <template.icon size="1.2rem" />
                      </ThemeIcon>
                      <Button 
                        size="xs" 
                        variant="light" 
                        color={template.color}
                        onClick={() => {
                          importMutation.mutate(JSON.stringify(template.data));
                          closeTemplates();
                        }}
                      >
                        Use Template
                      </Button>
                    </Group>
                    <Text fw={700}>{template.name}</Text>
                    <Text size="xs" c="dimmed" style={{ minHeight: 40 }}>{template.description}</Text>
                  </Stack>
                </Card>
              ))}
            </SimpleGrid>
          </Stack>
        </Modal>

        <Modal opened={importOpened} onClose={closeImport} title="Import Workflow from JSON" size="lg">
          <Stack>
            <Text size="sm">Paste the Workflow JSON configuration below.</Text>
            <JsonInput 
              placeholder='{ "name": "Imported Workflow", ... }' 
              validationError="Invalid JSON" 
              formatOnBlur 
              autosize 
              minRows={18} 
              maxRows={40}
              value={importJson}
              onChange={setImportJson}
            />
            <Group justify="flex-end">
              <Button variant="outline" color="gray" onClick={closeImport}>Cancel</Button>
              <Button onClick={() => importMutation.mutate(importJson)} loading={importMutation.isPending} disabled={!importJson}>
                Import Workflow
              </Button>
            </Group>
          </Stack>
        </Modal>

        <Paper p="md" withBorder radius="md">
          <Stack gap="md">
            <TextInput 
              placeholder="Search workflows..." 
              leftSection={<IconSearch size="1rem" />} 
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setPage(1);
              }}
            />

            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Virtual Host</Table.Th>
                  <Table.Th>Worker</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th>Nodes</Table.Th>
                  <Table.Th>Edges</Table.Th>
                  <Table.Th style={{ width: 150 }}>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {isLoading ? (
                  <Table.Tr><Table.Td colSpan={7}><Text ta="center" py="xl" c="dimmed">Loading workflows...</Text></Table.Td></Table.Tr>
                ) : workflows?.length === 0 ? (
                  <Table.Tr><Table.Td colSpan={7}><Text ta="center" py="xl" c="dimmed">{search ? 'No workflows match your search' : 'No workflows found'}</Text></Table.Td></Table.Tr>
                ) : workflows?.map((wf: any) => (
                  <Table.Tr key={wf.id}>
                    <Table.Td>
                      <Link to="/workflows/$id" params={{ id: wf.id } as any} style={{ textDecoration: 'none', color: 'inherit' }}>
                        <Text fw={600} style={{ cursor: 'pointer' }}>{wf.name}</Text>
                      </Link>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="dot" color="indigo">{wf.vhost || 'default'}</Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">{wf.worker_id ? getWorkerName(wf.worker_id) : <Text span c="dimmed" fs="italic">Auto Sharded</Text>}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={wf.active ? 'green' : 'gray'}>
                        {wf.active ? 'Active' : 'Inactive'}
                      </Badge>
                      {wf.status && (
                        <Text size="xs" c="dimmed" mt={4}>{wf.status}</Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="outline">{wf.nodes?.length || 0} nodes</Badge>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="outline">{wf.edges?.length || 0} edges</Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end">
                        <Tooltip label={wf.active ? 'Stop' : 'Start'}>
                          <ActionIcon 
                            variant="subtle" 
                            color={wf.active ? 'orange' : 'green'}
                            onClick={() => toggleMutation.mutate({ id: wf.id, active: wf.active })}
                          >
                            {wf.active ? <IconPlayerStop size="1rem" /> : <IconPlayerPlay size="1rem" />}
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="View Details & Logs">
                          <ActionIcon component={Link} to="/workflows/$id" params={{ id: wf.id } as any} variant="subtle" color="blue">
                            <IconActivity size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Edit Graph">
                          <ActionIcon component={Link} to="/workflows/$id/edit" params={{ id: wf.id } as any} variant="subtle" color="blue">
                            <IconEdit size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Clone">
                          <ActionIcon 
                            variant="subtle" 
                            color="gray" 
                            onClick={() => cloneMutation.mutate(wf)}
                            loading={cloneMutation.isPending}
                          >
                            <IconCopy size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Delete">
                          <ActionIcon 
                            variant="subtle" 
                            color="red" 
                            onClick={() => {
                              if (confirm('Are you sure you want to delete this workflow?')) {
                                deleteMutation.mutate(wf.id);
                              }
                            }}
                          >
                            <IconTrash size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            {totalPages > 1 && (
              <Group justify="center" p="md" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
                <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
              </Group>
            )}
          </Stack>
        </Paper>
      </Stack>
    </Container>
  );
}
