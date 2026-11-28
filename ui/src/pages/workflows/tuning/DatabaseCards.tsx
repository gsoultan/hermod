import { Alert, Box, Group, Loader, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { IconAlertCircle, IconCircleCheck, IconDatabase, IconUserExclamation } from '@tabler/icons-react'
import { useDatabases, type DatabaseCard, type DatabaseView } from '@/lib/insights'

interface DatabaseCardsProps {
  workflowId: string
  /** Step names by node ID, to name the step a card is about. */
  nodeNames: ReadonlyMap<string, string>
  enabled: boolean
}

function CardRow({ card, kind, nodeNames }: { card: DatabaseCard; kind: 'fixed' | 'needs'; nodeNames: ReadonlyMap<string, string> }) {
  const fixed = kind === 'fixed'
  const step = card.node_id ? nodeNames.get(card.node_id) || card.node_id : null
  return (
    <Group gap="sm" align="flex-start" wrap="nowrap" data-db-card={card.rule}>
      <ThemeIcon variant="light" color={fixed ? 'green' : 'orange'} size="md" radius="xl" style={{ flexShrink: 0 }}>
        {fixed ? <IconCircleCheck size="1rem" /> : <IconUserExclamation size="1rem" />}
      </ThemeIcon>
      <Box style={{ minWidth: 0 }}>
        <Text size="sm" fw={600} style={{ overflowWrap: 'anywhere' }}>{card.title}</Text>
        {step && <Text size="xs" c="dimmed">Step: {step}</Text>}
        <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{card.detail}</Text>
      </Box>
    </Group>
  )
}

function Column({ title, cards, kind, empty, nodeNames }: {
  title: string; cards: DatabaseCard[]; kind: 'fixed' | 'needs'; empty: string; nodeNames: ReadonlyMap<string, string>
}) {
  return (
    <Stack gap="sm">
      <Text size="xs" fw={700} tt="uppercase" c="dimmed">{title}</Text>
      {cards.length === 0
        ? <Text size="sm" c="dimmed">{empty}</Text>
        : cards.map((c, i) => <CardRow key={`${c.rule}:${c.node_id ?? ''}:${i}`} card={c} kind={kind} nodeNames={nodeNames} />)}
    </Stack>
  )
}

/** One line on how Hermod uses the database now, from what it tracks. */
function shareLine(db: DatabaseView): string {
  const parts: string[] = []
  if (db.learned > 0) parts.push(`Hermod gets ${db.learned} connections here`)
  if (db.limit > 0) parts.push(`runs up to ${db.limit} long queries at once`)
  parts.push(`${db.running} ${db.running === 1 ? 'query' : 'queries'} running now`)
  const line = parts.join(', ')
  return line.charAt(0).toUpperCase() + line.slice(1) + '.'
}

function DatabasePanel({ db, nodeNames }: { db: DatabaseView; nodeNames: ReadonlyMap<string, string> }) {
  return (
    <Paper withBorder radius="md" p={{ base: 'sm', sm: 'md' }} data-database={db.source_id}>
      <Stack gap="md">
        <Box>
          <Group gap="xs" wrap="nowrap">
            <IconDatabase size="1.1rem" style={{ flexShrink: 0 }} />
            <Title order={5} style={{ overflowWrap: 'anywhere' }}>{db.source}</Title>
          </Group>
          <Text size="sm" c="dimmed">{shareLine(db)}</Text>
        </Box>
        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
          <Column title="Fixed by Hermod" kind="fixed" cards={db.fixed} nodeNames={nodeNames}
            empty="Nothing to fix: no wait for a connection here lately." />
          <Column title="Needs you" kind="needs" cards={db.needs_you} nodeNames={nodeNames}
            empty="Nothing needs you here." />
        </SimpleGrid>
      </Stack>
    </Paper>
  )
}

/**
 * The databases this flow reaches: what Hermod did about each by itself,
 * and what only a person can change. It reads what Hermod tracks in memory,
 * refreshed every 10 seconds, and connects to nothing.
 */
export function DatabaseCards({ workflowId, nodeNames, enabled }: DatabaseCardsProps) {
  const q = useDatabases(workflowId, enabled)
  if (q.isLoading) return <Loader size="sm" />
  if (q.error) {
    return (
      <Alert color="yellow" icon={<IconAlertCircle size="1rem" />} title="Could not read the databases">
        {(q.error as Error).message}
      </Alert>
    )
  }
  const dbs = q.data?.databases ?? []
  if (dbs.length === 0) {
    return <Text size="sm" c="dimmed">This flow reaches no PostgreSQL database that Hermod tracks.</Text>
  }
  return (
    <Stack gap="md">
      {dbs.map((db) => <DatabasePanel key={db.source_id} db={db} nodeNames={nodeNames} />)}
    </Stack>
  )
}
