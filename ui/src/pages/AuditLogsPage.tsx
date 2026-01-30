import { Title, Table, Group, Stack, Badge, Paper, Text, Box, ActionIcon, Tooltip, TextInput, Pagination, Modal, ScrollArea, Code, Divider, Button } from '@mantine/core';
import { IconHistory, IconRefresh, IconEye } from '@tabler/icons-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useState } from 'react';
import { useDisclosure } from '@mantine/hooks';

import { formatDateTime } from '../utils/dateUtils';

const API_BASE = '/api';

export function AuditLogsPage() {
  const queryClient = useQueryClient();
  const [activePage, setPage] = useState(1);
  const [action, setAction] = useState('');
  const [entityType, setEntityType] = useState('');
  const [selectedLog, setSelectedLog] = useState<any>(null);
  const [opened, { open, close }] = useDisclosure(false);
  const itemsPerPage = 50;

  const { data: auditResponse, isFetching } = useQuery({
    queryKey: ['audit-logs', activePage, action, entityType],
    queryFn: async () => {
      let url = `${API_BASE}/audit-logs?page=${activePage}&limit=${itemsPerPage}`;
      if (action) url += `&action=${action}`;
      if (entityType) url += `&entity_type=${entityType}`;
      // In a real scenario, search might be implemented on the backend for audit logs too
      const res = await apiFetch(url);
      if (!res.ok) throw new Error('Failed to fetch audit logs');
      return res.json();
    }
  });

  const logs = (auditResponse as any)?.items || [];
  const totalItems = (auditResponse as any)?.total || 0;
  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const viewDetails = (log: any) => {
    setSelectedLog(log);
    open();
  };

  const getActionColor = (action: string) => {
    switch (action?.toUpperCase()) {
      case 'CREATE': return 'green';
      case 'UPDATE': return 'blue';
      case 'DELETE': return 'red';
      case 'START': return 'teal';
      case 'STOP': return 'orange';
      default: return 'gray';
    }
  };

  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconHistory size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Audit Logs</Title>
              <Text size="sm" c="dimmed">
                Track administrative changes, workflow lifecycle events, and user actions for security and compliance.
              </Text>
            </Box>
            <Tooltip label="Refresh">
              <ActionIcon aria-label="Refresh logs" variant="light" onClick={() => queryClient.invalidateQueries({ queryKey: ['audit-logs'] })} loading={isFetching}>
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Paper>

        <Paper p="md" withBorder radius="md">
          <Group align="flex-end" gap="md">
            <TextInput 
              label="Entity Type" 
              placeholder="e.g. workflow, source" 
              value={entityType} 
              onChange={(e) => {
                setEntityType(e.currentTarget.value);
                setPage(1);
              }}
              style={{ width: 200 }}
            />
            <TextInput 
              label="Action" 
              placeholder="e.g. CREATE, UPDATE" 
              value={action} 
              onChange={(e) => {
                setAction(e.currentTarget.value);
                setPage(1);
              }}
              style={{ width: 150 }}
            />
          </Group>
        </Paper>

        <Paper withBorder radius="md" style={{ overflow: 'hidden' }}>
          <Table verticalSpacing="sm" highlightOnHover>
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                <Table.Th style={{ width: 120 }}>User</Table.Th>
                <Table.Th style={{ width: 100 }}>Action</Table.Th>
                <Table.Th style={{ width: 120 }}>Entity Type</Table.Th>
                <Table.Th>Entity ID</Table.Th>
                <Table.Th style={{ width: 120 }}>IP Address</Table.Th>
                <Table.Th style={{ width: 80 }}>Details</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {logs?.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={7} style={{ textAlign: 'center', padding: '40px' }}>
                    <Text c="dimmed">No audit logs found.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                logs.map((log: any) => (
                  <Table.Tr key={log.id}>
                    <Table.Td>
                      <Text size="sm">{formatDateTime(log.timestamp)}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        <Text size="sm" fw={500}>{log.username || 'System'}</Text>
                        <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>{log.user_id?.substring(0, 8)}</Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Badge color={getActionColor(log.action)} variant="light" size="sm">
                        {log.action}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" style={{ textTransform: 'capitalize' }}>{log.entity_type}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs" style={{ fontFamily: 'monospace' }}>{log.entity_id}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs">{log.ip || '-'}</Text>
                    </Table.Td>
                    <Table.Td>
                      <ActionIcon aria-label="View details" variant="light" color="blue" onClick={() => viewDetails(log)}>
                        <IconEye size="1.1rem" />
                      </ActionIcon>
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
        title={<Text fw={700}>Audit Log Details</Text>}
        size="lg"
      >
        {selectedLog && (
          <Stack gap="md">
            <Group grow>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Timestamp</Text>
                <Text size="sm">{formatDateTime(selectedLog.timestamp)}</Text>
              </Box>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Action</Text>
                <Badge color={getActionColor(selectedLog.action)} variant="light">
                  {selectedLog.action}
                </Badge>
              </Box>
            </Group>

            <Divider />

            <Group grow>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Entity Type</Text>
                <Text size="sm">{selectedLog.entity_type}</Text>
              </Box>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Entity ID</Text>
                <Text size="xs" style={{ fontFamily: 'monospace' }}>{selectedLog.entity_id}</Text>
              </Box>
            </Group>

            <Group grow>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>User</Text>
                <Text size="sm">{selectedLog.username || 'System'}</Text>
                <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>{selectedLog.user_id}</Text>
              </Box>
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>IP Address</Text>
                <Text size="sm">{selectedLog.ip || '-'}</Text>
              </Box>
            </Group>

            {selectedLog.payload && (
              <Box>
                <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }} mb={4}>Payload</Text>
                <ScrollArea h={300} type="always">
                  <Paper withBorder p="xs" bg="gray.0">
                    <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                      {(() => {
                        try {
                          return JSON.stringify(JSON.parse(selectedLog.payload), null, 2);
                        } catch (e) {
                          return selectedLog.payload;
                        }
                      })()}
                    </Code>
                  </Paper>
                </ScrollArea>
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
