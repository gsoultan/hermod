import { Alert, Badge, Group, Loader, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconCircleCheck } from '@tabler/icons-react'
import type { LineageImpact, LineageNode } from '@/lib/lineage'
import { relationLabel } from '@/lib/lineage'
import { kindWords } from './LineageGraphView'

/** How an asset downstream is reached, by the relation into it. */
const writtenWords: Record<string, string> = { writes: 'written by', dead_letter: 'dead-lettered by', trains: 'trained by' }

const plural =(n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`

interface Props {
  impact: LineageImpact | undefined
  loading: boolean
  error: Error | null
  /** Labels of every node in the graph, to spell out paths. */
  labels: Map<string, LineageNode>
}

/**
 * What breaks if the chosen asset changes: everything downstream of it,
 * nearest first, each with the path the change travels to reach it.
 */
export function ImpactPanel({ impact, loading, error, labels }: Props) {
  if (loading) {
    return (
      <Group component="output" gap="xs" aria-label="Loading the impact">
        <Loader size="xs" /><Text size="sm" c="dimmed">Working out what depends on it…</Text>
      </Group>
    )
  }
  if (error) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The impact could not be worked out">
        {error.message}
      </Alert>
    )
  }
  if (!impact) return null

  const root = impact.root
  if (impact.impacted.length === 0) {
    return (
      <Alert color="green" variant="light" icon={<IconCircleCheck size="1rem" />} title={`Nothing reads this downstream`}>
        No workflow in scope reads {root.label}, so changing it breaks nothing Hermod knows about.
      </Alert>
    )
  }

  const name = (id: string) => labels.get(id)?.label ?? id
  return (
    <Stack gap="xs">
      <Text size="sm">
        {plural(impact.workflows, 'workflow')} and {plural(impact.assets, 'asset')} are downstream of <b>{root.label}</b>.
        Nearest first: each is reached through the path under it.
      </Text>
      <Stack component="ol" gap={6} aria-label={`Downstream of ${root.label}`} style={{ listStyle: 'none', padding: 0, margin: 0 }}>
        {impact.impacted.map((it) => (
          <li key={it.node.id}>
            <Stack gap={2} p={8} style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 6 }}>
              <Group gap={6} wrap="nowrap" justify="space-between">
                <Text size="sm" fw={600} truncate data-testid="impact-label">{it.node.label}</Text>
                <Group gap={4} wrap="nowrap">
                  <Badge size="xs" variant="light" color={it.node.kind === 'workflow' ? 'orange' : 'blue'} tt="none">{kindWords(it.node)}</Badge>
                  <Badge size="xs" variant="outline" color="gray">hop {it.depth}</Badge>
                </Group>
              </Group>
              <Text size="xs" c="dimmed">
                {it.node.kind === 'workflow'
                  ? `${relationLabel(it.relation)} ${name(it.path[it.path.length - 2])}`
                  : `${writtenWords[it.relation] ?? 'written by'} ${name(it.path[it.path.length - 2])}`}
                {it.node.locator ? ` · ${it.node.locator}` : ''}
              </Text>
              <Text size="xs" c="dimmed" style={{ wordBreak: 'break-word' }}>{it.path.map(name).join(' → ')}</Text>
            </Stack>
          </li>
        ))}
      </Stack>
    </Stack>
  )
}
