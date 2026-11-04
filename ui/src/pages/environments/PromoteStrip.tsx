import { Group, Paper, Text } from '@mantine/core'
import { IconArrowRight, IconCheck } from '@tabler/icons-react'
import { useLinks, useReady, type Link, type ReadyWorkflow } from '@/lib/environments'

export interface PromoteTarget {
  /** The link this workflow's vhost promotes through, or none. */
  link?: Link
  /** Set when it changed since it last went there. */
  ready?: ReadyWorkflow
}

/**
 * Where a workflow in vhost promotes to: the link for that vhost, else an
 * older link that serves every vhost, and whether it changed since it last
 * went there.
 */
export function usePromoteTarget(workflowId: string, vhost: string | undefined, enabled: boolean): PromoteTarget {
  const { data: links } = useLinks(enabled)
  const { data: lanes } = useReady(enabled)
  const v = vhost || 'default'
  const link = (links ?? []).find((l) => l.vhost === v) ?? (links ?? []).find((l) => l.vhost === '')
  const lane = (lanes ?? []).find((l) => l.link_id === link?.id)
  const ready = (lane?.workflows ?? []).find((w) => w.id === workflowId)
  return { link, ready }
}

/** One line on the flow page: this version here, and how far behind the next environment is. */
export function PromoteStrip({ here, target }: { here: string; target: PromoteTarget }) {
  const { link, ready } = target
  if (!link) return null
  const behind = ready ? ready.version - ready.last_sent_version : 0
  return (
    <Paper withBorder radius="md" px="md" py="xs" mt="md" aria-label={`${here} and ${link.name}`} component="section">
      <Group gap="sm" wrap="wrap">
        <Text size="sm"><b>{here}</b> (here){ready ? ` · v${ready.version}` : ''}</Text>
        <IconArrowRight size="0.9rem" aria-hidden />
        <Text size="sm"><b>{link.name}</b></Text>
        {!ready ? (
          <Group gap={4} wrap="nowrap">
            <IconCheck size="0.9rem" color="var(--mantine-color-green-7)" aria-hidden />
            <Text size="sm" c="dimmed">Up to date</Text>
          </Group>
        ) : ready.last_sent_version > 0 ? (
          <Text size="sm" c="orange.8">last sent v{ready.last_sent_version} · {behind} {behind === 1 ? 'version' : 'versions'} to promote</Text>
        ) : (
          <Text size="sm" c="blue.7">not there yet</Text>
        )}
      </Group>
    </Paper>
  )
}
