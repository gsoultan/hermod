import { useState, useMemo, useEffect, useCallback, useRef } from 'react';
import { 
  ReactFlowProvider,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { 
  Title, Button, Group, Paper, Stack, Text, Box, Divider, Badge, ScrollArea, Flex,
  ThemeIcon, Table, ActionIcon, Tooltip, Pagination, TextInput, Tabs, Modal, Code,
  useMantineColorScheme, Grid, Loader, UnstyledButton, Alert, SimpleGrid
} from '@mantine/core';
import { Link, useParams } from '@tanstack/react-router';
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { getToken } from '@/auth/storage';
import { useDisclosure, useDebouncedValue } from '@mantine/hooks';
import { formatDateTime } from '@/utils/dateUtils';
import { normalizeWorkflowStatus } from '@/utils/workflowStatus';
import { 
  IconArrowLeft, IconArrowsExchange, IconChartBar, IconChevronRight, IconCircleCheck, IconCircleX, IconClock, IconEye, IconHistory, IconInfoCircle, IconRefresh, IconRotateDot, IconSearch, IconTerminal2, IconTimeline,
  IconBug, IconBrain, IconActivity
} from '@tabler/icons-react';
import { WorkflowDebugger } from './WorkflowDebugger';
import { DetailFlowCanvas } from './WorkflowEditor/components/DetailFlowCanvas';
const API_BASE = '/api';

export function WorkflowDetailPage() {
  const { id } = useParams({ from: '/workflows/$id' }) as any;
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<string | null>('graph');
  const [selectedTraceID, setSelectedTraceID] = useState<string | null>(null);
  const [tracePage, setTracePage] = useState(1);
  const TRACES_PER_PAGE = 50;

  const { data: traces, isLoading: isTracesLoading } = useQuery({
    queryKey: ['traces', id, tracePage],
    queryFn: async () => {
      const offset = (tracePage - 1) * TRACES_PER_PAGE;
      const res = await apiFetch(`/api/workflows/${id}/traces?limit=${TRACES_PER_PAGE}&offset=${offset}`);
      if (!res.ok) throw new Error('Failed to fetch traces');
      return res.json();
    },
    enabled: activeTab === 'traces',
  });

  const { data: versions, isLoading: isVersionsLoading } = useQuery({
    queryKey: ['versions', id],
    queryFn: async () => {
      const res = await apiFetch(`/api/workflows/${id}/versions`);
      if (!res.ok) throw new Error('Failed to fetch versions');
      return res.json();
    },
    enabled: activeTab === 'history',
  });

  const rollbackMutation = useMutation({
    mutationFn: async (version: number) => {
      if (!confirm(`Rollback workflow to version ${version}?`)) return;
      const res = await apiFetch(`/api/workflows/${id}/rollback/${version}`, {
        method: 'POST'
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', id] });
      queryClient.invalidateQueries({ queryKey: ['versions', id] });
      setActiveTab('graph');
    }
  });

  const { data: selectedTrace, isLoading: isTraceDetailLoading } = useQuery({
    queryKey: ['trace', id, selectedTraceID],
    queryFn: async () => {
      // Use query parameter for message_id to avoid issues with slashes in IDs (e.g. Postgres LSNs)
      const res = await apiFetch(`/api/workflows/${id}/traces/?message_id=${encodeURIComponent(selectedTraceID || '')}`);
      if (!res.ok) throw new Error('Failed to fetch trace detail');
      return res.json();
    },
    enabled: !!selectedTraceID,
  });
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  
  // Logs state
  const [search, setSearch] = useState('');
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 20;
  const [selectedLog, setSelectedLog] = useState<any>(null);
  const [opened, { open, close }] = useDisclosure(false);
  const [realtimeLogs, setRealtimeLogs] = useState<any[]>([]);
  const [engineStatus, setEngineStatus] = useState<any>(null);

  const [nodes, setNodesRaw, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdgesRaw, onEdgesChange] = useEdgesState<Edge>([]);

  const setNodes = useCallback((nds: any) => {
    setNodesRaw((current: Node[]) => {
      const next = typeof nds === 'function' ? nds(current) : nds;
      if (JSON.stringify(next) === JSON.stringify(current)) return current;
      return next;
    });
  }, [setNodesRaw]);

  const setEdges = useCallback((eds: any) => {
    setEdgesRaw((current: Edge[]) => {
      const next = typeof eds === 'function' ? eds(current) : eds;
      if (JSON.stringify(next) === JSON.stringify(current)) return current;
      return next;
    });
  }, [setEdgesRaw]);


  const { data: workflow, isLoading: isWorkflowLoading } = useQuery({
    queryKey: ['workflow', id],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}`);
      if (!res.ok) throw new Error('Failed to fetch workflow');
      const data = await res.json();
      return data;
    },
  });

  const wsActive = activeTab === 'logs' && activePage === 1 && !debouncedSearch;
  const { data: logsResponse, isFetching: isLogsFetching } = useQuery({
    queryKey: ['logs', 'workflow', id, debouncedSearch, activePage],
    queryFn: async () => {
      let url = `${API_BASE}/logs?workflow_id=${id}&page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(debouncedSearch || '')}`;
      const res = await apiFetch(url);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json();
    },
    enabled: activeTab === 'logs',
    refetchInterval: wsActive ? false : 5000,
    staleTime: 5000,
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    if (activeTab !== 'logs') return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `&token=${token}` : '';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}${tokenParam}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (Array.isArray(payload)) {
          setRealtimeLogs((prev) => {
            const combined = [...payload, ...prev];
            const seen = new Set<string>();
            const deduped: any[] = [];
            for (const l of combined) {
              const id = (l as any)?.id;
              if (id && !seen.has(id)) {
                seen.add(id);
                deduped.push(l);
              }
            }
            return deduped.slice(0, 100);
          });
        } else {
          setRealtimeLogs((prev) => {
            if (prev.some((l: any) => l.id === payload.id)) return prev;
            return [payload, ...prev].slice(0, 100);
          });
        }
      } catch (err) {
        console.error('Failed to parse log update', err);
      }
    };

    return () => ws.close();
  }, [id, activeTab]);

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `&token=${token}` : '';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status?workflow_id=${id}${tokenParam}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (payload.workflow_id === id || !payload.workflow_id) {
          setEngineStatus(payload);
        }
      } catch (err) {
        console.error('Failed to parse status update', err);
      }
    };

    return () => ws.close();
  }, [id]);

  const logs = useMemo(() => {
    const fetchedLogs = (logsResponse as any)?.data || [];
    // Combine fetched logs with real-time logs, avoiding duplicates if possible
    // For simplicity, if we are on page 1 and no search, we show real-time logs at the top
    if (activePage === 1 && !debouncedSearch) {
      const logIds = new Set(realtimeLogs.map(l => l.id));
      return [...realtimeLogs, ...fetchedLogs.filter((l: any) => !logIds.has(l.id))];
    }
    return fetchedLogs;
  }, [logsResponse, realtimeLogs, activePage, debouncedSearch]);

  const lastInitializedId = useRef<string | null>(null);

  useEffect(() => {
    if (workflow && lastInitializedId.current !== workflow.id) {
      lastInitializedId.current = workflow.id;
      const initialNodes = (workflow.nodes || []).map((node: any) => ({
        id: node.id,
        type: node.type,
        position: { x: node.x || 0, y: node.y || 0 },
        data: { 
          ...(node.config || {}), 
          ref_id: node.ref_id,
          label: node.config?.label || node.id
        },
        draggable: false,
        selectable: true,
      }));

      const initialEdges = (workflow.edges || []).map((edge: any) => ({
        id: edge.id,
        source: edge.source_id,
        target: edge.target_id,
        data: edge.config,
        animated: workflow.active,
      }));

      setNodes(initialNodes);
      setEdges(initialEdges);
    }
  }, [workflow?.id, workflow?.active, setNodes, setEdges]);

  const totalItems = (logsResponse as any)?.total || 0;
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

  const viewDetails = (log: any) => {
    setSelectedLog(log);
    open();
  };

  if (isWorkflowLoading) {
    return <Flex align="center" justify="center" h="100vh"><Text>Loading workflow details...</Text></Flex>;
  }

  if (!workflow) {
    return <Flex align="center" justify="center" h="100vh"><Text color="red">Workflow not found</Text></Flex>;
  }

  return (
    <Box p="md" style={{ height: 'calc(100vh - 80px)', display: 'flex', flexDirection: 'column' }}>
      <Stack gap="md" style={{ flex: 1 }}>
        <Paper p="md" withBorder radius="md">
          <Group justify="space-between">
            <Group>
              <Button 
                variant="subtle" 
                leftSection={<IconArrowLeft size="1rem" />} 
                component={Link} 
                to="/workflows"
              >
                Back
              </Button>
              <Divider orientation="vertical" />
              <Box>
                <Group gap="xs">
                  <Title order={3}>{workflow.name}</Title>
                  <Badge color={workflow.active ? 'green' : 'gray'}>
                    {workflow.active ? 'Active' : 'Inactive'}
                  </Badge>
                  {(() => {
                    // Prefer the real-time engine status streamed over the status
                    // WebSocket, falling back to the persisted workflow status.
                    const liveStatus = engineStatus?.engine_status || workflow.status;
                    if (!liveStatus) return null;
                    const { label, color } = normalizeWorkflowStatus(liveStatus);
                    return (
                      <Badge variant="outline" color={color}>
                        {label}
                      </Badge>
                    );
                  })()}
                  {workflow.cron && (
                    <Badge color="indigo" leftSection={<IconClock size="0.8rem" />}>
                      {workflow.cron}
                    </Badge>
                  )}
                </Group>
                <Text size="sm" c="dimmed">{workflow.vhost || 'Default VHost'}</Text>
              </Box>
            </Group>
            <Group>
              <Button 
                variant="light" 
                leftSection={<IconArrowsExchange size="1rem" />}
                component={Link}
                to="/workflows/$id/edit"
                params={{ id: id } as any}
              >
                Edit Workflow
              </Button>
            </Group>
          </Group>
        </Paper>

        <Paper withBorder radius="md" style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          <Tabs value={activeTab} onChange={setActiveTab} style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
            <Tabs.List px="md">
              <Tabs.Tab value="graph" leftSection={<IconChartBar size="1rem" />}>Graph View</Tabs.Tab>
              <Tabs.Tab value="traces" leftSection={<IconTimeline size="1rem" />}>Message Traces</Tabs.Tab>
              <Tabs.Tab value="history" leftSection={<IconHistory size="1rem" />}>History</Tabs.Tab>
              <Tabs.Tab value="logs" leftSection={<IconTerminal2 size="1rem" />}>Logs</Tabs.Tab>
              <Tabs.Tab value="debug" leftSection={<IconBug size="1rem" />}>Debugger</Tabs.Tab>
              <Tabs.Tab value="opt" leftSection={<IconBrain size="1rem" />}>Optimization</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="graph" style={{ flex: 1, position: 'relative' }}>
              <ReactFlowProvider>
                <DetailFlowCanvas 
                  nodes={nodes}
                  edges={edges}
                  onNodesChange={onNodesChange}
                  onEdgesChange={onEdgesChange}
                  setNodes={setNodes}
                />
              </ReactFlowProvider>
            </Tabs.Panel>

            <Tabs.Panel value="traces" style={{ flex: 1, overflow: 'hidden' }}>
              <Grid h="100%" gap={0}>
                <Grid.Col span={4} style={{ borderRight: `1px solid ${isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'}`, display: 'flex', flexDirection: 'column' }}>
                  <Stack gap={0} h="100%">
                    <Box p="md" style={{ borderBottom: `1px solid ${isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'}` }}>
                      <Text fw={700} size="sm">Recent Traces</Text>
                    </Box>
                    <ScrollArea style={{ flex: 1 }}>
                      {isTracesLoading ? (
                        <Group justify="center" p="xl"><Loader size="sm" /></Group>
                      ) : (traces as any)?.length === 0 ? (
                        <Text p="md" size="sm" c="dimmed" ta="center">No traces found.</Text>
                      ) : (Array.isArray(traces) ? (traces as any[]).map((t: any) => (
                        <UnstyledButton 
                          key={t.message_id} 
                          onClick={() => setSelectedTraceID(t.message_id)}
                          p="sm"
                          style={{ 
                            width: '100%', 
                            borderBottom: `1px solid ${isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-2)'}`,
                            backgroundColor: selectedTraceID === t.message_id ? (isDark ? 'var(--mantine-color-dark-5)' : 'var(--mantine-color-blue-0)') : 'transparent'
                          }}
                        >
                          <Group justify="space-between" wrap="nowrap">
                            <Box style={{ flex: 1, overflow: 'hidden' }}>
                              <Text size="sm" fw={600} truncate>{t.message_id}</Text>
                              <Text size="xs" c="dimmed">{formatDateTime(t.created_at)}</Text>
                            </Box>
                            <IconChevronRight size="1rem" color="var(--mantine-color-gray-5)" />
                          </Group>
                        </UnstyledButton>
                      )) : null)}
                    </ScrollArea>
                    <Group
                      justify="space-between"
                      p="xs"
                      style={{ borderTop: `1px solid ${isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'}` }}
                    >
                      <Button
                        size="xs"
                        variant="default"
                        leftSection={<IconArrowLeft size="0.9rem" />}
                        disabled={tracePage <= 1 || isTracesLoading}
                        onClick={() => setTracePage((p) => Math.max(1, p - 1))}
                      >
                        Previous
                      </Button>
                      <Text size="xs" c="dimmed">Page {tracePage}</Text>
                      <Button
                        size="xs"
                        variant="default"
                        rightSection={<IconChevronRight size="0.9rem" />}
                        disabled={!Array.isArray(traces) || (traces as any[]).length < TRACES_PER_PAGE || isTracesLoading}
                        onClick={() => setTracePage((p) => p + 1)}
                      >
                        Next
                      </Button>
                    </Group>
                  </Stack>
                </Grid.Col>
                <Grid.Col span={8} h="100%">
                  <ScrollArea h="100%" p="md">
                    {!selectedTraceID ? (
                      <Stack h="100%" align="center" justify="center" py={100} gap="xs">
                        <IconTimeline size="3rem" color="var(--mantine-color-gray-3)" />
                        <Text c="dimmed">Select a message trace to see its journey</Text>
                      </Stack>
                    ) : isTraceDetailLoading ? (
                      <Group justify="center" py={100}><Loader size="md" /></Group>
                    ) : selectedTrace ? (
                      <Stack gap="xl">
                        <Box>
                          <Title order={4} mb="xs">Message Journey</Title>
                          <Text size="sm" c="dimmed">Tracking ID: <Code>{selectedTraceID}</Code></Text>
                        </Box>
                        
                        <Stack gap={0}>
                          {Array.isArray((selectedTrace as any)?.steps) && ((selectedTrace as any).steps as any[]).map((step: any, idx: number) => (
                            <Box key={idx} style={{ 
                              borderLeft: '2px solid var(--mantine-color-blue-2)', 
                              paddingLeft: '2rem',
                              paddingBottom: '2rem',
                              position: 'relative'
                            }}>
                              <ThemeIcon 
                                variant="filled" 
                                size="md" 
                                radius="xl" 
                                color={step.error ? "red" : "blue"}
                                style={{ position: 'absolute', left: '-13px', top: '0' }}
                              >
                                {step.error ? <IconCircleX size="1rem" /> : <IconCircleCheck size="1rem" />}
                              </ThemeIcon>
                              
                              <Paper withBorder p="md" radius="md" shadow="xs">
                                <Stack gap="xs">
                                  <Group justify="space-between">
                                    <Text fw={700}>Node: {step.node_id}</Text>
                                    <Badge leftSection={<IconClock size="0.8rem" />} variant="light">
                                      {step.duration_ms || Math.round(step.duration / 1000000)}ms
                                    </Badge>
                                  </Group>
                                  
                                  <Text size="xs" c="dimmed">{formatDateTime(step.timestamp)}</Text>

                                  {step.error && (
                                    <Alert color="red" icon={<IconCircleX size="1rem" />} title="Processing Error">
                                      {step.error}
                                    </Alert>
                                  )}
                                  
                                  {(step.before || step.after) ? (
                                    <Grid gap="md">
                                      <Grid.Col span={{ base: 12, md: 6 }}>
                                        <Text size="xs" fw={700} mb={4} c="dimmed">INPUT / BEFORE</Text>
                                        <Paper withBorder p="xs" bg={isDark ? 'dark.8' : 'gray.0'}>
                                          <ScrollArea h={200}>
                                            <Code block bg="transparent" style={{ whiteSpace: 'pre' }}>{JSON.stringify(step.before || {}, null, 2)}</Code>
                                          </ScrollArea>
                                        </Paper>
                                      </Grid.Col>
                                      <Grid.Col span={{ base: 12, md: 6 }}>
                                        <Text size="xs" fw={700} mb={4} c="dimmed">OUTPUT / AFTER</Text>
                                        <Paper withBorder p="xs" bg={isDark ? 'dark.8' : 'gray.0'}>
                                          <ScrollArea h={200}>
                                            <Code block bg="transparent" style={{ whiteSpace: 'pre' }}>{JSON.stringify(step.after || step.data || {}, null, 2)}</Code>
                                          </ScrollArea>
                                        </Paper>
                                      </Grid.Col>
                                    </Grid>
                                  ) : step.data && (
                                    <Box>
                                      <Text size="xs" fw={700} mb={4} c="dimmed">DATA</Text>
                                      <Code block>{JSON.stringify(step.data, null, 2)}</Code>
                                    </Box>
                                  )}
                                </Stack>
                              </Paper>
                            </Box>
                          ))}
                        </Stack>
                      </Stack>
                    ) : (
                      <Alert color="red">Failed to load trace details.</Alert>
                    )}
                  </ScrollArea>
                </Grid.Col>
              </Grid>
            </Tabs.Panel>

            <Tabs.Panel value="history" style={{ flex: 1, overflow: 'hidden' }}>
              <ScrollArea h="100%" p="md">
                <Stack gap="md">
                  <Box>
                    <Title order={4} mb="xs">Workflow History</Title>
                    <Text size="sm" c="dimmed">View and restore previous versions of this workflow.</Text>
                  </Box>

                  {isVersionsLoading ? (
                    <Group justify="center" p="xl"><Loader size="md" /></Group>
                  ) : (versions as any)?.length === 0 ? (
                    <Alert color="gray" icon={<IconInfoCircle size="1rem" />}>
                      No history found for this workflow. Versions are created automatically when you save changes.
                    </Alert>
                  ) : (
                    <Table verticalSpacing="sm" highlightOnHover>
                      <Table.Thead>
                        <Table.Tr>
                          <Table.Th style={{ width: 80 }}>Version</Table.Th>
                          <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                          <Table.Th style={{ width: 120 }}>Created By</Table.Th>
                          <Table.Th>Message</Table.Th>
                          <Table.Th style={{ width: 150 }}>Actions</Table.Th>
                        </Table.Tr>
                      </Table.Thead>
                      <Table.Tbody>
                        {(Array.isArray(versions) ? versions : []).map((v: any) => (
                          <Table.Tr key={v.id}>
                            <Table.Td><Badge size="md">v{v.version}</Badge></Table.Td>
                            <Table.Td><Text size="sm">{formatDateTime(v.created_at)}</Text></Table.Td>
                            <Table.Td><Text size="sm">{v.created_by}</Text></Table.Td>
                            <Table.Td><Text size="sm">{v.message}</Text></Table.Td>
                            <Table.Td>
                              <Button 
                                variant="light" 
                                size="xs" 
                                color="orange" 
                                leftSection={<IconRotateDot size="0.8rem" />}
                                onClick={() => rollbackMutation.mutate(v.version)}
                                loading={rollbackMutation.isPending}
                              >
                                Restore
                              </Button>
                            </Table.Td>
                          </Table.Tr>
                        ))}
                      </Table.Tbody>
                    </Table>
                  )}
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            <Tabs.Panel value="debug" style={{ flex: 1, overflow: 'hidden' }}>
              <WorkflowDebugger workflowId={id} />
            </Tabs.Panel>

            <Tabs.Panel value="opt" p="md" style={{ flex: 1, overflow: 'hidden' }}>
              <ScrollArea h="100%">
                <Stack gap="xl">
                  <Box>
                    <Title order={4} mb="xs">AI Stability & Optimization</Title>
                    <Text size="sm" c="dimmed">Real-time performance metrics and AI-driven runtime tunings.</Text>
                  </Box>

                  <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
                    <Paper withBorder p="md" radius="md">
                      <Text size="xs" c="dimmed" fw={700} tt="uppercase">Throughput</Text>
                      <Group justify="space-between" align="flex-end" gap={0}>
                        <Text size="xl" fw={700}>{engineStatus?.throughput?.toFixed(1) || '0.0'} msg/s</Text>
                        <ThemeIcon color="blue" variant="light"><IconActivity size={16} /></ThemeIcon>
                      </Group>
                    </Paper>
                    <Paper withBorder p="md" radius="md">
                      <Text size="xs" c="dimmed" fw={700} tt="uppercase">Error Rate</Text>
                      <Group justify="space-between" align="flex-end" gap={0}>
                        <Text size="xl" fw={700} color={(engineStatus?.error_rate || 0) > 0.05 ? 'red' : 'inherit'}>
                          {((engineStatus?.error_rate || 0) * 100).toFixed(2)}%
                        </Text>
                        <ThemeIcon color="red" variant="light"><IconCircleX size={16} /></ThemeIcon>
                      </Group>
                    </Paper>
                    <Paper withBorder p="md" radius="md">
                      <Text size="xs" c="dimmed" fw={700} tt="uppercase">Backpressure</Text>
                      <Group justify="space-between" align="flex-end" gap={0}>
                        <Text size="xl" fw={700} color={(engineStatus?.backpressure || 0) > 0.8 ? 'orange' : 'inherit'}>
                          {((engineStatus?.backpressure || 0) * 100).toFixed(0)}%
                        </Text>
                        <ThemeIcon color="orange" variant="light"><IconArrowsExchange size={16} /></ThemeIcon>
                      </Group>
                    </Paper>
                  </SimpleGrid>

                  <Box>
                    <Title order={5} mb="md">Optimization Log</Title>
                    <Paper withBorder p="md" bg={isDark ? 'dark.8' : 'gray.0'}>
                      <Stack gap="xs">
                        {engineStatus?.backpressure > 0.8 && (
                          <Alert icon={<IconBrain size={16} />} title="AI Insight: Scaling Required" color="blue">
                            High backpressure detected. AI Optimizer is increasing concurrency to handle the load.
                          </Alert>
                        )}
                        {engineStatus?.error_rate > 0.05 && (
                          <Alert icon={<IconBrain size={16} />} title="AI Insight: Stability Issue" color="orange">
                            High error rate detected. AI is analyzing node failures and suggesting circuit breaker tunings.
                          </Alert>
                        )}
                        {!engineStatus && <Text c="dimmed" size="sm">Waiting for real-time optimization data...</Text>}
                        {engineStatus && engineStatus.throughput > 0 && (
                          <Text size="sm" c="green">✓ System performing within optimal parameters.</Text>
                        )}
                      </Stack>
                    </Paper>
                  </Box>
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            <Tabs.Panel value="logs" p="md" style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
               <Stack gap="md" style={{ height: '100%' }}>
                  <Group justify="space-between">
                    <TextInput 
                      placeholder="Search logs..." 
                      leftSection={<IconSearch size="0.8rem" />}
                      value={search}
                      onChange={(e) => {
                        setSearch(e.currentTarget.value);
                        setPage(1);
                        setRealtimeLogs([]);
                      }}
                      style={{ width: 300 }}
                    />
                    <Group>
                       {activePage === 1 && !search && (
                         <Badge variant="dot" color="green" size="sm">Live</Badge>
                       )}
                       <Tooltip label="Refresh">
                          <ActionIcon aria-label="Refresh workflow logs" variant="light" onClick={() => queryClient.invalidateQueries({ queryKey: ['logs', 'workflow', id] })} loading={isLogsFetching}>
                            <IconRefresh size="1rem" />
                          </ActionIcon>
                       </Tooltip>
                    </Group>
                  </Group>

                  <ScrollArea style={{ flex: 1 }}>
                    <Table verticalSpacing="xs" layout="fixed">
                      <Table.Thead>
                        <Table.Tr>
                          <Table.Th w={180}>Timestamp</Table.Th>
                          <Table.Th w={100}>Level</Table.Th>
                          <Table.Th>Message</Table.Th>
                          <Table.Th w={150}>Action</Table.Th>
                          <Table.Th w={50}></Table.Th>
                        </Table.Tr>
                      </Table.Thead>
                      <Table.Tbody>
                        {Array.isArray(logs) && logs.map((log: any) => (
                          <Table.Tr key={log.id}>
                            <Table.Td>
                              <Text size="xs" truncate="end">{formatDateTime(log.timestamp)}</Text>
                            </Table.Td>
                            <Table.Td>
                              <Badge color={getLevelColor(log.level)} variant="light" size="sm">
                                {log.level}
                              </Badge>
                            </Table.Td>
                            <Table.Td>
                              <Text size="sm" fw={500} truncate="end" title={log.message}>{log.message}</Text>
                            </Table.Td>
                            <Table.Td>
                              {log.action && <Badge variant="outline" size="xs">{log.action}</Badge>}
                            </Table.Td>
                            <Table.Td>
                              <ActionIcon aria-label="View log details" variant="subtle" onClick={() => viewDetails(log)}>
                                <IconEye size="1rem" />
                              </ActionIcon>
                            </Table.Td>
                          </Table.Tr>
                        ))}
                        {logs.length === 0 && !isLogsFetching && (
                          <Table.Tr>
                            <Table.Td colSpan={5}>
                              <Text ta="center" c="dimmed" py="xl">No logs found for this workflow.</Text>
                            </Table.Td>
                          </Table.Tr>
                        )}
                      </Table.Tbody>
                    </Table>
                  </ScrollArea>

                  {totalPages > 1 && (
                    <Group justify="center" pt="md">
                      <Pagination 
                        total={totalPages} 
                        value={activePage} 
                        onChange={(p) => {
                          setPage(p);
                          setRealtimeLogs([]);
                        }} 
                        size="sm" 
                      />
                    </Group>
                  )}
               </Stack>
            </Tabs.Panel>
          </Tabs>
        </Paper>
      </Stack>

      <Modal opened={opened} onClose={close} title="Log Details" size="lg">
        {selectedLog && (
          <Stack gap="md">
            <Group justify="space-between">
              <Badge color={getLevelColor(selectedLog.level)}>{selectedLog.level}</Badge>
              <Text size="xs" c="dimmed">{formatDateTime(selectedLog.timestamp)}</Text>
            </Group>
            
            <Box>
              <Text fw={700} size="sm" mb={4}>Message</Text>
              <Paper withBorder p="xs" bg="var(--mantine-color-body)">
                <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  {(() => {
                    try {
                      if (selectedLog.message.trim().startsWith('{') || selectedLog.message.trim().startsWith('[')) {
                        return JSON.stringify(JSON.parse(selectedLog.message), null, 2);
                      }
                    } catch (e) {}
                    return selectedLog.message;
                  })()}
                </Code>
              </Paper>
            </Box>

            <Group grow>
              <Box>
                <Text fw={700} size="sm">Action</Text>
                <Text size="sm">{selectedLog.action || '-'}</Text>
              </Box>
              <Box>
                <Text fw={700} size="sm">Workflow ID</Text>
                <Text size="sm" style={{ fontFamily: 'monospace' }}>{selectedLog.workflow_id || '-'}</Text>
              </Box>
            </Group>

            <Group grow>
              <Box>
                <Text fw={700} size="sm">Source ID</Text>
                <Text size="sm" style={{ fontFamily: 'monospace' }}>{selectedLog.source_id || '-'}</Text>
              </Box>
              <Box>
                <Text fw={700} size="sm">Sink ID</Text>
                <Text size="sm" style={{ fontFamily: 'monospace' }}>{selectedLog.sink_id || '-'}</Text>
              </Box>
            </Group>

            {selectedLog.data && (
              <Box>
                <Text fw={700} size="sm">Data</Text>
                <ScrollArea h="100%">
                  <Code block>{selectedLog.data}</Code>
                </ScrollArea>
              </Box>
            )}
          </Stack>
        )}
      </Modal>
    </Box>
  );
}


