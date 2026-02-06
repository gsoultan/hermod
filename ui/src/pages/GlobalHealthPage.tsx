import { IconActivity, IconCheck, IconCpu, IconNetwork, IconServer } from '@tabler/icons-react';
import { Title, Text, Stack, Paper, Group, Badge, SimpleGrid, RingProgress, ThemeIcon, Table, ScrollArea, Loader, Center } from '@mantine/core'import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../api'
import { formatTime } from '../utils/dateUtils'

export default function GlobalHealthPage() {
  const { data: health, isLoading, error } = useQuery<any[]>({
    queryKey: ['mesh-health'],
    queryFn: async () => {
      const res = await apiFetch('/api/infra/mesh-health')
      return res.json()
    },
    refetchInterval: 10000
  })

  if (isLoading) return <Center h="100vh"><Loader size="xl" /></Center>
  if (error) return <Center h="100vh"><Text color="red">Failed to load mesh health</Text></Center>

  const onlineWorkers = health?.filter(w => w.status === 'online').length || 0;
  const totalWorkflows = health?.reduce((acc, w) => acc + w.workflows, 0) || 0;
  const avgCPU = (health?.reduce((acc, w) => acc + w.cpu, 0) || 0) / (health?.length || 1) * 100;

  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Stack gap={0}>
          <Title order={2}>Global Mesh Health</Title>
          <Text size="sm" c="dimmed">Real-time status of all Hermod clusters and worker nodes</Text>
        </Stack>
        <Badge size="xl" variant="dot" color="green">{onlineWorkers} Nodes Online</Badge>
      </Group>

      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
        <Paper withBorder p="md" radius="md">
          <Group justify="space-between">
            <div>
              <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Active Capacity</Text>
              <Text size="xl" fw={800}>{totalWorkflows} Workflows</Text>
            </div>
            <ThemeIcon color="blue" variant="light" size="xl" radius="md">
              <IconNetwork size="1.4rem" />
            </ThemeIcon>
          </Group>
        </Paper>

        <Paper withBorder p="md" radius="md">
          <Group justify="space-between">
            <div>
              <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Avg CPU Load</Text>
              <Text size="xl" fw={800}>{avgCPU.toFixed(1)}%</Text>
            </div>
            <ThemeIcon color="orange" variant="light" size="xl" radius="md">
              <IconCpu size="1.4rem" />
            </ThemeIcon>
          </Group>
        </Paper>

        <Paper withBorder p="md" radius="md">
          <Group justify="space-between">
            <div>
              <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>System Health</Text>
              <Text size="xl" fw={800}>Optimal</Text>
            </div>
            <ThemeIcon color="green" variant="light" size="xl" radius="md">
              <IconCheck size="1.4rem" />
            </ThemeIcon>
          </Group>
        </Paper>
      </SimpleGrid>

      <Paper withBorder radius="md">
        <ScrollArea h={500}>
          <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Node / Cluster</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Workload</Table.Th>
                <Table.Th>CPU</Table.Th>
                <Table.Th>Memory</Table.Th>
                <Table.Th>Last Seen</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {health?.map(node => (
                <Table.Tr key={node.id}>
                  <Table.Td>
                    <Group gap="sm">
                      <ThemeIcon size="sm" variant="light" color={node.status === 'online' ? 'blue' : 'gray'}>
                        <IconServer size={14} />
                      </ThemeIcon>
                      <div>
                        <Group gap="xs">
                          <Text size="sm" fw={500}>{node.name || 'Unnamed Node'}</Text>
                          {node.type === 'cluster' && (
                            <Badge size="xs" variant="outline" color="indigo">Mesh Cluster</Badge>
                          )}
                          {node.type === 'worker' && (
                            <Badge size="xs" variant="outline" color="blue">Worker</Badge>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed">
                          {node.id} {node.region ? `• ${node.region}` : ''} {node.endpoint ? `• ${node.endpoint}` : ''}
                        </Text>
                      </div>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Badge 
                      variant="light" 
                      color={node.status === 'online' ? 'green' : node.status === 'degraded' ? 'yellow' : 'red'}
                    >
                      {node.status}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Group gap="xs">
                      <IconActivity size={14} color="var(--mantine-color-blue-6)" />
                      <Text size="sm">{node.workflows} workflows</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Group gap="xs">
                      <RingProgress
                        size={35}
                        thickness={4}
                        roundCaps
                        sections={[{ value: node.cpu * 100, color: node.cpu > 0.8 ? 'red' : 'blue' }]}
                      />
                      <Text size="sm">{(node.cpu * 100).toFixed(0)}%</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{node.memory.toFixed(1)} MB</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{formatTime(node.last_seen)}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      </Paper>
    </Stack>
  )
}


