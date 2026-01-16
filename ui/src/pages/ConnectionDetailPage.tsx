import { useParams } from '@tanstack/react-router';
import { useSuspenseQuery, useQuery, useMutation } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { Title, Table, Group, Stack, Badge, Paper, Text, Box, ActionIcon, Tooltip, Breadcrumbs, Anchor, Grid, Divider, Code, ThemeIcon, TextInput, Pagination, Modal, ScrollArea, Button, SimpleGrid } from '@mantine/core';
import { IconActivity, IconDatabase, IconSend, IconSettings, IconRefresh, IconTableAlias, IconFilter, IconArrowsDiff, IconWand, IconWorld, IconRoute, IconSearch, IconEye, IconTrash } from '@tabler/icons-react';
import { useNavigate } from '@tanstack/react-router';
import { useState, useEffect, useMemo, useRef } from 'react';
import { notifications } from '@mantine/notifications';
import { useDisclosure } from '@mantine/hooks';

const API_BASE = '/api';

function Sparkline({ data, height = 30, color = 'blue' }: { data: number[], height?: number, color?: string }) {
  if (!data || data.length === 0) return null;
  const max = Math.max(...data, 1);
  const width = data.length * 10;
  const points = data.map((v, i) => `${i === 0 ? '' : 'L'} ${i * 10} ${height - (v / (max || 1)) * height}`).join(' ');

  return (
    <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ overflow: 'visible' }}>
      <path
        d={`M ${points}`}
        fill="none"
        stroke={`var(--mantine-color-${color}-filled)`}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function ConnectionDetailPage() {
  const { connectionId } = useParams({ from: '/connections/$connectionId' });
  const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [selectedLog, setSelectedLog] = useState<any>(null);
  const [opened, { open, close }] = useDisclosure(false);

  const viewDetails = (log: any) => {
    setSelectedLog(log);
    open();
  };

  const [liveStatus, setLiveStatus] = useState<any>(null);
  const [mps, setMps] = useState(0);
  const [mpsHistory, setMpsHistory] = useState<number[]>(new Array(30).fill(0));
  const lastStatsRef = useRef({ time: Date.now(), count: 0 });

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status`;
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const update = JSON.parse(event.data);
        if (update.connection_id === connectionId) {
          setLiveStatus((prev: any) => {
            if (update.engine_status.startsWith('reconnecting') && (!prev || !prev.engine_status.startsWith('reconnecting'))) {
               notifications.show({
                  title: 'Connection Alert',
                  message: `Connection entered reconnecting state`,
                  color: 'orange'
                });
            }
            return update;
          });

          // Calculate MPS
          const now = Date.now();
          const timeDiff = (now - lastStatsRef.current.time) / 1000;
          const countDiff = update.processed_count - lastStatsRef.current.count;
          
          if (timeDiff >= 1) {
              const currentMps = Math.max(0, Math.round(countDiff / timeDiff));
              setMps(currentMps);
              setMpsHistory(prev => [...prev.slice(1), currentMps]);
              lastStatsRef.current = { time: now, count: update.processed_count };
          }
        }
      } catch (err) {
        console.error('Failed to parse status update', err);
      }
    };

    return () => ws.close();
  }, [connectionId]);

  const { data: connection } = useSuspenseQuery({
    queryKey: ['connection', connectionId],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/connections/${connectionId}`);
      if (!res.ok) throw new Error('Failed to fetch connection');
      return res.json();
    },
    refetchInterval: 5000,
  });

  const { data: source } = useQuery({
    queryKey: ['source', connection?.source_id],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources/${connection.source_id}`);
      if (!res.ok) throw new Error('Failed to fetch source');
      return res.json();
    },
    enabled: !!connection?.source_id,
  });

  const { data: sinksResponse } = useQuery({
    queryKey: ['sinks-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks?limit=1000`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    },
  });

  const { data: workersResponse } = useQuery({
    queryKey: ['workers-all'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers?limit=1000`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const allSinks = sinksResponse?.data || [];
  const workers = workersResponse?.data || [];

  const getWorkerName = (id: string) => {
    if (!id) return 'Shared (Auto)';
    const worker = (workers as any[])?.find(w => w.id === id);
    return worker ? worker.name : id;
  };

  const { data: logsResponse, refetch: refetchLogs, isFetching: isFetchingLogs } = useQuery({
    queryKey: ['logs', connectionId, activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/logs?connection_id=${connectionId}&page=${activePage}&limit=${itemsPerPage}&search=${search}`);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json();
    },
    refetchInterval: 3000,
  });

  const logs = (logsResponse as any)?.data || [];
  const totalItems = (logsResponse as any)?.total || 0;

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const clearLogsMutation = useMutation({
    mutationFn: async () => {
      if (!window.confirm('Are you sure you want to clear all logs for this connection?')) return;
      const res = await apiFetch(`${API_BASE}/logs?connection_id=${connectionId}`, { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to clear logs');
    },
    onSuccess: () => {
      refetchLogs();
      notifications.show({ title: 'Success', message: 'Logs cleared', color: 'green' });
    }
  });

  const { data: transformationsResponse } = useQuery({
    queryKey: ['transformations'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations`);
      return res.json();
    }
  });

  const transformations = transformationsResponse?.data || [];

  const selectedTransformations = transformations?.filter((t: any) => connection?.transformation_ids?.includes(t.id)) || [];

  const sinks = allSinks?.filter((s: any) => connection?.sink_ids?.includes(s.id)) || [];

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
        <Breadcrumbs>
          <Anchor onClick={() => navigate({ to: '/connections' })}>Connections</Anchor>
          <Text>{connection?.name || connectionId}</Text>
        </Breadcrumbs>

        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group justify="space-between">
            <Group gap="sm">
              <IconActivity size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box>
                <Title order={2} fw={800}>{connection?.name || 'Connection Detail'}</Title>
                <Text size="sm" c="dimmed">Detailed view of data flow and activity for this connection.</Text>
              </Box>
            </Group>
            <Badge 
              variant="filled" 
              color={!connection?.active ? 'gray' : 
                     ((liveStatus?.engine_status || connection?.status) === 'connecting' ? 'orange' :
                      ((liveStatus?.engine_status || connection?.status)?.startsWith('reconnecting') ? 'orange' : 
                       ((liveStatus?.engine_status || connection?.status) === 'running' ? 'green' : 
                        ((liveStatus?.engine_status || connection?.status) === 'error' || (!liveStatus && connection?.status === 'shutdown') ? 'red' : 'blue'))))}
              size="lg"
              radius="sm"
            >
              {!connection?.active ? 'STOPPED' : 
               ((liveStatus?.engine_status || connection?.status) === 'connecting' ? 'CONNECTING' :
                ((liveStatus?.engine_status || connection?.status)?.startsWith('reconnecting') ? 'RECONNECTING' : 
                 ((liveStatus?.engine_status || connection?.status) === 'running' ? 'RUNNING' : 
                  ((liveStatus?.engine_status || connection?.status) === 'error' || (!liveStatus && connection?.status === 'shutdown') ? 'SHUTDOWN' : 'ACTIVE'))))}
            </Badge>
          </Group>
        </Paper>

        <Paper p="md" withBorder radius="md">
          <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md">
            <Box>
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Virtual Host</Text>
              <Text fw={600}>{connection?.vhost || '-'}</Text>
            </Box>
            <Box>
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Assigned Worker</Text>
              <Text fw={600} c={connection?.worker_id ? 'indigo' : 'inherit'}>
                {getWorkerName(connection?.worker_id)}
              </Text>
            </Box>
            <Box>
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Source Type</Text>
              <Group gap="xs">
                <ThemeIcon size="xs" variant="light" color="blue"><IconDatabase size="0.8rem" /></ThemeIcon>
                <Text fw={600}>{source?.type || 'Loading...'}</Text>
              </Group>
            </Box>
            <Box>
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Total Sinks</Text>
              <Group gap="xs">
                <ThemeIcon size="xs" variant="light" color="green"><IconSend size="0.8rem" /></ThemeIcon>
                <Text fw={600}>{connection?.sink_ids?.length || 0} Target(s)</Text>
              </Group>
            </Box>
          </SimpleGrid>
        </Paper>

        <Grid>
          <Grid.Col span={{ base: 12, md: 8 }}>
            <Paper p="xl" withBorder radius="md">
              <Group justify="space-between" mb="xl">
                <Title order={4}>Visual Data Flow</Title>
                <Group gap="xs">
                  <Group gap={4}>
                    <Box w={12} h={12} style={{ borderRadius: '2px', backgroundColor: '#3b82f6' }} />
                    <Text size="xs" c="dimmed">Healthy Source</Text>
                  </Group>
                  <Group gap={4}>
                    <Box w={12} h={12} style={{ borderRadius: '2px', backgroundColor: '#22c55e' }} />
                    <Text size="xs" c="dimmed">Healthy Sink</Text>
                  </Group>
                  <Group gap={4}>
                    <Box w={12} h={12} style={{ borderRadius: '2px', backgroundColor: '#f97316' }} />
                    <Text size="xs" c="dimmed">Connecting</Text>
                  </Group>
                  <Group gap={4}>
                    <Box w={12} h={12} style={{ borderRadius: '2px', backgroundColor: '#ef4444' }} />
                    <Text size="xs" c="dimmed">Offline</Text>
                  </Group>
                </Group>
              </Group>
              <Box h={300} style={{ position: 'relative', overflow: 'hidden' }}>
                <VisualGraph connection={connection} source={source} sinks={sinks} liveStatus={liveStatus} />
              </Box>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, md: 4 }}>
            <Paper p="md" withBorder radius="md" h="100%">
              <Title order={4} mb="md">Connection Metrics</Title>
              <Stack gap="md">
                <Paper p="md" withBorder radius="md" bg="gray.0">
                  <Group justify="space-between" mb="xs">
                    <Text size="xs" fw={700} c="dimmed">LIVE THROUGHPUT</Text>
                    <Badge variant="filled" color="indigo">{mps} msg/s</Badge>
                  </Group>
                  <Box h={50}>
                    <Sparkline data={mpsHistory} color="indigo" height={50} />
                  </Box>
                </Paper>

                <Divider label="Active Transformations" labelPosition="center" />
                {selectedTransformations.length > 0 && (
                  <Box>
                    <Text size="xs" fw={700} c="dimmed" mb="xs">INDEPENDENT</Text>
                    <Stack gap="xs">
                      {selectedTransformations.map((t: any) => (
                        <TransformationBadge key={t.id} transformation={t} />
                      ))}
                    </Stack>
                    <Divider my="md" />
                  </Box>
                )}
                <Box>
                  <Text size="xs" fw={700} c="dimmed" mb="xs">INLINE TRANSFORMATIONS</Text>
                  {connection?.transformations?.length > 0 ? (
                    <Stack gap="xs">
                      {connection.transformations.map((t: any, i: number) => (
                        <TransformationBadge key={i} transformation={t} />
                      ))}
                    </Stack>
                  ) : (
                    <Text size="xs" c="dimmed" fs="italic">No inline transformations</Text>
                  )}
                </Box>
              </Stack>
            </Paper>
          </Grid.Col>

          <Grid.Col span={12}>
            <Paper p="md" withBorder radius="md">
              <Group justify="space-between" mb="md">
                <Title order={4}>Connection Logs</Title>
                <Group>
                  <TextInput
                    placeholder="Search logs..."
                    size="xs"
                    leftSection={<IconSearch size="0.8rem" />}
                    value={search}
                    onChange={(e) => {
                      setSearch(e.currentTarget.value);
                      setPage(1);
                    }}
                    style={{ width: 250 }}
                  />
                  <ActionIcon variant="light" color="blue" onClick={() => refetchLogs()} loading={isFetchingLogs} title="Refresh logs">
                    <IconRefresh size="1.2rem" />
                  </ActionIcon>
                  <ActionIcon variant="light" color="red" onClick={() => clearLogsMutation.mutate()} loading={clearLogsMutation.isPending} title="Clear connection logs">
                    <IconTrash size="1.2rem" />
                  </ActionIcon>
                </Group>
              </Group>
              <Box style={{ overflowX: 'auto' }}>
                <Table verticalSpacing="sm">
                  <Table.Thead bg="gray.0">
                    <Table.Tr>
                      <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                      <Table.Th style={{ width: 80 }}>Level</Table.Th>
                      <Table.Th style={{ width: 120 }}>Action</Table.Th>
                      <Table.Th>Message</Table.Th>
                      <Table.Th style={{ width: 80 }}>Details</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {logs?.length === 0 ? (
                      <Table.Tr>
                        <Table.Td colSpan={5} py="xl">
                          <Text c="dimmed" ta="center">{search ? 'No logs match your search' : 'No logs found for this connection.'}</Text>
                        </Table.Td>
                      </Table.Tr>
                    ) : (
                      logs?.map((log: any) => (
                        <Table.Tr key={log.id}>
                          <Table.Td>
                            <Text size="xs">{new Date(log.timestamp).toLocaleString()}</Text>
                          </Table.Td>
                          <Table.Td>
                            <Badge color={getLevelColor(log.level)} variant="light" size="xs">
                              {log.level}
                            </Badge>
                          </Table.Td>
                          <Table.Td>
                            {log.action ? (
                              <Badge variant="outline" color="blue" size="xs" radius="xs" style={{ textTransform: 'none' }}>
                                {log.action}
                              </Badge>
                            ) : (
                              <Text size="xs" c="dimmed">-</Text>
                            )}
                          </Table.Td>
                          <Table.Td>
                            <Text size="xs" fw={500}>{log.message}</Text>
                          </Table.Td>
                          <Table.Td>
                            <Tooltip label="View Full Details">
                              <ActionIcon variant="light" color="blue" size="sm" onClick={() => viewDetails(log)}>
                                <IconEye size="1rem" />
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
                    <Pagination total={totalPages} value={activePage} onChange={setPage} size="sm" radius="md" />
                  </Group>
                )}
              </Box>
            </Paper>
          </Grid.Col>
        </Grid>
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
              {selectedLog.connection_id && (
                <Box>
                  <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Connection ID</Text>
                  <Code block fz="xs">{selectedLog.connection_id}</Code>
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

function TransformationBadge({ transformation: t }: { transformation: any }) {
  const icon = t.type === 'advanced' ? <IconWand size="0.8rem" /> : 
               t.type === 'mapping' ? <IconArrowsDiff size="0.8rem" /> : 
               t.type === 'filter_operation' ? <IconFilter size="0.8rem" /> : 
               t.type === 'http' ? <IconWorld size="0.8rem" /> :
               t.type === 'sql' ? <IconDatabase size="0.8rem" /> :
               t.type === 'pipeline' ? <IconRoute size="0.8rem" /> :
               <IconTableAlias size="0.8rem" />;
  
  const color = t.type === 'advanced' ? 'indigo' : 
                t.type === 'mapping' ? 'blue' : 
                t.type === 'filter_operation' ? 'orange' : 
                t.type === 'http' ? 'teal' :
                t.type === 'sql' ? 'cyan' :
                t.type === 'pipeline' ? 'grape' : 'gray';

  return (
    <Tooltip label={
      <Stack gap={2}>
        {t.name && <Text size="xs" fw={700}>{t.name}</Text>}
        {t.type === 'pipeline' ? (
          <Text size="xs">{t.steps?.length || 0} steps</Text>
        ) : (
          Object.entries(t.config || {}).map(([k, v]: [string, any]) => (
            <Text key={k} size="xs"><Code>{k}</Code>: {v}</Text>
          ))
        )}
      </Stack>
    } position="left" withArrow>
      <Paper withBorder p={8} radius="sm">
        <Group gap="xs">
          <ThemeIcon size="sm" variant="light" color={color}>{icon}</ThemeIcon>
          <Text size="xs" fw={700} style={{ textTransform: 'uppercase' }}>
            {t.name || t.type.replace('_', ' ')}
          </Text>
        </Group>
      </Paper>
    </Tooltip>
  );
}

function VisualGraph({ connection, source, sinks, liveStatus }: any) {
  const isActive = connection?.active;
  const status = liveStatus?.engine_status || connection?.status || '';

  const groups = useMemo(() => {
    let transformation_groups = connection?.transformation_groups || [];
    if (typeof transformation_groups === 'string') {
      try {
        transformation_groups = JSON.parse(transformation_groups);
      } catch (e) {
        transformation_groups = [];
      }
    }
    return transformation_groups;
  }, [connection]);

  const getSourceState = () => {
    const sourceStatus = liveStatus?.source_status;
    if (sourceStatus === 'running') return 'healthy';
    if (sourceStatus === 'error' || (sourceStatus && sourceStatus.startsWith('error'))) return 'error';
    if (source && !source.active) return 'error';
    if (status === 'reconnecting:source' || status === 'connecting') return 'transitional';
    if (isActive && !liveStatus) return 'transitional';
    return isActive ? 'healthy' : 'error';
  };

  const getSinkState = (id: string) => {
    const sink = sinks.find((s: any) => s.id === id);
    const sinkStatus = liveStatus?.sink_statuses?.[id];
    if (sinkStatus === 'running') return 'healthy';
    if (sinkStatus === 'error' || (sinkStatus && sinkStatus.startsWith('error'))) return 'error';
    if (sink && !sink.active) return 'error';
    if (status === `reconnecting:sink:${id}` || status === 'connecting') return 'transitional';
    if (isActive && !liveStatus) return 'transitional';
    return isActive ? 'healthy' : 'error';
  };

  const sourceState = getSourceState();
  const sourceColors = (Object as any).assign({
    healthy: { fill: "#eff6ff", stroke: "#3b82f6", icon: "#3b82f6", text: "inherit" },
    transitional: { fill: "#fff7ed", stroke: "#f97316", icon: "#f97316", text: "#f97316" },
    error: { fill: "#fef2f2", stroke: "#ef4444", icon: "#ef4444", text: "#ef4444" }
  }, {})[sourceState];

  const hasBranches = groups.length > 0;
  const totalRows = Math.max(sinks.length, groups.length, 1);
  const height = Math.max(400, totalRows * 100);
  const centerY = height / 2;

  // Column X-coordinates
  const colSource = 50;
  const colPipeline = 250;
  const colBranches = 450;
  const colSinks = 700;

  return (
    <svg width="100%" height="100%" viewBox={`0 0 850 ${height}`} style={{ overflow: 'visible' }}>
      <defs>
        <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto">
          <polygon points="0 0, 10 3.5, 0 7" fill="#cbd5e1" />
        </marker>
        <linearGradient id="flowGradient" x1="0%" y1="0%" x2="100%" y2="0%">
          <stop offset="0%" stopColor="#3b82f6" stopOpacity="0" />
          <stop offset="50%" stopColor="#3b82f6" stopOpacity="1" />
          <stop offset="100%" stopColor="#3b82f6" stopOpacity="0" />
        </linearGradient>
      </defs>

      {/* Connection Lines */}
      {/* Source to Pipeline */}
      <path d={`M ${colSource + 100} ${centerY} L ${colPipeline} ${centerY}`} stroke="#cbd5e1" strokeWidth="2" markerEnd="url(#arrowhead)" fill="none" />

      {/* Global Pipeline to Branches or Sinks */}
      {!hasBranches ? (
        sinks.map((_: any, i: number) => {
          const y = centerY + (i - (sinks.length - 1) / 2) * 100;
          return (
            <path key={i} d={`M ${colPipeline + 100} ${centerY} L ${colPipeline + 130} ${centerY} L ${colPipeline + 130} ${y} L ${colSinks} ${y}`} stroke="#cbd5e1" strokeWidth="2" fill="none" markerEnd="url(#arrowhead)" />
          );
        })
      ) : (
        groups.map((group: any, i: number) => {
          const yGroup = centerY + (i - (groups.length - 1) / 2) * 100;
          return (
            <g key={i}>
              <path d={`M ${colPipeline + 100} ${centerY} L ${colPipeline + 130} ${centerY} L ${colPipeline + 130} ${yGroup} L ${colBranches} ${yGroup}`} stroke="#cbd5e1" strokeWidth="2" fill="none" markerEnd="url(#arrowhead)" />
              {/* Branch to its Sinks */}
              {(group.sink_ids || []).map((sinkId: string) => {
                const sinkIdx = sinks.findIndex((s: any) => s.id === sinkId);
                if (sinkIdx === -1) return null;
                const ySink = centerY + (sinkIdx - (sinks.length - 1) / 2) * 100;
                return (
                  <path key={sinkId} d={`M ${colBranches + 120} ${yGroup} L ${colBranches + 140} ${yGroup} L ${colBranches + 140} ${ySink} L ${colSinks} ${ySink}`} stroke="#cbd5e1" strokeWidth="2" fill="none" markerEnd="url(#arrowhead)" />
                );
              })}
            </g>
          );
        })
      )}

      {/* Animated Flow Dots */}
      {isActive && status === 'running' && (
        <>
          <circle r="3" fill="#3b82f6">
            <animateMotion dur="1.5s" repeatCount="indefinite" path={`M ${colSource + 100} ${centerY} L ${colPipeline} ${centerY}`} />
          </circle>
          {!hasBranches ? (
            sinks.map((_: any, i: number) => {
              const y = centerY + (i - (sinks.length - 1) / 2) * 100;
              return (
                <circle key={i} r="3" fill="#3b82f6">
                  <animateMotion dur="2s" repeatCount="indefinite" begin={`${i * 0.3}s`} path={`M ${colPipeline + 100} ${centerY} L ${colPipeline + 130} ${centerY} L ${colPipeline + 130} ${y} L ${colSinks} ${y}`} />
                </circle>
              );
            })
          ) : (
             groups.map((group: any, i: number) => {
               const yGroup = centerY + (i - (groups.length - 1) / 2) * 100;
               return (group.sink_ids || []).map((sinkId: string) => {
                 const sinkIdx = sinks.findIndex((s: any) => s.id === sinkId);
                 if (sinkIdx === -1) return null;
                 const ySink = centerY + (sinkIdx - (sinks.length - 1) / 2) * 100;
                 return (
                   <circle key={`${i}-${sinkId}`} r="3" fill="#3b82f6">
                     <animateMotion dur="2.5s" repeatCount="indefinite" begin={`${i * 0.4}s`} path={`M ${colPipeline + 100} ${centerY} L ${colPipeline + 130} ${centerY} L ${colPipeline + 130} ${yGroup} L ${colBranches} ${yGroup} L ${colBranches + 120} ${yGroup} L ${colBranches + 140} ${yGroup} L ${colBranches + 140} ${ySink} L ${colSinks} ${ySink}`} />
                   </circle>
                 );
               });
             })
          )}
        </>
      )}

      {/* Nodes */}
      {/* Source Node */}
      <g transform={`translate(${colSource}, ${centerY - 40})`}>
        <rect width="100" height="80" rx="8" fill={sourceColors.fill} stroke={sourceColors.stroke} strokeWidth="2" />
        <foreignObject x="0" y="0" width="100" height="80">
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px', textAlign: 'center' }}>
            <IconDatabase size="1.2rem" color={sourceColors.icon} />
            <Text size="10px" fw={700} style={{ color: sourceColors.text }} truncate>{source?.name || 'Source'}</Text>
            <Text size="8px" c="dimmed">{source?.type}</Text>
          </div>
        </foreignObject>
      </g>

      {/* Global Pipeline Node */}
      <g transform={`translate(${colPipeline}, ${centerY - 40})`}>
        <rect width="100" height="80" rx="8" fill="#f8fafc" stroke="#94a3b8" strokeWidth="2" strokeDasharray="4" />
        <foreignObject x="0" y="0" width="100" height="80">
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px', textAlign: 'center' }}>
            <IconSettings size="1.2rem" color="#64748b" />
            <Text size="10px" fw={700}>Pipeline</Text>
            <Text size="8px" c="dimmed">{(connection?.transformations?.length || 0) + (connection?.transformation_ids?.length || 0)} steps</Text>
          </div>
        </foreignObject>
      </g>

      {/* Branch Nodes */}
      {hasBranches && groups.map((group: any, i: number) => {
        const y = centerY - 40 + (i - (groups.length - 1) / 2) * 100;
        return (
          <g key={i} transform={`translate(${colBranches}, ${y})`}>
            <rect width="120" height="80" rx="8" fill="#fdf4ff" stroke="#d946ef" strokeWidth="2" />
            <foreignObject x="0" y="0" width="120" height="80">
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px', textAlign: 'center' }}>
                <IconRoute size="1.2rem" color="#d946ef" />
                <Text size="10px" fw={700} truncate>{group.name || `Branch ${i+1}`}</Text>
                <Text size="8px" c="dimmed">{group.transformations?.length || 0} transforms</Text>
              </div>
            </foreignObject>
          </g>
        );
      })}

      {/* Sink Nodes */}
      {sinks.map((sink: any, i: number) => {
        const y = centerY - 40 + (i - (sinks.length - 1) / 2) * 100;
        const sinkState = getSinkState(sink.id);
        const sinkColors = (Object as any).assign({
          healthy: { fill: "#f0fdf4", stroke: "#22c55e", icon: "#22c55e", text: "inherit" },
          transitional: { fill: "#fff7ed", stroke: "#f97316", icon: "#f97316", text: "#f97316" },
          error: { fill: "#fef2f2", stroke: "#ef4444", icon: "#ef4444", text: "#ef4444" }
        }, {})[sinkState];

        return (
          <g key={sink.id} transform={`translate(${colSinks}, ${y})`}>
            <rect width="100" height="80" rx="8" fill={sinkColors.fill} stroke={sinkColors.stroke} strokeWidth="2" />
            <foreignObject x="0" y="0" width="100" height="80">
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px', textAlign: 'center' }}>
                <IconSend size="1.2rem" color={sinkColors.icon} />
                <Text size="10px" fw={700} style={{ color: sinkColors.text }} truncate>{sink.name}</Text>
                <Text size="8px" c="dimmed">{sink.type}</Text>
              </div>
            </foreignObject>
          </g>
        );
      })}
    </svg>
  );
}
