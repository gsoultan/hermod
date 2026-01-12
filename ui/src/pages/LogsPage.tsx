import { Title, Table, Group, Stack, Badge, Paper, Text, Box, ActionIcon, Tooltip, Select, TextInput } from '@mantine/core';
import { IconActivity, IconRefresh, IconSearch, IconTrash } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useState } from 'react';

const API_BASE = '/api';

export function LogsPage() {
  const queryClient = useQueryClient();
  const [limit, setLimit] = useState<string>('100');
  const [connectionId, setConnectionId] = useState<string>('');
  const [level, setLevel] = useState<string | null>(null);

  const { data: logs, isFetching } = useQuery({
    queryKey: ['logs', connectionId, level, limit],
    queryFn: async () => {
      let url = `${API_BASE}/logs?limit=${limit}`;
      if (connectionId) url += `&connection_id=${connectionId}`;
      if (level) url += `&level=${level}`;
      const res = await apiFetch(url);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json();
    },
    refetchInterval: 5000, // Refresh every 5 seconds
  });

  const deleteMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/logs`, { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to clear logs');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['logs'] });
    }
  });

  const getLevelColor = (level: string) => {
    switch (level) {
      case 'ERROR': return 'red';
      case 'WARN': return 'yellow';
      case 'INFO': return 'blue';
      case 'DEBUG': return 'gray';
      default: return 'gray';
    };
  };

  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconActivity size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Activity Logs</Title>
              <Text size="sm" c="dimmed">
                Monitor data flow and system events in real-time. Logs show messages as they are received, transformed, and delivered.
              </Text>
            </Box>
            <Group>
              <Tooltip label="Refresh">
                <ActionIcon variant="light" onClick={() => queryClient.invalidateQueries({ queryKey: ['logs'] })} loading={isFetching}>
                  <IconRefresh size="1.2rem" />
                </ActionIcon>
              </Tooltip>
              <Tooltip label="Clear All Logs">
                <ActionIcon variant="light" color="red" onClick={() => { if(confirm('Clear all logs?')) deleteMutation.mutate(); }}>
                  <IconTrash size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            </Group>
          </Group>
        </Paper>

        <Paper p="md" withBorder radius="md">
          <Group align="flex-end" gap="md">
            <TextInput 
              label="Filter by Connection ID" 
              placeholder="Enter ID..." 
              value={connectionId} 
              onChange={(e) => setConnectionId(e.currentTarget.value)}
              leftSection={<IconSearch size="1rem" />}
              style={{ flex: 1 }}
            />
            <Select 
              label="Level" 
              placeholder="All levels"
              data={['INFO', 'WARN', 'ERROR', 'DEBUG']}
              value={level}
              onChange={setLevel}
              clearable
              style={{ width: 150 }}
            />
            <Select 
              label="Limit" 
              data={['50', '100', '200', '500']}
              value={limit}
              onChange={(val) => setLimit(val || '100')}
              style={{ width: 100 }}
            />
          </Group>
        </Paper>

        <Paper withBorder radius="md" style={{ overflow: 'hidden' }}>
          <Table verticalSpacing="sm" highlightOnHover>
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th style={{ width: 200 }}>Timestamp</Table.Th>
                <Table.Th style={{ width: 100 }}>Level</Table.Th>
                <Table.Th>Message</Table.Th>
                <Table.Th>Connection / Source / Sink</Table.Th>
                <Table.Th>Details</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {logs?.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={5} style={{ textAlign: 'center', padding: '40px' }}>
                    <Text c="dimmed">No logs found matching the criteria.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                logs?.map((log: any) => (
                  <Table.Tr key={log.id}>
                    <Table.Td>
                      <Text size="sm">{new Date(log.timestamp).toLocaleString()}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge color={getLevelColor(log.level)} variant="light" size="sm">
                        {log.level}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" fw={500}>{log.message}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        {log.connection_id && (
                          <Group gap={4}>
                            <Text size="xs" c="dimmed" fw={700}>Conn:</Text>
                            <Text size="xs" style={{ fontFamily: 'monospace' }}>{log.connection_id.substring(0, 8)}...</Text>
                          </Group>
                        )}
                        {log.source_id && (
                          <Group gap={4}>
                            <Text size="xs" c="dimmed" fw={700}>Src:</Text>
                            <Text size="xs" style={{ fontFamily: 'monospace' }}>{log.source_id.substring(0, 8)}...</Text>
                          </Group>
                        )}
                        {log.sink_id && (
                          <Group gap={4}>
                            <Text size="xs" c="dimmed" fw={700}>Snk:</Text>
                            <Text size="xs" style={{ fontFamily: 'monospace' }}>{log.sink_id.substring(0, 8)}...</Text>
                          </Group>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {log.data && (
                        <Tooltip label={log.data} multiline w={400} withArrow>
                          <Text size="xs" c="dimmed" style={{ 
                            maxWidth: 200, 
                            whiteSpace: 'nowrap', 
                            overflow: 'hidden', 
                            textOverflow: 'ellipsis',
                            cursor: 'help'
                          }}>
                            {log.data}
                          </Text>
                        </Tooltip>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Paper>
      </Stack>
    </Box>
  );
}
