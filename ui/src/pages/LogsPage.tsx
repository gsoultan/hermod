import { Title, Table, Group, Stack, Badge, Paper, Text, Box, ActionIcon, Tooltip, Select, TextInput, Pagination, Modal, ScrollArea, Code, Divider, Button } from '@mantine/core';
import { IconActivity, IconRefresh, IconSearch, IconTrash, IconEye } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useState, useEffect } from 'react';
import { useDisclosure } from '@mantine/hooks';
import { useSearch } from '@tanstack/react-router';

const API_BASE = '/api';

export function LogsPage() {
  const searchParams = useSearch({ from: '/logs' }) as any;
  const queryClient = useQueryClient();
  const [workflowId, setWorkflowId] = useState<string>(searchParams.workflow_id || '');
  const [level, setLevel] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [selectedLog, setSelectedLog] = useState<any>(null);
  const [opened, { open, close }] = useDisclosure(false);

  useEffect(() => {
    setWorkflowId(searchParams.workflow_id || '');
  }, [searchParams.workflow_id]);

  const viewDetails = (log: any) => {
    setSelectedLog(log);
    open();
  };

  const { data: logsResponse, isFetching } = useQuery({
    queryKey: ['logs', workflowId, level, search, activePage],
    queryFn: async () => {
      let url = `${API_BASE}/logs?page=${activePage}&limit=${itemsPerPage}&search=${search}`;
      if (workflowId) url += `&workflow_id=${workflowId}`;
      if (level) url += `&level=${level}`;
      const res = await apiFetch(url);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json();
    },
    refetchInterval: 5000, // Refresh every 5 seconds
  });

  const logs = (logsResponse as any)?.data || [];
  const totalItems = (logsResponse as any)?.total || 0;

  const deleteMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/logs`, { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to clear logs');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['logs'] });
    }
  });

  const totalPages = Math.ceil(totalItems / itemsPerPage);

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
              label="Search Logs" 
              placeholder="Search in message, data, IDs..." 
              value={search} 
              onChange={(e) => {
                setSearch(e.currentTarget.value);
                setPage(1);
              }}
              leftSection={<IconSearch size="1rem" />}
              style={{ flex: 1 }}
            />
            <TextInput 
              label="Workflow ID" 
              placeholder="Filter by ID..." 
              value={workflowId} 
              onChange={(e) => {
                setWorkflowId(e.currentTarget.value);
                setPage(1);
              }}
              leftSection={<IconSearch size="1rem" />}
              style={{ width: 200 }}
            />
            <Select 
              label="Level" 
              placeholder="All levels"
              data={['INFO', 'WARN', 'ERROR', 'DEBUG']}
              value={level}
              onChange={(val) => {
                setLevel(val);
                setPage(1);
              }}
              clearable
              style={{ width: 120 }}
            />
          </Group>
        </Paper>

        <Paper withBorder radius="md" style={{ overflow: 'hidden' }}>
          <Table verticalSpacing="sm" highlightOnHover>
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                <Table.Th style={{ width: 100 }}>Level</Table.Th>
                <Table.Th style={{ width: 150 }}>Action</Table.Th>
                <Table.Th>Message</Table.Th>
                <Table.Th style={{ width: 220 }}>Workflow / Source / Sink</Table.Th>
                <Table.Th style={{ width: 80 }}>Details</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {logs?.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={6} style={{ textAlign: 'center', padding: '40px' }}>
                    <Text c="dimmed">{search || workflowId || level ? 'No logs found matching the criteria.' : 'No logs found.'}</Text>
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
                      {log.action ? (
                        <Badge variant="outline" color="blue" size="sm" radius="xs" style={{ textTransform: 'none' }}>
                          {log.action}
                        </Badge>
                      ) : (
                        <Text size="xs" c="dimmed">-</Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" fw={500}>{log.message}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        {log.workflow_id && (
                          <Group gap={4}>
                            <Text size="xs" c="dimmed" fw={700}>WF:</Text>
                            <Text size="xs" style={{ fontFamily: 'monospace' }}>{log.workflow_id.substring(0, 8)}...</Text>
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
                      <Tooltip label="View Full Details">
                        <ActionIcon variant="light" color="blue" onClick={() => viewDetails(log)}>
                          <IconEye size="1.1rem" />
                        </ActionIcon>
                      </Tooltip>
                    </Table.Td>
                  </Table.Tr>
                ))
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
        title={<Text fw={700}>Log Entry Details</Text>}
        size="lg"
        radius="md"
      >
        {selectedLog && (
          <Stack gap="md">
            <Group justify="space-between">
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Timestamp</Text>
                <Text size="sm">{new Date(selectedLog.timestamp).toLocaleString()}</Text>
              </Box>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }} ta="right">Level</Text>
                <Badge color={getLevelColor(selectedLog.level)} variant="light">
                  {selectedLog.level}
                </Badge>
              </Box>
            </Group>

            <Divider />

            <Box>
              <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }} mb={4}>Message</Text>
              <Paper withBorder p="xs" bg="gray.0">
                <Text size="sm" fw={500}>{selectedLog.message}</Text>
              </Paper>
            </Box>

            {selectedLog.action && (
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }} mb={4}>Action</Text>
                <Badge variant="outline" color="blue" radius="xs" style={{ textTransform: 'none' }}>
                  {selectedLog.action}
                </Badge>
              </Box>
            )}

            <Group grow>
              {selectedLog.workflow_id && (
                <Box>
                  <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Workflow ID</Text>
                  <Code block fz="xs">{selectedLog.workflow_id}</Code>
                </Box>
              )}
              {selectedLog.source_id && (
                <Box>
                  <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Source ID</Text>
                  <Code block fz="xs">{selectedLog.source_id}</Code>
                </Box>
              )}
              {selectedLog.sink_id && (
                <Box>
                  <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Sink ID</Text>
                  <Code block fz="xs">{selectedLog.sink_id}</Code>
                </Box>
              )}
            </Group>

            {selectedLog.data && (
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }} mb={4}>Action Data / Payload</Text>
                <ScrollArea.Autosize mah={400} type="always">
                  <Paper withBorder p="xs" bg="gray.0">
                    <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                      {(() => {
                        try {
                          // Try to format as JSON if it looks like JSON
                          if (selectedLog.data.trim().startsWith('{') || selectedLog.data.trim().startsWith('[')) {
                            return JSON.stringify(JSON.parse(selectedLog.data), null, 2);
                          }
                        } catch (e) {}
                        return selectedLog.data;
                      })()}
                    </Code>
                  </Paper>
                </ScrollArea.Autosize>
              </Box>
            )}

            <Group justify="flex-end" mt="md">
              <Button onClick={close} variant="light">Close</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Box>
  );
}
