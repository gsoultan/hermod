import { useEffect, useState } from 'react';
import { Title, Table, Button, Group, Stack, ActionIcon, Switch, Badge, Paper, Text, Box, Anchor, TextInput, Pagination, Checkbox } from '@mantine/core';
import { IconTrash, IconPlus, IconRoute, IconEdit, IconEye, IconSearch, IconDatabase, IconSend, IconActivity, IconPlayerPlay, IconPlayerStop, IconCopy } from '@tabler/icons-react';
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { notifications } from '@mantine/notifications';

const API_BASE = '/api';

export function ConnectionsPage() {
  const queryClient = useQueryClient();
  const { selectedVHost } = useVHost();
  const role = getRoleFromToken();
  const isViewer = role === 'Viewer';
  const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [selectedIds, setSelectedIds] = useState<string[]>([]);

  const [liveStatuses, setLiveStatuses] = useState<Record<string, any>>({});

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status`;
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const update = JSON.parse(event.data);
        setLiveStatuses(prev => {
          const prevStatus = prev[update.connection_id];
          
          // Show alert if status changed to reconnecting
          if (update.engine_status.startsWith('reconnecting') && (!prevStatus || !prevStatus.engine_status.startsWith('reconnecting'))) {
            notifications.show({
              title: 'Connection Alert',
              message: `Connection ${update.connection_id} entered reconnecting state`,
              color: 'orange'
            });
          }
          
          return {
            ...prev,
            [update.connection_id]: update
          };
        });
      } catch (err) {
        console.error('Failed to parse status update', err);
      }
    };

    return () => ws.close();
  }, []);

  const { data: connectionsResponse } = useSuspenseQuery({
    queryKey: ['connections', activePage, search, selectedVHost],
    queryFn: async () => {
      const vhostParam = selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const res = await apiFetch(`${API_BASE}/connections?page=${activePage}&limit=${itemsPerPage}&search=${search}${vhostParam}`);
      if (!res.ok) throw new Error('Failed to fetch connections');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const connections = (connectionsResponse as any)?.data || [];
  const totalItems = (connectionsResponse as any)?.total || 0;

  const { data: sourcesResponse } = useSuspenseQuery({
    queryKey: ['sources-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources?limit=1000`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    },
    refetchInterval: 5000,
  });
  const sources = (sourcesResponse as any)?.data || [];

  const { data: sinksResponse } = useSuspenseQuery({
    queryKey: ['sinks-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks?limit=1000`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    },
    refetchInterval: 5000,
  });
  const sinks = (sinksResponse as any)?.data || [];

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
    if (!id) return 'Shared (Auto)';
    const worker = (workers as any[])?.find(w => w.id === id);
    return worker ? worker.name : id;
  };

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const toggleMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/connections/${id}/toggle`, { method: 'POST' });
      if (!res.ok) throw new Error('Failed to toggle connection');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
    }
  });

  const bulkToggleMutation = useMutation({
    mutationFn: async ({ ids, active }: { ids: string[]; active: boolean }) => {
      const promises = ids.map(id => apiFetch(`${API_BASE}/connections/${id}/toggle`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ active })
      }));
      return Promise.all(promises);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
      notifications.show({ title: 'Success', message: 'Bulk action completed', color: 'green' });
      setSelectedIds([]);
    }
  });

  const bulkDeleteMutation = useMutation({
    mutationFn: async (ids: string[]) => {
      if (!window.confirm(`Are you sure you want to delete ${ids.length} connections?`)) return;
      const promises = ids.map(id => apiFetch(`${API_BASE}/connections/${id}`, { method: 'DELETE' }));
      return Promise.all(promises);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
      notifications.show({ title: 'Success', message: 'Selected connections deleted', color: 'green' });
      setSelectedIds([]);
    }
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/connections/${id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to delete connection');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
    }
  });

  const duplicateMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/connections/${id}`);
      if (!res.ok) throw new Error('Failed to fetch connection for duplication');
      const conn = await res.json();
      
      const payload = {
        ...conn,
        id: undefined,
        name: `${conn.name} (Copy)`,
        active: false,
        status: undefined,
      };
      
      const createRes = await apiFetch(`${API_BASE}/connections`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      
      if (!createRes.ok) {
        const err = await createRes.json();
        throw new Error(`Failed to duplicate connection: ${err.error}`);
      }
      return createRes.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
      notifications.show({ title: 'Success', message: 'Connection duplicated', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message, color: 'red' });
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
              <IconRoute size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>Connections</Title>
                <Text size="sm" c="dimmed">
                  Connections link Sources to Sinks. They define the flow of data from your databases to your messaging systems, 
                  applying any configured transformations along the way.
                </Text>
              </Box>
              {!isViewer && (
                <Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/connections/new' })} radius="md">
                  Add Connection
                </Button>
              )}
            </Group>
            <TextInput
              placeholder="Search connections by name, source, or sink..."
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

        {selectedIds.length > 0 && (
          <Paper p="sm" withBorder radius="md" bg="blue.0">
            <Group justify="space-between">
              <Text size="sm" fw={600} c="blue.9">{selectedIds.length} connections selected</Text>
              <Group gap="xs">
                {!isViewer && (
                  <>
                    <Button size="xs" color="green" variant="filled" leftSection={<IconPlayerPlay size="0.9rem" />} onClick={() => bulkToggleMutation.mutate({ ids: selectedIds, active: true })}>Start Selected</Button>
                    <Button size="xs" color="orange" variant="filled" leftSection={<IconPlayerStop size="0.9rem" />} onClick={() => bulkToggleMutation.mutate({ ids: selectedIds, active: false })}>Stop Selected</Button>
                    <Button size="xs" color="red" variant="filled" leftSection={<IconTrash size="0.9rem" />} onClick={() => bulkDeleteMutation.mutate(selectedIds)}>Delete Selected</Button>
                  </>
                )}
                <Button size="xs" variant="subtle" color="gray" onClick={() => setSelectedIds([])}>Cancel</Button>
              </Group>
            </Group>
          </Paper>
        )}

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
          <Table.Thead bg="gray.0">
            <Table.Tr>
              <Table.Th style={{ width: 40 }}>
                <Checkbox 
                  checked={selectedIds.length === connections.length && connections.length > 0}
                  indeterminate={selectedIds.length > 0 && selectedIds.length < connections.length}
                  onChange={(e) => {
                    if (e.currentTarget.checked) {
                      setSelectedIds(connections.map((c: any) => c.id));
                    } else {
                      setSelectedIds([]);
                    }
                  }}
                />
              </Table.Th>
              <Table.Th>Name</Table.Th>
              <Table.Th>Source</Table.Th>
              <Table.Th>Sinks</Table.Th>
              <Table.Th>VHost</Table.Th>
              <Table.Th>Worker</Table.Th>
              <Table.Th>Status</Table.Th>
              <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {connections?.map((conn: any) => (
              <Table.Tr key={conn.id} bg={selectedIds.includes(conn.id) ? 'blue.0' : undefined}>
                <Table.Td>
                  <Checkbox 
                    checked={selectedIds.includes(conn.id)}
                    onChange={(e) => {
                      if (e.currentTarget.checked) {
                        setSelectedIds([...selectedIds, conn.id]);
                      } else {
                        setSelectedIds(selectedIds.filter(id => id !== conn.id));
                      }
                    }}
                  />
                </Table.Td>
                <Table.Td fw={500}>
                  <Anchor onClick={() => navigate({ to: `/connections/${conn.id}` })} style={{ cursor: 'pointer' }}>
                    {conn.name}
                  </Anchor>
                </Table.Td>
                <Table.Td>
                  {(() => {
                    const source = sources.find((s: any) => s.id === conn.source_id);
                    const liveUpdate = liveStatuses[conn.id];
                    const sourceStatus = liveUpdate?.source_status || source?.status;
                    const engineStatus = liveUpdate?.engine_status || conn.status;
                    
                    let color = 'gray';
                    if (conn.active) {
                      if (sourceStatus === 'running') {
                        color = 'green';
                      } else if (!source?.active || sourceStatus === 'error' || (sourceStatus && sourceStatus.startsWith('error')) || engineStatus === 'reconnecting:source') {
                        color = 'red';
                      } else if (sourceStatus === 'reconnecting' || sourceStatus === 'connecting' || engineStatus === 'reconnecting:source' || engineStatus === 'connecting') {
                        color = 'orange';
                      } else {
                        color = 'blue';
                      }
                    } else {
                      // Even if connection is inactive, show if source is offline
                      if (sourceStatus === 'error' || (sourceStatus && sourceStatus.startsWith('error'))) {
                        color = 'red';
                      }
                    }

                    return (
                      <Badge 
                        variant="filled" 
                        color={color} 
                        radius="sm"
                        leftSection={<IconDatabase size="0.7rem" />}
                        title={sourceStatus}
                      >
                        {source?.name || conn.source_id}
                      </Badge>
                    );
                  })()}
                </Table.Td>
                <Table.Td>
                  <Group gap={4}>
                    {(conn.sink_ids || []).map((sinkId: string) => {
                      const sink = sinks.find((s: any) => s.id === sinkId);
                      const liveUpdate = liveStatuses[conn.id];
                      const sinkStatus = liveUpdate?.sink_statuses?.[sinkId] || sink?.status;
                      const engineStatus = liveUpdate?.engine_status || conn.status;
                      
                      let color = 'gray';
                      if (conn.active) {
                        if (sinkStatus === 'running') {
                          color = 'green';
                        } else if (!sink?.active || sinkStatus === 'error' || (sinkStatus && sinkStatus.startsWith('error')) || engineStatus === `reconnecting:sink:${sinkId}`) {
                          color = 'red';
                        } else if (sinkStatus === 'reconnecting' || sinkStatus === 'connecting' || engineStatus === `reconnecting:sink:${sinkId}` || engineStatus === 'connecting') {
                          color = 'orange';
                        } else {
                          color = 'blue';
                        }
                      } else {
                        // Even if connection is inactive, show if sink is offline
                        if (sinkStatus === 'error' || (sinkStatus && sinkStatus.startsWith('error'))) {
                          color = 'red';
                        }
                      }

                      return (
                        <Badge 
                          key={sinkId} 
                          variant="filled" 
                          color={color} 
                          radius="sm"
                          leftSection={<IconSend size="0.7rem" />}
                          title={sinkStatus}
                        >
                          {sink?.name || sinkId}
                        </Badge>
                      );
                    })}
                  </Group>
                </Table.Td>
                <Table.Td>{conn.vhost || '-'}</Table.Td>
                <Table.Td>
                  <Text 
                    size="sm" 
                    component="span" 
                    px={8} py={2} 
                    bg={conn.worker_id ? "indigo.0" : "gray.1"} 
                    c={conn.worker_id ? "indigo.7" : "gray.7"} 
                    fw={600} 
                    style={{ borderRadius: '4px', fontSize: '10px', textTransform: 'uppercase' }}
                  >
                    {getWorkerName(conn.worker_id)}
                  </Text>
                </Table.Td>
                <Table.Td>
                  {(() => {
                    const liveUpdate = liveStatuses[conn.id];
                    const status = liveUpdate?.engine_status || conn.status;
                    const isActive = conn.active;
                    const hasStatus = !!liveUpdate;
                    
                    if (!isActive) {
                      return (
                        <Badge variant="light" color="gray" size="sm" radius="sm">
                          Stopped
                        </Badge>
                      );
                    }

                    if (!hasStatus && !status) {
                      return (
                        <Badge 
                          variant="filled" 
                          color="red" 
                          size="sm" 
                          radius="sm"
                          leftSection={<IconActivity size="0.7rem" />}
                        >
                          Shutdown
                        </Badge>
                      );
                    }
                    
                    if (status?.startsWith('reconnecting')) {
                      return (
                        <Badge 
                          variant="filled" 
                          color="orange" 
                          size="sm" 
                          radius="sm"
                          leftSection={<IconActivity size="0.7rem" />}
                        >
                          Reconnecting
                        </Badge>
                      );
                    }

                    if (status === 'connecting') {
                      return (
                        <Badge 
                          variant="filled" 
                          color="orange" 
                          size="sm" 
                          radius="sm"
                          leftSection={<IconActivity size="0.7rem" />}
                        >
                          Connecting
                        </Badge>
                      );
                    }

                    if (status === 'error' || (isActive && !hasStatus && status === 'shutdown')) {
                      return (
                        <Badge 
                          variant="filled" 
                          color="red" 
                          size="sm" 
                          radius="sm"
                          leftSection={<IconActivity size="0.7rem" />}
                        >
                          {status === 'error' ? 'Error' : 'Shutdown'}
                        </Badge>
                      );
                    }
                    
                    return (
                      <Badge 
                        variant="filled" 
                        color={status === 'running' ? 'green' : 'blue'} 
                        size="sm" 
                        radius="sm"
                        leftSection={<IconActivity size="0.7rem" />}
                      >
                        {status ? (status.charAt(0).toUpperCase() + status.slice(1)) : 'Active'}
                      </Badge>
                    );
                  })()}
                </Table.Td>
                <Table.Td>
                  {!isViewer && (
                    <Group gap="xs" justify="flex-end">
                      <Switch 
                        checked={conn.active} 
                        onChange={() => toggleMutation.mutate(conn.id)}
                        size="sm"
                      />
                      <ActionIcon 
                        variant="light" 
                        color="green" 
                        onClick={() => navigate({ to: `/connections/${conn.id}` })} 
                        radius="md"
                        title="View connection details"
                      >
                        <IconEye size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                      <ActionIcon 
                        variant="light" 
                        color="blue" 
                        onClick={() => navigate({ to: `/connections/${conn.id}/edit` })} 
                        radius="md"
                        disabled={conn.active}
                        title={conn.active ? "Connection must be inactive to edit" : "Edit connection"}
                      >
                        <IconEdit size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                      <ActionIcon 
                        variant="light" 
                        color="grape" 
                        onClick={() => duplicateMutation.mutate(conn.id)} 
                        radius="md"
                        title="Duplicate connection"
                        loading={duplicateMutation.isPending && duplicateMutation.variables === conn.id}
                      >
                        <IconCopy size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                      <ActionIcon variant="light" color="red" onClick={() => {
                        if (confirm('Are you sure you want to delete this connection?')) {
                          deleteMutation.mutate(conn.id);
                        }
                      }} radius="md">
                        <IconTrash size="1.2rem" stroke={1.5} />
                      </ActionIcon>
                    </Group>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
            {connections?.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={7} py="xl">
                  <Text c="dimmed" ta="center">{search ? 'No connections match your search' : 'No connections found'}</Text>
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
    </Box>
  );
}
