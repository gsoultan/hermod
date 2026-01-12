import { useParams } from '@tanstack/react-router';
import { useSuspenseQuery, useQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { Title, Table, Group, Stack, Badge, Paper, Text, Box, ActionIcon, Tooltip, Breadcrumbs, Anchor, Grid, Divider, Code, ThemeIcon } from '@mantine/core';
import { IconActivity, IconDatabase, IconSend, IconSettings, IconRefresh, IconTableAlias, IconFilter, IconArrowsDiff, IconWand, IconWorld, IconRoute } from '@tabler/icons-react';
import { useNavigate } from '@tanstack/react-router';

const API_BASE = '/api';

export function ConnectionDetailPage() {
  const { connectionId } = useParams({ from: '/connections/$connectionId' });
  const navigate = useNavigate();

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

  const { data: allSinks } = useQuery({
    queryKey: ['sinks'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    },
  });

  const { data: logs, refetch: refetchLogs, isFetching: isFetchingLogs } = useQuery({
    queryKey: ['logs', connectionId],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/logs?connection_id=${connectionId}&limit=50`);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json();
    },
    refetchInterval: 3000,
  });

  const { data: transformations } = useQuery({
    queryKey: ['transformations'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations`);
      return res.json();
    }
  });

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
              color={connection?.active ? 'green' : 'gray'}
              size="lg"
              radius="sm"
            >
              {connection?.active ? 'RUNNING' : 'STOPPED'}
            </Badge>
          </Group>
        </Paper>

        <Grid>
          <Grid.Col span={{ base: 12, md: 8 }}>
            <Paper p="xl" withBorder radius="md">
              <Title order={4} mb="xl">Visual Data Flow</Title>
              <Box h={300} style={{ position: 'relative', overflow: 'hidden' }}>
                <VisualGraph connection={connection} source={source} sinks={sinks} />
              </Box>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, md: 4 }}>
            <Paper p="md" withBorder radius="md" h="100%">
              <Title order={4} mb="md">Active Transformations</Title>
              <Stack gap="md">
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
                <ActionIcon variant="light" onClick={() => refetchLogs()} loading={isFetchingLogs}>
                  <IconRefresh size="1.2rem" />
                </ActionIcon>
              </Group>
              <Box style={{ overflowX: 'auto' }}>
                <Table verticalSpacing="sm">
                  <Table.Thead bg="gray.0">
                    <Table.Tr>
                      <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                      <Table.Th style={{ width: 80 }}>Level</Table.Th>
                      <Table.Th>Message</Table.Th>
                      <Table.Th>Details</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {logs?.length === 0 ? (
                      <Table.Tr>
                        <Table.Td colSpan={4} py="xl">
                          <Text c="dimmed" ta="center">No logs found for this connection.</Text>
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
                            <Text size="xs" fw={500}>{log.message}</Text>
                          </Table.Td>
                          <Table.Td>
                            {log.data && (
                              <Tooltip label={log.data} multiline w={400} withArrow>
                                <Text size="xs" c="dimmed" style={{ 
                                  maxWidth: 300, 
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
              </Box>
            </Paper>
          </Grid.Col>
        </Grid>
      </Stack>
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

function VisualGraph({ connection, source, sinks }: any) {
  const isActive = connection?.active;
  const height = Math.max(300, sinks.length * 100);
  const centerY = height / 2;

  return (
    <svg width="100%" height="100%" viewBox={`0 0 800 ${height}`}>
      <defs>
        <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto">
          <polygon points="0 0, 10 3.5, 0 7" fill="#cbd5e1" />
        </marker>
      </defs>

      {/* Lines */}
      {/* Source to Buffer */}
      <path d={`M 150 ${centerY} L 300 ${centerY}`} stroke="#cbd5e1" strokeWidth="2" markerEnd="url(#arrowhead)" />
      
      {/* Buffer to Sinks */}
      {sinks.map((_: any, i: number) => {
        const y = centerY + (i - (sinks.length - 1) / 2) * 100;
        return (
          <path key={i} d={`M 500 ${centerY} L 550 ${centerY} L 550 ${y} L 650 ${y}`} stroke="#cbd5e1" strokeWidth="2" fill="none" markerEnd="url(#arrowhead)" />
        );
      })}

      {/* Animated Flow */}
      {isActive && (
        <>
          <circle r="4" fill="#3b82f6">
            <animateMotion dur="2s" repeatCount="indefinite" path={`M 150 ${centerY} L 300 ${centerY}`} />
          </circle>
          {sinks.map((_: any, i: number) => {
            const y = centerY + (i - (sinks.length - 1) / 2) * 100;
            return (
              <circle key={i} r="4" fill="#3b82f6">
                <animateMotion dur="2s" repeatCount="indefinite" path={`M 500 ${centerY} L 550 ${centerY} L 550 ${y} L 650 ${y}`} begin={`${i * 0.5}s`} />
              </circle>
            );
          })}
        </>
      )}

      {/* Source Node */}
      <g transform={`translate(50, ${centerY - 40})`}>
        <rect width="100" height="80" rx="8" fill="#eff6ff" stroke="#3b82f6" strokeWidth="2" />
        <foreignObject x="0" y="0" width="100" height="80">
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px' }}>
            <IconDatabase size="1.2rem" color="#3b82f6" />
            <div style={{ fontSize: '10px', fontWeight: 'bold', textAlign: 'center', marginTop: '2px', overflow: 'hidden', textOverflow: 'ellipsis', width: '100%' }}>{source?.name || 'Source'}</div>
            <div style={{ fontSize: '8px', color: '#64748b' }}>{source?.type}</div>
          </div>
        </foreignObject>
      </g>

      {/* Buffer Node */}
      <g transform={`translate(350, ${centerY - 40})`}>
        <rect width="100" height="80" rx="8" fill="#f8fafc" stroke="#94a3b8" strokeWidth="2" strokeDasharray="4" />
        <foreignObject x="0" y="0" width="100" height="80">
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px' }}>
            <IconSettings size="1.2rem" color="#64748b" />
            <div style={{ fontSize: '10px', fontWeight: 'bold' }}>Engine Buffer</div>
            <div style={{ fontSize: '8px', color: '#64748b', textAlign: 'center' }}>
              {(connection?.transformations?.length || 0) + (connection?.transformation_ids?.length || 0)} Total
            </div>
            <div style={{ fontSize: '7px', color: '#94a3b8' }}>Transformations</div>
          </div>
        </foreignObject>
      </g>

      {/* Sink Nodes */}
      {sinks.map((sink: any, i: number) => {
        const y = centerY - 40 + (i - (sinks.length - 1) / 2) * 100;
        return (
          <g key={sink.id} transform={`translate(650, ${y})`}>
            <rect width="100" height="80" rx="8" fill="#f0fdf4" stroke="#22c55e" strokeWidth="2" />
            <foreignObject x="0" y="0" width="100" height="80">
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '5px' }}>
                <IconSend size="1.2rem" color="#22c55e" />
                <div style={{ fontSize: '10px', fontWeight: 'bold', textAlign: 'center', marginTop: '2px', overflow: 'hidden', textOverflow: 'ellipsis', width: '100%' }}>{sink.name}</div>
                <div style={{ fontSize: '8px', color: '#64748b' }}>{sink.type}</div>
              </div>
            </foreignObject>
          </g>
        );
      })}
    </svg>
  );
}
