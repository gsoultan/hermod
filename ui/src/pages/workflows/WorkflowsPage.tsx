import { useState } from 'react';
import { 
  Container, Title, Button, Group, Table, ActionIcon, Text, Badge, Paper, 
  Stack, TextInput, Pagination, Tooltip, Modal, JsonInput, Select, Menu, Checkbox
} from '@mantine/core';
import { keepPreviousData, useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { lazy, Suspense } from 'react'
import { Link } from '@tanstack/react-router';
import type { Workflow, Worker, Workspace } from '@/types';
import { apiFetch } from '@/api';
import { notifications } from '@mantine/notifications';
import { useDisclosure, useDebouncedValue } from '@mantine/hooks';
import { useVHost } from '@/context/VHostContext';
import { IconActivity, IconChevronDown, IconCopy, IconDownload, IconEdit, IconFolder, IconGitBranch, IconHierarchy, IconPlayerPlay, IconPlayerStop, IconPlus, IconSearch, IconTrash, IconUpload } from '@tabler/icons-react';
import { useConfirm } from '@/components/common/ConfirmProvider';
const API_BASE = '/api';

const TemplatesModal = lazy(() => import('./WorkflowsPage_TemplatesModal'))

export default function WorkflowsPage() {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const { selectedVHost, availableVHosts } = useVHost();
  const [search, setSearch] = useState('');
  // Debounced so a burst of keystrokes costs one request, and used as the query
  // key so the key changes once per search rather than once per character.
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [selectedWorkspace, setSelectedWorkspace] = useState<string>('all');
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const [importOpened, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [templatesOpened, { open: openTemplates, close: closeTemplates }] = useDisclosure(false);
  const [importJson, setImportJson] = useState('');

  const { data: workspacesResponse } = useQuery<Workspace[]>({
    queryKey: ['workspaces'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workspaces`);
      return res.json();
    }
  });

  const { data: workflowsResponse, isLoading } = useQuery<{ data: Workflow[], total: number }>({
    queryKey: ['workflows', activePage, debouncedSearch, selectedVHost, selectedWorkspace],
        // Hold the previous page/search on screen while the next one loads,
        // instead of dropping to undefined and blanking the table.
        placeholderData: keepPreviousData,
    queryFn: async () => {
      let url = `${API_BASE}/workflows?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(debouncedSearch)}&vhost=${selectedVHost}`;
      if (selectedWorkspace !== 'all') {
        url += `&workspace_id=${selectedWorkspace}`;
      }
      const res = await apiFetch(url);
      return res.json();
    }
  });

  const { data: workersResponse } = useQuery<{ data: Worker[], total: number }>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      return res.json();
    }
  });

  const workflows = workflowsResponse?.data || [];
  const totalItems = workflowsResponse?.total || 0;
  const workers = workersResponse?.data || [];
  // /api/workspaces answers with a bare array, but an error envelope or a future
  // {data,total} shape would land here too and take the page down with an
  // uncaught "workspaces.map is not a function". A workspace filter is not worth
  // a blank screen.
  const workspaces = Array.isArray(workspacesResponse) ? workspacesResponse : [];

  const workspaceOptions = [
    { value: 'all', label: 'All Workspaces' },
    ...workspaces.map((ws: Workspace) => ({ value: ws.id, label: ws.name }))
  ];

  const getWorkspaceName = (id: string) => {
    const ws = workspaces.find((w: Workspace) => w.id === id);
    return ws ? ws.name : null;
  };

  const getWorkerName = (id: string) => {
    const worker = workers.find((w: Worker) => w.id === id);
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
    mutationFn: async (wf: Workflow) => {
      // Strip the identity and runtime fields; the three names exist only to be
      // omitted from `clone`.
      // eslint-disable-next-line no-unused-vars
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

  const handleExport = async (wf: Workflow) => {
    try {
      const res = await apiFetch(`${API_BASE}/workflows/${wf.id}/export`);
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `workflow-${wf.name}.json`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
      notifications.show({ title: 'Success', message: 'Workflow exported successfully', color: 'green' });
    } catch (err: any) {
      notifications.show({ title: 'Export Failed', message: err.message, color: 'red' });
    }
  };

  const handleFileUpload = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (file) {
      const reader = new FileReader();
      reader.onload = (e) => {
        const content = e.target?.result as string;
        setImportJson(content);
      };
      reader.readAsText(file);
    }
  };

  const importMutation = useMutation({
    mutationFn: async (json: string) => {
      let data;
      try {
        data = JSON.parse(json);
      } catch {
        throw new Error("Invalid JSON");
      }

      // Support patching vhost if missing from the workflow in the bundle or the single workflow
      const workflow = data.workflow || data;
      if (!workflow.vhost && selectedVHost) {
        workflow.vhost = selectedVHost === 'all' ? (availableVHosts[0] || 'default') : selectedVHost;
      }

      const res = await apiFetch(`${API_BASE}/workflows/import`, {
        method: 'POST',
        body: JSON.stringify(data),
        silent: true,
      });
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      notifications.show({ title: 'Success', message: 'Workflow imported successfully', color: 'green' });
      closeImport();
      setImportJson('');
    },
    onError: (err: any) => {
      notifications.show({ 
        id: 'workflow-import-error',
        title: 'Import Failed', 
        message: err.message, 
        color: 'red' 
      });
    }
  });

  const batchToggleMutation = useMutation({
    mutationFn: async ({ ids, active }: { ids: string[], active: boolean }) => {
      await apiFetch(`${API_BASE}/workflows/batch/toggle`, {
        method: 'POST',
        body: JSON.stringify({ ids, active })
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setSelectedIDs([]);
      notifications.show({ title: 'Success', message: 'Batch operation completed', color: 'green' });
    }
  });

  const batchDeleteMutation = useMutation({
    mutationFn: async (ids: string[]) => {
      await apiFetch(`${API_BASE}/workflows/batch/delete`, {
        method: 'POST',
        body: JSON.stringify({ ids })
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setSelectedIDs([]);
      notifications.show({ title: 'Success', message: 'Workflows deleted', color: 'green' });
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

  const allSelected = workflows.length > 0 && selectedIDs.length === workflows.length;

  return (
    <Container size="xl">
      <Stack gap="lg">
        <Group justify="space-between">
          <Group>
            <IconGitBranch size="2rem" color="var(--mantine-color-indigo-6)" />
            <Title order={2}>Workflows</Title>
          </Group>
          <Group>
            {selectedIDs.length > 0 && (
              <Menu shadow="md" width={200}>
                <Menu.Target>
                  <Button variant="outline" color="blue" rightSection={<IconChevronDown size="1rem" />}>
                    Batch Actions ({selectedIDs.length})
                  </Button>
                </Menu.Target>

                <Menu.Dropdown>
                  <Menu.Label>Lifecycle</Menu.Label>
                  <Menu.Item 
                    leftSection={<IconPlayerPlay size="1rem" />} 
                    onClick={() => batchToggleMutation.mutate({ ids: selectedIDs, active: true })}
                  >
                    Start Selected
                  </Menu.Item>
                  <Menu.Item 
                    leftSection={<IconPlayerStop size="1rem" />}
                    onClick={() => batchToggleMutation.mutate({ ids: selectedIDs, active: false })}
                  >
                    Stop Selected
                  </Menu.Item>
                  <Menu.Divider />
                  <Menu.Item 
                    color="red" 
                    leftSection={<IconTrash size="1rem" />}
                    onClick={async () => {
                      if (await confirm({ title: `Delete ${selectedIDs.length} workflows`, message: `Permanently delete ${selectedIDs.length} selected workflow(s)?`, consequence: 'Running workflows are stopped. This cannot be undone.', confirmLabel: 'Delete all', danger: true })) {
                        batchDeleteMutation.mutate(selectedIDs);
                      }
                    }}
                  >
                    Delete Selected
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
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
            <Group justify="space-between">
              <Text size="sm">Paste the Workflow JSON configuration below or upload a file.</Text>
              <Button variant="subtle" component="label" size="xs" leftSection={<IconUpload size="1rem" />}>
                Upload File
                <input type="file" hidden accept=".json" onChange={handleFileUpload} />
              </Button>
            </Group>
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

        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Stack gap="md">
          <Group grow>
            <TextInput 
              placeholder="Search workflows..." 
              leftSection={<IconSearch size="1rem" />} 
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setPage(1);
              }}
            />
            <Select
              placeholder="Workspace"
              data={workspaceOptions}
              value={selectedWorkspace}
              onChange={(val) => {
                setSelectedWorkspace(val || 'all');
                setPage(1);
              }}
              leftSection={<IconFolder size="1rem" />}
            />
          </Group>

            <Table.ScrollContainer minWidth={700}>
              <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th style={{ width: 40 }}>
                    <Checkbox 
                      aria-label="Select all workflows"
                      checked={allSelected}
                      indeterminate={selectedIDs.length > 0 && !allSelected}
                      onChange={(e) => {
                        if (e.currentTarget.checked) {
                          setSelectedIDs(workflows.map((wf: any) => wf.id));
                        } else {
                          setSelectedIDs([]);
                        }
                      }}
                    />
                  </Table.Th>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Workspace</Table.Th>
                  <Table.Th>Virtual Host</Table.Th>
                  <Table.Th>Worker</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th>Graph</Table.Th>
                  {/* Six icon buttons wrapped onto two rows at 150px. */}
                  <Table.Th style={{ width: 210 }}>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {isLoading ? (
                  <Table.Tr><Table.Td colSpan={7}><Text ta="center" py="xl" c="dimmed">Loading workflows...</Text></Table.Td></Table.Tr>
                ) : workflows?.length === 0 ? (
                  <Table.Tr><Table.Td colSpan={7}><Text ta="center" py="xl" c="dimmed">{search ? 'No workflows match your search' : 'No workflows found'}</Text></Table.Td></Table.Tr>
                ) : workflows.map((wf: Workflow) => (
                  <Table.Tr key={wf.id} bg={selectedIDs.includes(wf.id) ? 'var(--mantine-color-blue-light)' : undefined}>
                    <Table.Td>
                      <Checkbox 
                        aria-label={`Select workflow ${wf.name}`}
                        checked={selectedIDs.includes(wf.id)}
                        onChange={(e) => {
                          if (e.currentTarget.checked) {
                            setSelectedIDs([...selectedIDs, wf.id]);
                          } else {
                            setSelectedIDs(selectedIDs.filter(id => id !== wf.id));
                          }
                        }}
                      />
                    </Table.Td>
                    <Table.Td>
                      <Link to="/workflows/$id" params={{ id: wf.id } as any} style={{ textDecoration: 'none', color: 'inherit' }}>
                        <Text fw={600} style={{ cursor: 'pointer' }}>{wf.name}</Text>
                      </Link>
                    </Table.Td>
                    <Table.Td>
                      {wf.workspace_id ? (
                        <Badge variant="light" color="blue" leftSection={<IconFolder size="0.7rem" />}>
                          {getWorkspaceName(wf.workspace_id) || wf.workspace_id}
                        </Badge>
                      ) : (
                        <Text size="xs" c="dimmed">Default</Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="dot" color="indigo">{wf.vhost || 'default'}</Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">{wf.worker_id ? getWorkerName(wf.worker_id) : <Text span c="dimmed" fs="italic">Auto Sharded</Text>}</Text>
                    </Table.Td>
                    {/* Nine columns left these cells too narrow for their own
                        contents at 1440px: the status read "ACT…", the counts
                        "3 NO…" and "2 ED…". The two count columns say one thing
                        between them, so they are one column, and the badges are
                        told not to shrink below their text. */}
                    <Table.Td style={{ whiteSpace: 'nowrap' }}>
                      <Tooltip label={wf.status || (wf.active ? 'Active' : 'Inactive')} disabled={!wf.status}>
                        <Badge variant="light" color={wf.active ? 'green' : 'gray'} style={{ flexShrink: 0 }}>
                          {wf.active ? 'Active' : 'Inactive'}
                        </Badge>
                      </Tooltip>
                      {/* The engine's own status string goes in the badge's
                          tooltip rather than under it: stacking "running"
                          beneath a green "Active" badge repeated what the badge
                          already said while squeezing the column so the badge
                          itself rendered as "ACT…". */}
                    </Table.Td>
                    <Table.Td style={{ whiteSpace: 'nowrap' }}>
                      <Text size="sm" c="dimmed">
                        {wf.nodes?.length || 0} nodes · {wf.edges?.length || 0} edges
                      </Text>
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
                        <Tooltip label="Export JSON">
                          <ActionIcon 
                            aria-label="Export workflow to JSON"
                            variant="subtle" 
                            color="gray" 
                            onClick={() => handleExport(wf)}
                          >
                            <IconDownload size="1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Delete">
                          <ActionIcon 
                            aria-label="Delete workflow"
                            variant="subtle" 
                            color="red" 
                            onClick={async () => {
                              if (await confirm({ title: 'Delete workflow', message: 'Permanently delete this workflow?', consequence: 'If it is running it will be stopped. This cannot be undone.', confirmLabel: 'Delete', danger: true })) {
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
            </Table.ScrollContainer>
            {totalPages > 1 && (
              <Group justify="center" p="md" bg="var(--mantine-color-body)" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
                <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
              </Group>
            )}
          </Stack>
        </Paper>
      </Stack>
    </Container>
  );
}


