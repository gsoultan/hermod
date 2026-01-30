import { useState } from 'react';
import { 
  Container, Title, Button, Group, Table, ActionIcon, Text, Badge, Paper, 
  Stack, TextInput, Pagination, Tooltip, Modal, JsonInput
} from '@mantine/core';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { 
  IconPlus, IconTrash, IconEdit, IconSearch, IconGitBranch, IconPlayerPlay, 
  IconPlayerStop, IconCopy, IconDownload, IconActivity, IconHierarchy
} from '@tabler/icons-react';
import { lazy, Suspense } from 'react'
import { Link } from '@tanstack/react-router';
import { apiFetch } from '../api';
import { notifications } from '@mantine/notifications';
import { useDisclosure } from '@mantine/hooks';
import { useVHost } from '../context/VHostContext';

const API_BASE = '/api';

const TemplatesModal = lazy(() => import('./WorkflowsPage_TemplatesModal'))

export default function WorkflowsPage() {
  const queryClient = useQueryClient();
  const { selectedVHost, availableVHosts } = useVHost();
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [importOpened, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [templatesOpened, { open: openTemplates, close: closeTemplates }] = useDisclosure(false);
  const [importJson, setImportJson] = useState('');

  const { data: workflowsResponse, isLoading } = useQuery<any>({
    queryKey: ['workflows', activePage, search, selectedVHost],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workflows?page=${activePage}&limit=${itemsPerPage}&search=${search}&vhost=${selectedVHost}`);
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
      if (!data.vhost && selectedVHost) {
        data.vhost = selectedVHost === 'all' ? (availableVHosts[0] || 'default') : selectedVHost;
      }
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
          <Suspense fallback={<Text size="sm">Loading templates…</Text>}>
            <TemplatesModal 
              onUseTemplate={(data) => {
                importMutation.mutate(JSON.stringify(data))
                closeTemplates()
              }}
            />
          </Suspense>
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
                ) : (Array.isArray(workflows) ? workflows : []).map((wf: any) => (
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
                            aria-label={wf.active ? 'Stop workflow' : 'Start workflow'}
                            variant="subtle" 
                            color={wf.active ? 'orange' : 'green'}
                            onClick={() => toggleMutation.mutate({ id: wf.id, active: wf.active })}
                          >
                            {wf.active ? <IconPlayerStop size="1rem" /> : <IconPlayerPlay size="1rem" />}
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="View Details & Logs">
                          <ActionIcon aria-label="View details and logs" component={Link} to="/workflows/$id" params={{ id: wf.id } as any} variant="subtle" color="blue">
                            <IconActivity size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Edit Graph">
                          <ActionIcon aria-label="Edit workflow graph" component={Link} to="/workflows/$id/edit" params={{ id: wf.id } as any} variant="subtle" color="blue">
                            <IconEdit size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Clone">
                          <ActionIcon 
                            aria-label="Clone workflow"
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
                            aria-label="Delete workflow"
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
