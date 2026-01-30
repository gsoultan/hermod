import { useEffect, useState } from 'react';
import { Title, Table, Button, Group, ActionIcon, Paper, Text, Box, Stack, Badge, Modal, List, ThemeIcon, TextInput, Pagination } from '@mantine/core';
import { IconTrash, IconPlus, IconExternalLink, IconEdit, IconAlertCircle, IconSearch, IconActivity } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { useDisclosure } from '@mantine/hooks';

const API_BASE = '/api';

export function SinksPage() {
  const queryClient = useQueryClient();
  const { selectedVHost } = useVHost();
  const role = getRoleFromToken();
  const isViewer = role === 'Viewer';
  const navigate = useNavigate();
  const [opened, { open, close }] = useDisclosure(false);
  const [sinkToDelete, setSinkToDelete] = useState<any>(null);
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
          [update.workflow_id]: update
        }));
      } catch (err) {
        console.error('Failed to parse status update', err);
      }
    };

    return () => ws.close();
  }, []);

  const { data: sinksResponse } = useQuery({
    queryKey: ['sinks', activePage, search, selectedVHost],
    queryFn: async () => {
      const vhostParam = selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const res = await apiFetch(`${API_BASE}/sinks?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(search)}${vhostParam}`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    },
    staleTime: 30_000,
    refetchInterval: false,
  });

  const sinks = (sinksResponse as any)?.data || [];
  const totalItems = (sinksResponse as any)?.total || 0;

  const { data: workflowsResponse } = useQuery({
    queryKey: ['workflows-for-delete', selectedVHost],
    enabled: !!sinkToDelete,
    queryFn: async () => {
      const vhostParam = selectedVHost && selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const res = await apiFetch(`${API_BASE}/workflows?limit=200${vhostParam}`);
      if (res.ok) return res.json();
      return { data: [], total: 0 } as any;
    },
    staleTime: 60_000,
    refetchInterval: false,
  });
  const workflows = (workflowsResponse as any)?.data || [];

  const activeWorkflowsUsingSink = sinkToDelete 
    ? (workflows as any[])?.filter(wf => wf.nodes?.some((n: any) => n.type === 'sink' && n.ref_id === sinkToDelete.id) && wf.active)
    : [];

  const { data: workersResponse } = useQuery({
    queryKey: ['workers-lite'],
    enabled: true,
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers?limit=200`);
      if (res.ok) return res.json();
      return { data: [], total: 0 } as any;
    },
    staleTime: 60_000,
    refetchInterval: false,
  });
  const workers = (workersResponse as any)?.data || [];

  const getWorkerName = (id: string) => {
    const worker = (workers as any[])?.find(w => w.id === id);
    return worker ? worker.name : id;
  };

  const getSinkLiveStatus = (sinkId: string) => {
    const relevantStatuses: string[] = [];
    Object.values(liveStatuses).forEach((s: any) => {
      if (s.sink_id === sinkId && s.sink_status) {
        relevantStatuses.push(s.sink_status);
      }
      if (s.sink_statuses && s.sink_statuses[sinkId]) {
        relevantStatuses.push(s.sink_statuses[sinkId]);
      }
    });

    if (relevantStatuses.length === 0) return null;
    
    if (relevantStatuses.some(status => status === 'error')) return 'error';
    if (relevantStatuses.some(status => status === 'reconnecting')) return 'reconnecting';
    if (relevantStatuses.some(status => status === 'connecting')) return 'connecting';
    if (relevantStatuses.some(status => status === 'running')) return 'running';
    return relevantStatuses[0];
  };

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/sinks/${id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to delete sink');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sinks'] });
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
              <IconExternalLink size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>Sinks</Title>
                <Text size="sm" c="dimmed">
                  Sinks are the destinations for your data. Configure output targets like NATS, RabbitMQ, Redis, 
                  or Kafka to receive the data streams processed by Hermod.
                </Text>
              </Box>
              {!isViewer && (
                <Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/sinks/new' })} radius="md">
                  Add Sink
                </Button>
              )}
            </Group>
            <TextInput
              placeholder="Search sinks by name or type..."
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
            {(Array.isArray(sinks) ? sinks : []).map((snk: any) => (
              <Table.Tr key={snk.id}>
                <Table.Td fw={500}>{snk.name}</Table.Td>
                <Table.Td>
                  <Text size="sm" component="span" px={8} py={2} bg="teal.0" c="teal.9" fw={600} style={{ borderRadius: '4px', textTransform: 'uppercase', fontSize: '10px' }}>
                    {snk.type}
                  </Text>
                </Table.Td>
                <Table.Td>{snk.vhost || '-'}</Table.Td>
                <Table.Td>
                  {(() => {
                    const liveStatus = getSinkLiveStatus(snk.id);
                    const status = liveStatus || snk.status;

                    if (!snk.active && !liveStatus && !snk.status) {
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
                  {snk.worker_id ? (
                    <Text size="sm" component="span" px={8} py={2} bg="gray.1" c="gray.7" fw={600} style={{ borderRadius: '4px', fontSize: '10px' }}>
                      {getWorkerName(snk.worker_id)}
                    </Text>
                  ) : '-'}
                </Table.Td>
                <Table.Td>
                  {!isViewer && (
                    <Group justify="flex-end">
                      <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/sinks/${snk.id}/edit` })} radius="md">
                        <IconEdit size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                      <ActionIcon variant="light" color="red" onClick={() => {
                        setSinkToDelete(snk);
                        open();
                      }} radius="md">
                        <IconTrash size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                    </Group>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
            {sinks?.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={6} py="xl">
                  <Text c="dimmed" ta="center">{search ? 'No sinks match your search' : 'No sinks found'}</Text>
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
      title={<Text fw={700} size="lg">Delete Sink</Text>}
      centered
      radius="md"
    >
      <Stack gap="md">
        <Box>
          <Text size="sm" mb="xs">
            Are you sure you want to delete sink <b>{sinkToDelete?.name}</b>? This action cannot be undone.
          </Text>
          
          {activeWorkflowsUsingSink.length > 0 && (
            <Paper withBorder p="sm" bg="red.0" style={{ borderColor: 'var(--mantine-color-red-2)' }}>
              <Group gap="xs" mb="xs">
                <IconAlertCircle size="1.2rem" color="var(--mantine-color-red-6)" />
                <Text size="sm" fw={600} c="red.9">Warning: Related active workflows</Text>
              </Group>
              <Text size="xs" c="red.8" mb="sm">
                The following active workflows use this sink and will be <b>deactivated</b>:
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
                {activeWorkflowsUsingSink.map((wf: any) => (
                  <List.Item key={wf.id}>
                    <Text size="xs" component="span" fw={500}>{wf.name}</Text>
                  </List.Item>
                ))}
              </List>
            </Paper>
          )}
        </Box>

        <Group justify="flex-end" mt="md">
          <Button variant="light" onClick={close} radius="md">Cancel</Button>
          <Button color="red" onClick={() => {
            if (sinkToDelete) {
              deleteMutation.mutate(sinkToDelete.id);
              close();
            }
          }} radius="md">
            Delete Sink
          </Button>
        </Group>
      </Stack>
    </Modal>
    </Box>
  );
}
