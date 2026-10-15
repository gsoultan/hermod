import { Badge, Button, Group, List, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { IconAlertTriangle, IconCircleCheck, IconInfoCircle, IconSchool } from '@tabler/icons-react'
import {
  KIND_LABEL, SEVERITY, goodFor, profileSummary, type ColumnProfile, type DatasetProfile, type ProfileWarning, type TrainStart,
} from '@/lib/mlDataProfile'
import { DataNeedsLink } from '../dataNeeds'
import { ColumnBars, ShareBars, StrengthBar, short } from './analysisCharts'

const pct = (share: number) => `${Math.round(share * 100)}%`

/** The analysis in a few sentences, the first the headline. */
export function SummaryCard({ profile }: { profile: DatasetProfile }) {
  const [head, ...rest] = profileSummary(profile)
  return (
    <Paper p="md" withBorder radius="md" bg="var(--mantine-color-default-hover)">
      <Stack gap={4}>
        <Group gap="xs">
          <Text fw={600}>{head}</Text>
          {profile.sampled && (
            <Badge variant="light" color="gray" tt="none">Sample of {profile.sample_rows.toLocaleString()} rows</Badge>
          )}
        </Group>
        {rest.map((line) => <Text key={line} size="sm">{line}</Text>)}
      </Stack>
    </Paper>
  )
}

/** What the data is good for, each with the button that starts that training. */
export function GoodForList({ profile, onTrain, canTrain, trainBlocked }: {
  profile: DatasetProfile
  onTrain?: (start: TrainStart) => void
  canTrain: boolean
  /** Why training cannot start, shown instead of the buttons. */
  trainBlocked?: string
}) {
  const hints = goodFor(profile)
  return (
    <Stack gap="xs" component="section" aria-labelledby="ml-analysis-good-for">
      <Title order={4} id="ml-analysis-good-for">What this data is good for</Title>
      {hints.length === 0 ? (
        <Stack gap={4}>
          <Text size="sm" c="dimmed">
            Hermod found no column to predict and no orders to recommend from. A prediction needs a column with the
            answer filled in for past rows; recommendations need one row per product a customer had.
          </Text>
          <DataNeedsLink kind="classification" />
        </Stack>
      ) : (
        <SimpleGrid cols={{ base: 1, sm: hints.length > 1 ? 2 : 1 }}>
          {hints.map((h) => (
            <Paper key={h.kind} p="sm" withBorder radius="md">
              <Stack gap={6} h="100%">
                <Group gap="xs" justify="space-between" wrap="nowrap">
                  <Text fw={600} size="sm">{h.title}</Text>
                  <Badge variant="light" color={h.ready ? 'green' : 'gray'} tt="none">{h.ready ? 'Ready' : 'Not yet'}</Badge>
                </Group>
                <Text size="sm" style={{ flex: 1 }}>{h.reason}</Text>
                <Group gap="xs">
                  {onTrain && canTrain && h.ready && (
                    <Button size="xs" leftSection={<IconSchool size="1rem" />} onClick={() => onTrain(h.start)}
                      aria-label={`Train: ${h.title.toLowerCase()}${h.start.target ? ` (${h.start.target})` : ''}`}>
                      Train this model
                    </Button>
                  )}
                  <DataNeedsLink kind={h.kind} />
                </Group>
              </Stack>
            </Paper>
          ))}
        </SimpleGrid>
      )}
      {!canTrain && trainBlocked && <Text size="xs" c="dimmed">{trainBlocked}</Text>}
    </Stack>
  )
}

const SEVERITY_ICON = { high: IconAlertTriangle, medium: IconAlertTriangle, low: IconInfoCircle }

/** The data's problems, worst first, each with how to fix it. */
export function WarningsList({ warnings }: { warnings: ProfileWarning[] }) {
  return (
    <Stack gap="xs" component="section" aria-labelledby="ml-analysis-checks">
      <Title order={4} id="ml-analysis-checks">Things to check</Title>
      {warnings.length === 0 ? (
        <Group gap="xs">
          <ThemeIcon variant="light" color="green" radius="xl" size="sm" aria-hidden><IconCircleCheck size="0.9rem" /></ThemeIcon>
          <Text size="sm">No problems found: no empty or constant columns, copied rows or odd values.</Text>
        </Group>
      ) : (
        <List spacing="sm" size="sm" center={false} aria-label="Things to check">
          {warnings.map((w, i) => {
            const Icon = SEVERITY_ICON[w.severity]
            const sev = SEVERITY[w.severity]
            return (
              <List.Item key={`${w.code}-${w.column ?? ''}-${i}`}
                icon={<ThemeIcon variant="light" color={sev.color} radius="xl" size="sm" aria-hidden><Icon size="0.9rem" /></ThemeIcon>}>
                <Stack gap={2}>
                  <Group gap="xs">
                    <Text size="sm">{w.message}</Text>
                    <Badge size="xs" variant="light" color={sev.color} tt="none">{sev.label}</Badge>
                  </Group>
                  <Text size="sm" c="dimmed"><Text span fw={500} c="var(--mantine-color-text)">How to fix: </Text>{w.fix}</Text>
                </Stack>
              </List.Item>
            )
          })}
        </List>
      )}
    </Stack>
  )
}

/** One column: its kind, gaps, values and a small chart of them. */
export function ColumnCard({ column: c }: { column: ColumnProfile }) {
  const n = c.numbers
  return (
    <Paper p="sm" withBorder radius="md" component="article" aria-label={`Column ${c.name}`}>
      <Stack gap="xs">
        <Group justify="space-between" gap="xs" wrap="nowrap">
          <Text fw={600} size="sm" ff="var(--mantine-font-family-monospace)" truncate="end" title={c.name}>{c.name}</Text>
          <Badge variant="light" tt="none">{KIND_LABEL[c.kind]}</Badge>
        </Group>
        <Text size="xs" c="dimmed">
          {c.missing ? `${pct(c.missing_share)} empty` : 'No empty cells'} · {c.distinct.toLocaleString()} different values
        </Text>
        {c.kind === 'number' && n && c.histogram && (
          <>
            <ColumnBars bars={c.histogram.map((b) => ({ label: `${short(b.from)} to ${short(b.to)}`, rows: b.rows }))}
              label={`How the values of ${c.name} spread from ${short(n.min)} to ${short(n.max)}`} from={short(n.min)} to={short(n.max)} />
            <Text size="xs">Median {short(n.median)} · average {short(n.mean)} · middle half {short(n.q1)} to {short(n.q3)}</Text>
          </>
        )}
        {c.kind === 'date' && c.dates && (
          <>
            <ColumnBars bars={c.dates.counts.map((b) => ({ label: b.label, rows: b.rows }))}
              label={`Rows per ${c.dates.per} of ${c.name}, ${c.dates.min} to ${c.dates.max}`}
              from={c.dates.counts[0]?.label ?? c.dates.min} to={c.dates.counts[c.dates.counts.length - 1]?.label ?? c.dates.max} />
            <Text size="xs">From {c.dates.min} to {c.dates.max} · rows per {c.dates.per}</Text>
          </>
        )}
        {c.top && c.top.length > 0 && c.kind !== 'number' && (
          <ShareBars items={c.top} other={c.other_share} label={`Most common values of ${c.name}`} />
        )}
        {c.kind === 'id' && <Text size="xs" c="dimmed">Almost every row has its own value: it says who or what a row is about.</Text>}
        {c.kind === 'empty' && <Text size="xs" c="dimmed">No row has a value in this column.</Text>}
      </Stack>
    </Paper>
  )
}

const strength = (r: number) => {
  const a = Math.abs(r)
  const how = a >= 0.8 ? 'Strongly' : a >= 0.5 ? 'Clearly' : 'Somewhat'
  return `${how} ${r >= 0 ? 'together: when one is high, so is the other' : 'opposite: when one is high, the other is low'}`
}

/** The numeric columns that move together, strongest first. */
export function Correlations({ profile }: { profile: DatasetProfile }) {
  if (!profile.correlations.length) {
    return <Text size="sm" c="dimmed">No two number columns move together noticeably.</Text>
  }
  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed">
        Number columns that rise and fall together. A column that moves almost exactly with what you predict may be
        the answer in disguise; two that move together exactly say the same thing twice.
      </Text>
      <Stack gap="xs" component="ul" aria-label="Related number columns" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {profile.correlations.map((c) => (
          <Stack key={`${c.a}-${c.b}`} gap={4} component="li">
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Text size="sm"><Text span ff="var(--mantine-font-family-monospace)">{c.a}</Text> and{' '}
                <Text span ff="var(--mantine-font-family-monospace)">{c.b}</Text></Text>
              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>r = {c.r.toFixed(2)}</Text>
            </Group>
            <StrengthBar r={c.r} />
            <Text size="xs" c="dimmed">{strength(c.r)}.</Text>
          </Stack>
        ))}
      </Stack>
    </Stack>
  )
}
