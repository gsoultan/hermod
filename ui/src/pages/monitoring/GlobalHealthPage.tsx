import { IconActivity, IconCheck, IconCpu, IconNetwork, IconServer, IconAlertTriangle } from '@tabler/icons-react';
import { Title, Text, Stack, Paper, Group, Badge, SimpleGrid, ThemeIcon, Table, ScrollArea, Loader, Center } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { formatTime } from '@/utils/dateUtils'
import { EmptyState } from '@/components/common/EmptyState';
import { ResourceGauge } from '@/components/common/ResourceGauge';
import { NO_READING, formatCapacity, formatPercent, usageFraction } from '@/utils/metricFormat';

/**
 * One row of the mesh, as the API sends it.
 *
 * Workers carry the same resource fields the workers list and the dashboard
 * use. Mesh clusters are remote endpoints this platform does not measure, so
 * they carry none of them — which is why every one is optional here and why
 * the page has to tell an absent reading from a zero. It did not: the row
 * rendered `node.memory.toFixed(1)` and threw on the first cluster registered.
 */
interface MeshNode {
  id: string
  name?: string
  status: string
  type?: string
  region?: string
  endpoint?: string
  workflows?: number
  last_seen?: string
  cpu_usage?: number
  memory_usage?: number
  cpu_cores?: number
  memory_total_bytes?: number
  memory_used_bytes?: number
  storage_total_bytes?: number
  storage_used_bytes?: number
}

/**
 * Whether a node's readings describe the present.
 *
 * Capacity survives going offline — the machine has the cores it had — but
 * utilisation does not: it is whatever the node last said, and a filled ring
 * beside an OFFLINE badge reads as live. The fleet totals drop the node
 * entirely, matching the dashboard, which counts only workers seen inside the
 * last two minutes.
 */
function isReporting(node: MeshNode): boolean {
  return node.status !== 'offline'
}

/**
 * The busy share of every core in the fleet, or null if nothing reported one.
 *
 * Weighted by core count, over the reporting nodes that have cores. The
 * previous figure was a plain mean of `cpu` over every row — including mesh
 * clusters, which report nothing — so the fleet looked idler the more clusters
 * were registered, and a busy two-core box counted the same as an idle
 * thirty-core one.
 */
function fleetCPU(nodes: MeshNode[]): number | null {
  let cores = 0
  let busy = 0
  for (const node of nodes) {
    if (!isReporting(node) || !node.cpu_cores || node.cpu_cores <= 0) continue
    cores += node.cpu_cores
    busy += node.cpu_cores * (node.cpu_usage ?? 0)
  }
  return cores > 0 ? busy / cores : null
}

export default function GlobalHealthPage() {
  const { data: health, isLoading, error } = useQuery<MeshNode[]>({
    queryKey: ['mesh-health'],
    queryFn: async () => {
      const res = await apiFetch('/api/infra/mesh-health')
      return res.json()
    },
    refetchInterval: 10000
  })

  if (isLoading) return <Center h="100vh"><Loader size="xl" /></Center>
  if (error) return <Center h="100vh"><Text color="red">Failed to load mesh health</Text></Center>

  const nodes = health ?? [];
  const onlineWorkers = nodes.filter(w => w.status === 'online').length;
  const totalWorkflows = nodes.reduce((acc, w) => acc + (w.workflows ?? 0), 0);
  const cpu = fleetCPU(nodes);
  const totalCores = nodes.filter(isReporting).reduce((acc, w) => acc + (w.cpu_cores ?? 0), 0);

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
              <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase' }}>Fleet CPU</Text>
              <Text size="xl" fw={800}>{cpu === null ? NO_READING : formatPercent(cpu)}</Text>
              <Text size="xs" c="dimmed">
                {totalCores > 0 ? `of ${totalCores} core${totalCores === 1 ? '' : 's'}` : 'No node reported a core count'}
              </Text>
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

      <Paper withBorder p="md" radius="md" bg="red.0">
        <Stack gap="xs">
          <Group gap="xs">
            <IconAlertTriangle color="var(--mantine-color-red-6)" size="1.2rem" />
            <Text fw={700} c="red.7">Intelligent Data Quality Alerts</Text>
          </Group>
          <Text size="sm" c="red.9">
            Data Quality drift detected in <strong>Workflow: Transaction Processing</strong>. 
            Score dropped to 65% (historical average 98%). Source: Kafka (vhost: prod).
          </Text>
        </Stack>
      </Paper>

      <Paper withBorder radius="md">
        <ScrollArea h={500}>
          <Table.ScrollContainer minWidth={1100}>
            <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Node / Cluster</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Workload</Table.Th>
                <Table.Th>CPU</Table.Th>
                <Table.Th>Memory</Table.Th>
                <Table.Th>Storage</Table.Th>
                <Table.Th>Last Seen</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {!isLoading && (health?.length ?? 0) === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={7}>
                    <EmptyState
                      compact
                      icon={<IconServer size={22} />}
                      title="No clusters reporting"
                      description="Mesh health appears once at least one cluster or worker node is registered and reporting in."
                    />
                  </Table.Td>
                </Table.Tr>
              )}
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
                      <Text size="sm">{node.workflows ?? 0} workflows</Text>
                    </Group>
                  </Table.Td>
                  {/* The same gauge the workers list uses, so a machine reads
                      the same way on both screens. A mesh cluster reports none
                      of this and renders as no reading rather than as an idle
                      machine — the previous row threw outright on one, because
                      `node.memory.toFixed` is undefined when the field is. */}
                  <Table.Td>
                    <ResourceGauge
                      fraction={isReporting(node) ? node.cpu_usage ?? null : null}
                      caption={node.cpu_cores ? `${node.cpu_cores} core${node.cpu_cores === 1 ? '' : 's'}` : NO_READING}
                      tooltip="CPU"
                    />
                  </Table.Td>
                  <Table.Td>
                    <ResourceGauge
                      fraction={
                        isReporting(node)
                          ? usageFraction(node.memory_used_bytes ?? 0, node.memory_total_bytes ?? 0)
                            ?? node.memory_usage
                            ?? null
                          : null
                      }
                      caption={formatCapacity(node.memory_used_bytes ?? 0, node.memory_total_bytes ?? 0)}
                      tooltip="Memory"
                    />
                  </Table.Td>
                  <Table.Td>
                    <ResourceGauge
                      fraction={
                        isReporting(node)
                          ? usageFraction(node.storage_used_bytes ?? 0, node.storage_total_bytes ?? 0)
                          : null
                      }
                      caption={formatCapacity(node.storage_used_bytes ?? 0, node.storage_total_bytes ?? 0)}
                      tooltip="Storage (data directory)"
                    />
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{formatTime(node.last_seen)}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          </Table.ScrollContainer>
        </ScrollArea>
      </Paper>
    </Stack>
  )
}


