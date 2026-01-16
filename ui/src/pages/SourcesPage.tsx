import { useEffect, useState } from 'react';
import { Title, Table, Button, Group, ActionIcon, Paper, Text, Box, Stack, Badge, Modal, List, ThemeIcon, TextInput, Pagination } from '@mantine/core';
import { IconTrash, IconPlus, IconDatabaseImport, IconEdit, IconAlertCircle, IconSearch, IconActivity } from '@tabler/icons-react';
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { useDisclosure } from '@mantine/hooks';

const API_BASE = '/api';

export function SourcesPage() {
  const queryClient = useQueryClient();
  const { selectedVHost } = useVHost();
  const role = getRoleFromToken();
  const isViewer = role === 'Viewer';
  const navigate = useNavigate();
  const [opened, { open, close }] = useDisclosure(false);
  const [sourceToDelete, setSourceToDelete] = useState<any>(null);
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;

  const [liveStatuses, setLiveStatuses] = useState<Record<string, any>>({});

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status`;
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const update = JSON.parse(event.data);
        setLiveStatuses(prev => ({
          ...prev,
          [update.connection_id]: update
        }));
      } catch (err) {
        console.error('Failed to parse status update', err);
      }
    };

    return () => ws.close();
  }, []);

  const { data: sourcesResponse } = useSuspenseQuery({
    queryKey: ['sources', activePage, search, selectedVHost],
    queryFn: async () => {
      const vhostParam = selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const res = await apiFetch(`${API_BASE}/sources?page=${activePage}&limit=${itemsPerPage}&search=${search}${vhostParam}`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const sources = (sourcesResponse as any)?.data || [];
  const totalItems = (sourcesResponse as any)?.total || 0;

  const { data: connectionsResponse } = useSuspenseQuery({
    queryKey: ['connections-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/connections?limit=1000`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    },
    refetchInterval: 5000,
  });
  const connections = (connectionsResponse as any)?.data || [];

  const activeConnectionsUsingSource = sourceToDelete 
    ? (connections as any[])?.filter(c => c.source_id === sourceToDelete.id && c.active)
    : [];

  const { data: workersResponse } = useSuspenseQuery({
    queryKey: ['workers-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers?limit=1000`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });
  const workers = (workersResponse as any)?.data || [];

  const getWorkerName = (id: string) => {
    const worker = (workers as any[])?.find(w => w.id === id);
    return worker ? worker.name : id;
  };

  const getSourceLiveStatus = (sourceId: string) => {
    const relevantStatuses = Object.values(liveStatuses).filter((s: any) => s.source_id === sourceId);
    if (relevantStatuses.length === 0) return null;
    
    if (relevantStatuses.some((s: any) => s.source_status === 'error')) return 'error';
    if (relevantStatuses.some((s: any) => s.source_status === 'reconnecting')) return 'reconnecting';
    if (relevantStatuses.some((s: any) => s.source_status === 'connecting')) return 'connecting';
    if (relevantStatuses.some((s: any) => s.source_status === 'running')) return 'running';
    return relevantStatuses[0].source_status;
  };

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`${API_BASE}/sources/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sources'] });
    }
  });

  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <style>
        {`
          @keyframes fadeIn {
            from { opacity: 0; transform: translateY(10px); }
            to { opacity: 1; transform: translateY(0); }
          }
        `}
      </style>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Stack gap="md">
            <Group gap="sm">
              <IconDatabaseImport size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>Sources</Title>
                <Text size="sm" c="dimmed">
                  Sources are the origin of your data. Configure database connections like PostgreSQL, MySQL, or MongoDB 
                  to capture changes (CDC) and stream them through Hermod.
                </Text>
              </Box>
              {!isViewer && (
                <Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/sources/new' })} radius="md">
                  Add Source
                </Button>
              )}
            </Group>
            <TextInput
              placeholder="Search sources by name or type..."
              leftSection={<IconSearch size="1rem" stroke={1.5} />}
              value={search}
              onChange={(event) => {
                setSearch(event.currentTarget.value);
                setPage(1);
              }}
              radius="md"
            />
          </Stack>
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
          <Table.Thead bg="gray.0">
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Type</Table.Th>
              <Table.Th>VHost</Table.Th>
              <Table.Th>Status</Table.Th>
              <Table.Th>Worker</Table.Th>
              <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {sources?.map((src: any) => (
              <Table.Tr key={src.id}>
                <Table.Td fw={500}>{src.name}</Table.Td>
                <Table.Td>
                  <Text size="sm" component="span" px={8} py={2} bg="blue.0" c="blue.9" fw={600} style={{ borderRadius: '4px', textTransform: 'uppercase', fontSize: '10px' }}>
                    {src.type}
                  </Text>
                </Table.Td>
                <Table.Td>{src.vhost || '-'}</Table.Td>
                <Table.Td>
                  {(() => {
                    const liveStatus = getSourceLiveStatus(src.id);
                    const status = liveStatus || src.status;
                    
                    if (!src.active && !liveStatus && !src.status) {
                      return (
                        <Badge variant="filled" color="red" size="sm" radius="sm">
                          Shutdown
                        </Badge>
                      );
                    }
                    
                    if (status === 'running') {
                      return (
                        <Badge variant="filled" color="green" size="sm" radius="sm" leftSection={<IconActivity size="0.7rem" />}>
                          Online
                        </Badge>
                      );
                    }
                    if (status === 'error' || (status && status.startsWith('error'))) {
                      return (
                        <Badge variant="filled" color="red" size="sm" radius="sm" leftSection={<IconActivity size="0.7rem" />}>
                          Offline
                        </Badge>
                      );
                    }
                    if (status === 'reconnecting') {
                      return (
                        <Badge variant="filled" color="orange" size="sm" radius="sm" leftSection={<IconActivity size="0.7rem" />}>
                          Reconnecting
                        </Badge>
                      );
                    }
                    if (status === 'connecting') {
                      return (
                        <Badge variant="filled" color="orange" size="sm" radius="sm" leftSection={<IconActivity size="0.7rem" />}>
                          Connecting
                        </Badge>
                      );
                    }
                    
                    return (
                      <Badge variant="light" color="blue" size="sm" radius="sm">
                        {status || 'Active'}
                      </Badge>
                    );
                  })()}
                </Table.Td>
                <Table.Td>
                  {src.worker_id ? (
                    <Text size="sm" component="span" px={8} py={2} bg="gray.1" c="gray.7" fw={600} style={{ borderRadius: '4px', fontSize: '10px' }}>
                      {getWorkerName(src.worker_id)}
                    </Text>
                  ) : '-'}
                </Table.Td>
                <Table.Td>
                  {!isViewer && (
                    <Group justify="flex-end">
                      <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/sources/${src.id}/edit` })} radius="md">
                        <IconEdit size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                      <ActionIcon variant="light" color="red" onClick={() => {
                        setSourceToDelete(src);
                        open();
                      }} radius="md">
                        <IconTrash size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                    </Group>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
            {sources?.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={6} py="xl">
                  <Text c="dimmed" ta="center">{search ? 'No sources match your search' : 'No sources configured yet'}</Text>
                </Table.Td>
              </Table.Tr>
            )}
          </Table.Tbody>
        </Table>
        {totalPages > 1 && (
          <Group justify="center" p="md" bg="gray.0" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
            <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
          </Group>
        )}
      </Paper>
    </Stack>

    <Modal 
      opened={opened} 
      onClose={close} 
      title={<Text fw={700} size="lg">Delete Source</Text>}
      centered
      radius="md"
    >
      <Stack gap="md">
        <Box>
          <Text size="sm" mb="xs">
            Are you sure you want to delete source <b>{sourceToDelete?.name}</b>? This action cannot be undone.
          </Text>
          
          {activeConnectionsUsingSource.length > 0 && (
            <Paper withBorder p="sm" bg="red.0" style={{ borderColor: 'var(--mantine-color-red-2)' }}>
              <Group gap="xs" mb="xs">
                <IconAlertCircle size="1.2rem" color="var(--mantine-color-red-6)" />
                <Text size="sm" fw={600} c="red.9">Warning: Related active connections</Text>
              </Group>
              <Text size="xs" c="red.8" mb="sm">
                The following active connections use this source and will be <b>deactivated</b>:
              </Text>
              <List
                size="xs"
                spacing="xs"
                icon={
                  <ThemeIcon color="red" size={16} radius="xl">
                    <IconAlertCircle size="0.8rem" />
                  </ThemeIcon>
                }
              >
                {activeConnectionsUsingSource.map((c: any) => (
                  <List.Item key={c.id}>
                    <Text size="xs" component="span" fw={500}>{c.name}</Text>
                  </List.Item>
                ))}
              </List>
            </Paper>
          )}
        </Box>

        <Group justify="flex-end" mt="md">
          <Button variant="light" onClick={close} radius="md">Cancel</Button>
          <Button color="red" onClick={() => {
            if (sourceToDelete) {
              deleteMutation.mutate(sourceToDelete.id);
              close();
            }
          }} radius="md">
            Delete Source
          </Button>
        </Group>
      </Stack>
    </Modal>
    </Box>
  );
}
