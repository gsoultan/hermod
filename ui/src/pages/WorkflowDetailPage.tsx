import { useState, useMemo, useEffect, useCallback } from 'react';
import ReactFlow, { 
  Background, 
  Controls, 
  MiniMap,
  Handle,
  Position,
  ReactFlowProvider,
  useNodesState,
  useEdgesState
} from 'reactflow';
import 'reactflow/dist/style.css';
import { 
  Title, Button, Group, Paper, Stack, Text, Box, Divider, Badge, ScrollArea, Flex,
  ThemeIcon, Table, ActionIcon, Tooltip, Pagination, TextInput, Tabs, Modal, Code,
  useMantineColorScheme
} from '@mantine/core';
import { 
  IconArrowLeft, IconDatabase, IconArrowsExchange, IconServer, 
  IconWorld, IconSettingsAutomation, IconFileSpreadsheet, IconCircles, IconList,
  IconGitBranch, IconVariable, IconRefresh, IconSearch, IconEye,
  IconChartBar, IconTerminal2, IconLayoutGrid, IconCloud
} from '@tabler/icons-react';
import { Link, useParams } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useDisclosure, useDebouncedValue } from '@mantine/hooks';
import dagre from 'dagre';

const API_BASE = '/api';

// Reusing Node components from WorkflowEditor
const SourceNode = ({ data }: any) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const getIcon = () => {
    if (data.type === 'webhook') return <IconWorld size="0.8rem" />;
    if (data.type === 'cron') return <IconSettingsAutomation size="0.8rem" />;
    if (data.type === 'csv') return <IconFileSpreadsheet size="0.8rem" />;
    if (data.type === 'kafka') return <IconCircles size="0.8rem" />;
    if (data.type === 'rabbitmq' || data.type === 'rabbitmq_queue') return <IconList size="0.8rem" />;
    if (data.type === 'redis') return <IconDatabase size="0.8rem" />;
    return <IconDatabase size="0.8rem" />;
  };

  const getColor = () => {
    if (data.type === 'webhook') return 'cyan';
    if (data.type === 'cron') return 'indigo';
    if (data.type === 'csv') return 'orange';
    if (data.type === 'kafka' || data.type === 'rabbitmq' || data.type === 'rabbitmq_queue' || data.type === 'nats') return 'red';
    if (data.type === 'redis' || data.type === 'pulsar' || data.type === 'kinesis') return 'grape';
    if (['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'db2'].includes(data.type)) return 'teal';
    return 'blue';
  };

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-${getColor()}-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-6)' : 'var(--mantine-color-white)',
      }}
    >
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color={getColor()} variant="light" size="sm">
               {getIcon()}
            </ThemeIcon>
            <Text size="xs" fw={700} c={getColor()}>TRIGGER</Text>
          </Group>
          {data.type && <Badge size="xs" variant="light">{data.type}</Badge>}
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const TransformationNode = ({ data }: any) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-violet-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-6)' : 'var(--mantine-color-white)',
      }}
    >
      <Handle type="target" position={Position.Left} style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} id="true" style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} id="false" style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} id="default" style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Bottom} id="error" style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color="violet" variant="light" size="sm">
              {data.isGroup ? <IconGitBranch size="0.8rem" /> : <IconVariable size="0.8rem" />}
            </ThemeIcon>
            <Text size="xs" fw={700} c="violet">{data.isGroup ? 'GROUP' : 'TRANSFORM'}</Text>
          </Group>
          <Badge size="xs" variant="light" color="violet">{data.type || 'map'}</Badge>
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const SinkNode = ({ data }: any) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const getIcon = () => {
    if (data.type === 'http') return <IconWorld size="0.8rem" />;
    if (['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'clickhouse', 'yugabyte'].includes(data.type)) return <IconDatabase size="0.8rem" />;
    if (['s3', 's3-parquet', 'ftp'].includes(data.type)) return <IconCloud size="0.8rem" />;
    return <IconServer size="0.8rem" />;
  };

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-green-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-6)' : 'var(--mantine-color-white)',
      }}
    >
      <Handle type="target" position={Position.Left} style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color="green" variant="light" size="sm">
               {getIcon()}
            </ThemeIcon>
            <Text size="xs" fw={700} c="green.7">SINK</Text>
          </Group>
          {data.type && <Badge size="xs" variant="light" color="green">{data.type}</Badge>}
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const nodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: TransformationNode,
  condition: TransformationNode, // Reuse transformation for now
  switch: TransformationNode,
  merge: TransformationNode,
  stateful: TransformationNode,
};

