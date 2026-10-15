import { useMemo, useState } from 'react'
import { Badge, Button, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon } from '@mantine/core'
import { IconCircleCheck, IconCircleDashed, IconAlertTriangle } from '@tabler/icons-react'
import { dataGuide, type GuideKind } from '@/lib/mlDataGuide'
import { INSIGHT_LABEL, learnFrom, matchGuide, type ColumnInsight, type ColumnShape, type InsightKind } from '@/lib/mlDataInsights'

const KIND_COLOR: Record<InsightKind, string> = {
  id: 'gray', number: 'blue', yes_no: 'grape', date: 'cyan', category: 'teal', text: 'orange', empty: 'red',
}

/** Past this many columns the cards fold, with a button to show them all. */
const FOLD_AT = 6

/**
 * How the data's columns fit a kind of model: each column the guide asks
 * for, the column that looks like it, and the columns left to learn from.
 */
export function GuideFit({ kind, columns }: { kind: GuideKind; columns: ColumnShape[] }) {
  const guide = dataGuide(kind)
  const matches = useMemo(() => matchGuide(guide, columns), [guide, columns])
  const predicts = guide.required.some((c) => c.key === 'features')
  const rest = predicts ? learnFrom(matches, columns) : []
  const missing = matches.filter((m) => m.required && !m.match)
  return (
    <Stack gap={6}>
      <Stack gap={6} component="ul" aria-label={`How the columns fit ${guide.title}`} style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {matches.map((m) => (
          <Group key={m.column.key} gap="xs" wrap="nowrap" component="li" align="flex-start">
            <ThemeIcon size="sm" radius="xl" variant="light" color={m.match ? 'green' : m.required ? 'red' : 'gray'} aria-hidden>
              {m.match ? <IconCircleCheck size="0.85rem" /> : <IconCircleDashed size="0.85rem" />}
            </ThemeIcon>
            <Text size="sm" style={{ flex: 1 }}>
              <Text span fw={500}>{m.column.name}</Text>
              {m.required ? '' : ' (optional)'}:{' '}
              {m.match
                ? <Text span ff="var(--mantine-font-family-monospace)" c="green">{m.match}</Text>
                : <Text span c={m.required ? 'red' : 'dimmed'}>no column looks like it</Text>}
            </Text>
          </Group>
        ))}
        {predicts && (
          <Group gap="xs" wrap="nowrap" component="li" align="flex-start">
            <ThemeIcon size="sm" radius="xl" variant="light" color={rest.length ? 'green' : 'red'} aria-hidden>
              {rest.length ? <IconCircleCheck size="0.85rem" /> : <IconCircleDashed size="0.85rem" />}
            </ThemeIcon>
            <Text size="sm" style={{ flex: 1 }}>
              <Text span fw={500}>Columns to learn from</Text>:{' '}
              {rest.length ? <Text span ff="var(--mantine-font-family-monospace)">{rest.join(', ')}</Text> : <Text span c="red">none yet</Text>}
            </Text>
          </Group>
        )}
      </Stack>
      {missing.length > 0 && (
        <Text size="xs" c="dimmed">
          A required column is missing. Rename a column to what it means, or add one that holds it; the guide beside this says what each holds.
        </Text>
      )}
    </Stack>
  )
}

function InsightCard({ insight }: { insight: ColumnInsight }) {
  const warn = insight.kind === 'empty' || insight.constant || insight.filled < 0.7
  return (
    <Paper withBorder radius="md" p="xs">
      <Stack gap={4}>
        <Group justify="space-between" gap={6} wrap="nowrap">
          <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
            {warn && <IconAlertTriangle size="0.9rem" color="var(--mantine-color-orange-filled)" aria-label="Needs a look" />}
            <Text size="sm" fw={600} ff="var(--mantine-font-family-monospace)" truncate="end">{insight.name}</Text>
          </Group>
          <Badge size="xs" variant="light" color={KIND_COLOR[insight.kind]} style={{ flexShrink: 0 }}>{INSIGHT_LABEL[insight.kind]}</Badge>
        </Group>
        <Text size="xs" c="dimmed">{insight.hint}</Text>
      </Stack>
    </Paper>
  )
}

/**
 * What a sample of rows says: how the columns fit the kind of model being
 * trained, then each column with what it holds and what to do about it.
 * `rows` is how many rows the sample has, said so nobody takes it for the
 * whole table.
 */
export function DataInsights({ insights, kind, rows }: { insights: ColumnInsight[]; kind: GuideKind; rows: number }) {
  const [all, setAll] = useState(false)
  const shapes = useMemo(() => insights.map((i) => ({ name: i.name, kind: i.kind })), [insights])
  const shown = all ? insights : insights.slice(0, FOLD_AT)
  const issues = insights.filter((i) => i.kind === 'empty' || i.constant || i.filled < 0.7).length
  return (
    <Stack gap="md">
      <Stack gap={6}>
        <Text size="sm" fw={600}>How it fits: {dataGuide(kind).title}</Text>
        <GuideFit kind={kind} columns={shapes} />
      </Stack>
      <Stack gap={6}>
        <Group justify="space-between" gap="xs">
          <Text size="sm" fw={600}>Each column</Text>
          <Text size="xs" c="dimmed">
            From {rows.toLocaleString()} rows{issues ? `; ${issues} ${issues === 1 ? 'column needs' : 'columns need'} a look` : ''}
          </Text>
        </Group>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" verticalSpacing="xs">
          {shown.map((i) => <InsightCard key={i.name} insight={i} />)}
        </SimpleGrid>
        {insights.length > FOLD_AT && (
          <Group>
            <Button size="compact-xs" variant="subtle" onClick={() => setAll((a) => !a)}>
              {all ? 'Show fewer' : `Show all ${insights.length} columns`}
            </Button>
          </Group>
        )}
      </Stack>
    </Stack>
  )
}
