import { useId, useState } from 'react'
import { Accordion, Alert, Badge, Group, List, Paper, Progress, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { RECOMMENDATION, algorithmLabel, type LeaderboardEntry, type TrainResult } from '@/lib/mlModels'
import { isScenarioVersion } from '@/lib/mlRecommend'
import { MetricsTable, formatMetric } from './metrics'
import { ModelTestModal } from './ModelTestModal'
import { NextSteps, servingCurl, servingCurlAsync } from './nextSteps'
import { RecommendResultView } from './recommendResult'
import { ScenarioResultView } from './scenarioResult'
import { VERDICT_TEXT, baselineSentence, headline, verdict } from './trainVerdict'

const METRIC_WORDS: Record<string, string> = { balanced_accuracy: 'balanced accuracy', r2: 'R²' }

const seconds = (s: number) => (s < 1 ? 'under a second' : s < 90 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`)

/**
 * A training's outcome in plain words: how often it is right (or how far off),
 * whether that is good next to guessing, what mattered, what "auto" tried, and
 * what to do next. The raw metrics sit behind Details. Everything a server
 * older than these fields leaves out is simply not shown. A recommender's
 * outcome is read against recommending the most popular items instead, and a
 * recommendation scenario's against its simple lists.
 */
export function TrainResultView({ vhost, result, exampleRow }: {
  vhost: string
  result: TrainResult
  /** A row of the training data, to try the model with. */
  exampleRow?: Record<string, unknown>
}) {
  if (result.version.task !== RECOMMENDATION) return <PredictResultView vhost={vhost} result={result} exampleRow={exampleRow} />
  return isScenarioVersion(result.version)
    ? <ScenarioResultView vhost={vhost} result={result} />
    : <RecommendResultView vhost={vhost} result={result} />
}

function PredictResultView({ vhost, result, exampleRow }: {
  vhost: string
  result: TrainResult
  exampleRow?: Record<string, unknown>
}) {
  const v = result.version
  const name = result.model?.name ?? v.model
  const [trying, setTrying] = useState(false)
  const said = headline(v)
  const judged = verdict(v)
  const guess = baselineSentence(v)
  const row = exampleRow ? Object.fromEntries(v.features.filter((f) => f in exampleRow).map((f) => [f, exampleRow[f]])) : undefined
  const callBody = { instances: [row ?? Object.fromEntries(v.features.map((f) => [f, null]))] }
  const apiCall = servingCurl(vhost, name, callBody)
  const asyncCall = servingCurlAsync(vhost, name, callBody)

  return (
    <Stack gap="md">
      <Paper withBorder radius="md" p="md">
        <Stack gap="xs">
          <Group justify="space-between" align="flex-start" wrap="nowrap">
            <Title order={3} fz="lg">{said ?? `Version ${v.version} trained`}</Title>
            {judged && (
              <Badge size="lg" variant="light" color={VERDICT_TEXT[judged].color} tt="none">{VERDICT_TEXT[judged].label}</Badge>
            )}
          </Group>
          {judged && <Text size="sm">{VERDICT_TEXT[judged].meaning}</Text>}
          {guess && <Text size="sm" c="dimmed">{guess}</Text>}
          <Text size="sm" c="dimmed">
            Version {v.version}, trained on {v.rows.train.toLocaleString()} rows and checked on {v.rows.test.toLocaleString()} it had not seen.
          </Text>
        </Stack>
      </Paper>

      {!!v.warnings?.length && (
        <Alert color="yellow" variant="light" icon={<IconAlertCircle size="1rem" />} title="Notes about your data">
          <List size="sm" spacing={2}>{v.warnings.map((w) => <List.Item key={w}>{w}</List.Item>)}</List>
        </Alert>
      )}

      {!!v.importance?.length && (
        <Stack gap={6}>
          <Text fw={600} size="sm">What mattered most</Text>
          {v.importance.map((f) => {
            const pct = Math.round(f.weight * 100)
            return (
              <Group key={f.feature} gap="sm" wrap="nowrap">
                <Text size="sm" w={140} truncate="end" title={f.feature}>{f.feature}</Text>
                <Progress value={pct} style={{ flex: 1 }} aria-label={`${f.feature}: ${pct}%`} />
                <Text size="xs" c="dimmed" w={40} ta="right">{pct}%</Text>
              </Group>
            )
          })}
        </Stack>
      )}

      {!!v.leaderboard?.length && <Leaderboard entries={v.leaderboard} />}

      <NextSteps vhost={vhost} result={result} apiCall={apiCall} asyncCall={asyncCall} onTry={() => setTrying(true)}
        workflow={`To use it in a workflow, add a Predict node from Machine Learning and pick ${name}.`} />

      <Accordion variant="contained" keepMounted={false}>
        <Accordion.Item value="details">
          <Accordion.Control>Details</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="xs">
              {result.reason && <Text size="sm">{result.reason}.</Text>}
              <Group gap="xs">
                <Badge variant="light" tt="none">{v.task}</Badge>
                <Badge variant="light" tt="none">{v.algorithm}</Badge>
                {v.preset && <Badge variant="light" tt="none">{v.preset}</Badge>}
                {v.script && <Badge variant="light" color="grape" tt="none" title={v.script.sha256}>script {v.script.sha256.slice(0, 12)}</Badge>}
                <Badge variant="light" tt="none">{v.rows.train} rows trained, {v.rows.test} held back</Badge>
              </Group>
              <MetricsTable metrics={v.metrics} />
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

      {trying && (
        <ModelTestModal vhost={vhost} model={{ ...result.model, name, features: v.features }} initialRow={row}
          onClose={() => setTrying(false)} />
      )}
    </Stack>
  )
}

/** What "auto" tried, best first, and the one it kept. */
function Leaderboard({ entries }: { entries: LeaderboardEntry[] }) {
  const id = useId()
  return (
    <Stack gap={6}>
      <Text fw={600} size="sm" id={id}>What Auto tried</Text>
      <Table fz="sm" verticalSpacing={4} aria-labelledby={id}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Method</Table.Th>
            <Table.Th>Score</Table.Th>
            <Table.Th>Time</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {entries.map((e) => (
            <Table.Tr key={e.algorithm}>
              <Table.Td>{algorithmLabel(e.algorithm)}</Table.Td>
              <Table.Td title={`${METRIC_WORDS[e.metric] ?? e.metric} over ${e.folds} checks`} ff="var(--mantine-font-family-monospace)">
                {formatMetric(e.score)}
              </Table.Td>
              <Table.Td>{seconds(e.seconds)}</Table.Td>
              <Table.Td>{e.chosen && <Badge size="sm" color="green" variant="light" tt="none">Kept</Badge>}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}