export function WorkflowDetailPage() {
  const { id } = useParams({ from: '/workflows/$id' }) as any;
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<string | null>('graph');
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

  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  const onLayout = useCallback(() => {
    const dagreGraph = new dagre.graphlib.Graph();
    dagreGraph.setDefaultEdgeLabel(() => ({}));
    dagreGraph.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 100 });

    nodes.forEach((node) => {
      dagreGraph.setNode(node.id, { width: 140, height: 70 });
    });

    edges.forEach((edge) => {
      dagreGraph.setEdge(edge.source, edge.target);
    });

    dagre.layout(dagreGraph);

    setNodes((nds) =>
      nds.map((node) => {
        const nodeWithPosition = dagreGraph.node(node.id);
        return {
          ...node,
          position: {
            x: nodeWithPosition.x - 70,
            y: nodeWithPosition.y - 35,
          },
        };
      })
    );
  }, [nodes, edges, setNodes]);

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
    const wsUrl = `${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data);
        setRealtimeLogs((prev) => [log, ...prev].slice(0, 100));
      } catch (err) {
        console.error('Failed to parse log update', err);
      }
    };

    return () => ws.close();
  }, [id, activeTab]);

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

  useEffect(() => {
    if (workflow) {
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
      setNodes(initialNodes);

      const initialEdges = (workflow.edges || []).map((edge: any) => ({
        id: edge.id,
        source: edge.source_id,
        target: edge.target_id,
        data: edge.config,
        animated: workflow.active,
      }));
      setEdges(initialEdges);
    }
  }, [workflow, setNodes, setEdges]);

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
                  {workflow.status && (
                    <Badge variant="outline" color={workflow.status === 'running' ? 'green' : 'orange'}>
                      {workflow.status}
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
              <Tabs.Tab value="logs" leftSection={<IconTerminal2 size="1rem" />}>Logs</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="graph" style={{ flex: 1, position: 'relative' }}>
              <ReactFlowProvider>
                <ReactFlow
                  nodes={nodes}
                  edges={edges}
                  onNodesChange={onNodesChange}
                  onEdgesChange={onEdgesChange}
                  nodeTypes={nodeTypes}
                  fitView
                >
                  <Background color={isDark ? '#333' : '#aaa'} gap={20} />
                  <Controls />
                  <MiniMap 
                    nodeColor={(n) => {
                      if (n.type === 'source') return 'var(--mantine-color-blue-6)';
                      if (n.type === 'sink') return 'var(--mantine-color-green-6)';
                      return 'var(--mantine-color-violet-6)';
                    }}
                    style={{
                      backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-white)',
                    }}
                  />
                </ReactFlow>
                <Box style={{ position: 'absolute', right: 10, top: 10, zIndex: 5 }}>
                  <Tooltip label="Smart Align">
                    <ActionIcon aria-label="Smart align" variant="light" size="lg" color="indigo" onClick={onLayout}>
                      <IconLayoutGrid size="1.2rem" />
                    </ActionIcon>
                  </Tooltip>
                </Box>
              </ReactFlowProvider>
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
                    <Table verticalSpacing="xs">
                      <Table.Thead>
                        <Table.Tr>
                          <Table.Th w={180}>Timestamp</Table.Th>
                          <Table.Th w={100}>Level</Table.Th>
                          <Table.Th>Message</Table.Th>
                          <Table.Th w={150}>Action</Table.Th>
                          <Table.Th w={80}></Table.Th>
                        </Table.Tr>
                      </Table.Thead>
                      <Table.Tbody>
                        {logs.map((log: any) => (
                          <Table.Tr key={log.id}>
                            <Table.Td>
                              <Text size="xs">{new Date(log.timestamp).toLocaleString()}</Text>
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
              <Text size="xs" c="dimmed">{new Date(selectedLog.timestamp).toLocaleString()}</Text>
            </Group>
            
            <Box>
              <Text fw={700} size="sm">Message</Text>
              <Paper withBorder p="xs" bg="gray.0">
                <Text size="sm">{selectedLog.message}</Text>
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
