import { Title, Table, Button, Group, Stack, ActionIcon, Switch, Badge, Paper, Text, Box, Anchor } from '@mantine/core';
import { IconTrash, IconPlus, IconRoute, IconEdit, IconEye } from '@tabler/icons-react';
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';

const API_BASE = '/api';

export function ConnectionsPage() {
  const queryClient = useQueryClient();
  const { selectedVHost } = useVHost();
  const role = getRoleFromToken();
  const isViewer = role === 'Viewer';
  const navigate = useNavigate();

  const { data: connections } = useSuspenseQuery({
    queryKey: ['connections'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/connections`);
      if (!res.ok) throw new Error('Failed to fetch connections');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const { data: sources } = useSuspenseQuery({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const { data: sinks } = useSuspenseQuery({
    queryKey: ['sinks'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const { data: workers } = useSuspenseQuery({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const getWorkerName = (id: string) => {
    const worker = (workers as any[])?.find(w => w.id === id);
    return worker ? worker.name : id;
  };

  const filteredConnections = (connections || []).filter((c: any) => 
    selectedVHost === 'all' || c.vhost === selectedVHost
  );

  const toggleMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/connections/${id}/toggle`, { method: 'POST' });
      if (!res.ok) throw new Error('Failed to toggle connection');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connections'] });
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

  const getSourceName = (id: string) => sources?.find((s: any) => s.id === id)?.name || id;
  const getSinkName = (id: string) => sinks?.find((s: any) => s.id === id)?.name || id;

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
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
          <Table.Thead bg="gray.0">
            <Table.Tr>
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
            {filteredConnections?.map((conn: any) => (
              <Table.Tr key={conn.id}>
                <Table.Td fw={500}>
                  <Anchor onClick={() => navigate({ to: `/connections/${conn.id}` })} style={{ cursor: 'pointer' }}>
                    {conn.name}
                  </Anchor>
                </Table.Td>
                <Table.Td>
                  <Badge variant="dot" color="blue" radius="sm">{getSourceName(conn.source_id)}</Badge>
                </Table.Td>
                <Table.Td>
                  <Group gap={4}>
                    {(conn.sink_ids || []).map((sinkId: string) => (
                      <Badge key={sinkId} variant="dot" color="teal" radius="sm">{getSinkName(sinkId)}</Badge>
                    ))}
                  </Group>
                </Table.Td>
                <Table.Td>{conn.vhost || '-'}</Table.Td>
                <Table.Td>
                  {conn.worker_id ? (
                    <Text size="sm" component="span" px={8} py={2} bg="gray.1" c="gray.7" fw={600} style={{ borderRadius: '4px', fontSize: '10px' }}>
                      {getWorkerName(conn.worker_id)}
                    </Text>
                  ) : '-'}
                </Table.Td>
                <Table.Td>
                  <Badge 
                    variant="light" 
                    color={conn.active ? 'green' : 'gray'}
                    size="sm"
                    radius="sm"
                  >
                    {conn.active ? 'Running' : 'Stopped'}
                  </Badge>
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
            {filteredConnections?.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={7} py="xl">
                  <Text c="dimmed" ta="center">No connections found</Text>
                </Table.Td>
              </Table.Tr>
            )}
          </Table.Tbody>
        </Table>
      </Paper>
    </Stack>
    </Box>
  );
}
