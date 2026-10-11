import { useCallback, useMemo, useState } from 'react'
import { Alert, Badge, Box, Grid, Group, Loader, Paper, Select, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconGitMerge, IconLock } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useVHost } from '@/context/VHostContext'
import { EmptyState } from '@/components/common/EmptyState'
import { errorStatus, getImpact, getLineage, impactKey, lineageKey, type LineageNode } from '@/lib/lineage'
import { LineageGraphView, kindWords } from './lineage/LineageGraphView'
import { ImpactPanel } from './lineage/ImpactPanel'

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`

/**
 * Data lineage of a vhost: every asset its workflows read and write, joined
 * into one graph, and — for the asset or workflow picked — everything
 * downstream of it. It answers "what breaks if I change this source?".
 */
export function LineagePage() {
  const navigate = useNavigate()
  const { selectedVHost } = useVHost()
  const vhost = selectedVHost || 'all'
  const scopeWords = vhost === 'all' ? 'every vhost you can see' : vhost
  const [selected, setSelected] = useState<string | null>(null)

  const graphQuery = useQuery({
    queryKey: lineageKey(vhost),
    queryFn: ({ signal }) => getLineage(vhost, signal),
    retry: false,
  })
  const graph = graphQuery.data

  const impactQuery = useQuery({
    queryKey: impactKey(vhost, selected ?? ''),
    queryFn: ({ signal }) => getImpact(vhost, selected as string, signal),
    enabled: !!selected && !!graph,
    retry: false,
  })

  const byID = useMemo(() => new Map<string, LineageNode>((graph?.nodes ?? []).map((n) => [n.id, n])), [graph])
  const options = useMemo(() => {
    const opt = (n: LineageNode) => ({ value: n.id, label: `${n.label} — ${kindWords(n)}${n.locator ? ` @ ${n.locator}` : ''}` })
    const nodes = graph?.nodes ?? []
    return [
      { group: 'Assets', items: nodes.filter((n) => n.kind !== 'workflow').map(opt) },
      { group: 'Workflows', items: nodes.filter((n) => n.kind === 'workflow').map(opt) },
    ].filter((g) => g.items.length > 0)
  }, [graph])

  const impacted = useMemo(() => {
    if (!selected || !impactQuery.data) return null
    return new Set([selected, ...impactQuery.data.impacted.map((i) => i.node.id)])
  }, [selected, impactQuery.data])

  const pick = useCallback((id: string) => setSelected(id), [])

  const status = errorStatus(graphQuery.error)

  return (
    <Box p="md" className="page-enter">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Group gap="sm" align="flex-start">
            <IconGitMerge size="2rem" color="var(--mantine-color-blue-filled)" aria-hidden />
            <Box style={{ flex: 1 }}>
              <Group gap="sm" align="center">
                <Title order={2} fw={800}>Data lineage</Title>
                <Badge variant="light" size="lg" radius="sm" tt="none" aria-label="VHost shown">{vhost}</Badge>
              </Group>
              <Text size="sm" c="dimmed">
                Built from the saved workflows of {scopeWords}: the tables, topics, files, endpoints and models they read
                and write. Pick an asset to see everything downstream of it — what breaks if it changes.
              </Text>
            </Box>
            {graph && graph.nodes.length > 0 && (
              <Text size="sm" fw={600}>{plural(graph.summary.workflows, 'workflow')} · {plural(graph.summary.assets, 'asset')}</Text>
            )}
          </Group>
        </Paper>

        {graphQuery.isLoading ? (
          <Group component="output" justify="center" p="xl" aria-label="Loading the lineage">
            <Loader size="sm" /><Text size="sm" c="dimmed">Loading the lineage…</Text>
          </Group>
        ) : status === 403 ? (
          <Alert color="yellow" icon={<IconLock size="1rem" />} title={`No access to ${vhost}`}>
            Your account has no role on this vhost, so its lineage is not shown. Choose another vhost, or ask an
            administrator for access.
          </Alert>
        ) : graphQuery.error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title={`The lineage of ${scopeWords} could not be loaded`}>
            {(graphQuery.error as Error).message}
          </Alert>
        ) : !graph || graph.nodes.length === 0 ? (
          <Paper withBorder radius="md">
            <EmptyState
              icon={<IconGitMerge size={28} />}
              title="No lineage to show yet"
              description={`Lineage is derived from saved workflows. ${scopeWords === vhost ? `${vhost} has` : 'There are'} none that read or write anything yet.`}
              action={{ label: 'Create a workflow', onClick: () => navigate({ to: '/workflows/new' }) }}
            />
          </Paper>
        ) : (
          <Grid gap="md">
            <Grid.Col span={{ base: 12, md: 4 }}>
              <Paper withBorder radius="md" p="md">
                <Stack gap="md">
                  <Select
                    label="Asset or workflow"
                    description="Everything downstream of it is highlighted."
                    placeholder="Search by name"
                    searchable
                    clearable
                    nothingFoundMessage="Nothing by that name in this scope"
                    data={options}
                    value={selected}
                    onChange={setSelected}
                    maxDropdownHeight={320}
                  />
                  {graph.summary.unresolved > 0 && (
                    <Text size="xs" c="dimmed">
                      {plural(graph.summary.unresolved, 'asset')} could not be located from their configuration
                      (a cron trigger, an e-mail sink, a connector type lineage does not read) and are shown by their record.
                      They cannot link one workflow to another.
                    </Text>
                  )}
                  {selected ? (
                    <ImpactPanel impact={impactQuery.data} loading={impactQuery.isLoading}
                      error={impactQuery.error as Error | null} labels={byID} />
                  ) : (
                    <Text size="sm" c="dimmed">Choose an asset above, or press one in the graph.</Text>
                  )}
                </Stack>
              </Paper>
            </Grid.Col>
            <Grid.Col span={{ base: 12, md: 8 }}>
              <Paper withBorder radius="md" style={{ height: 620, width: '100%', overflow: 'hidden' }}>
                <LineageGraphView graph={graph} selected={selected} impacted={impacted} onPick={pick} />
              </Paper>
            </Grid.Col>
          </Grid>
        )}
      </Stack>
    </Box>
  )
}
