import { useId, useState } from 'react'
import {
  Accordion, Alert, Badge, Button, CopyButton, Group, List, Paper, Progress, Stack, Table, Text, Title,
} from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy, IconPlayerPlay, IconRocket } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  algorithmLabel, mlModelsKey, promoteVersion, servingUrl, versionsKey, type LeaderboardEntry, type TrainResult,
} from '@/lib/mlModels'
import { MetricsTable, formatMetric } from './metrics'
import { ModelTestModal } from './ModelTestModal'
import { VERDICT_TEXT, baselineSentence, headline, verdict } from './trainVerdict'

const METRIC_WORDS: Record<string, string> = { balanced_accuracy: 'balanced accuracy', r2: 'R²' }

const seconds = (s: number) => (s < 1 ? 'under a second' : s < 90 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`)

/**
 * A training's outcome in plain words: how often it is right (or how far off),
 * whether that is good next to guessing, what mattered, what "auto" tried, and
 * what to do next. The raw metrics sit behind Details. Everything a server
 * older than these fields leaves out is simply not shown.
 */
export function TrainResultView({ vhost, result, exampleRow }: {
  vhost: string
  result: TrainResult
  /** A row of the training data, to try the model with. */
  exampleRow?: Record<string, unknown>
}) {
  const queryClient = useQueryClient()
  const v = result.version
  const name = result.model?.name ?? v.model
  const [trying, setTrying] = useState(false)
  const promote = useMutation({
    mutationFn: () => promoteVersion(vhost, name, v.version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      queryClient.invalidateQueries({ queryKey: versionsKey(vhost, name) })
    },
  })
  const live = result.live || promote.isSuccess
  const said = headline(v)
  const judged = verdict(v)
  const guess = baselineSentence(v)
  const row = exampleRow ? Object.fromEntries(v.features.filter((f) => f in exampleRow).map((f) => [f, exampleRow[f]])) : undefined
  const apiCall = [
    `curl -X POST '${window.location.origin}${servingUrl(vhost, name)}' \\`,
    `  -H 'X-API-Key: <serving key>' -H 'Content-Type: application/json' \\`,
    `  -d '${JSON.stringify({ instances: [row ?? Object.fromEntries(v.features.map((f) => [f, null]))] })}'`,
  ].join('\n')

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

      <Stack gap="xs">
        <Text fw={600} size="sm">Next steps</Text>
        <Group gap="xs">
          <Button variant="light" leftSection={<IconPlayerPlay size="1rem" />} onClick={() => setTrying(true)}>Try it</Button>
          {live ? (
            <Badge color="green" variant="light" size="lg" tt="none">Live</Badge>
          ) : (
            <Button variant="light" color="green" leftSection={<IconRocket size="1rem" />} loading={promote.isPending}
              onClick={() => promote.mutate()}>
              Make it live
            </Button>
          )}
          <CopyButton value={apiCall}>
            {({ copied, copy }) => (
              <Button variant="default" leftSection={copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />} onClick={copy}>
                {copied ? 'Copied' : 'Copy API call'}
              </Button>
            )}
          </CopyButton>
        </Group>
        <Text size="xs" c="dimmed">
          {live ? 'It is live. ' : `It is not live yet${result.reason ? ` (${result.reason})` : ''}. `}
          To use it in a workflow, add a Predict node from Machine Learning and pick {name}. Applications call it with a
          serving key, made from the model&apos;s key button.
        </Text>
        {promote.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The version was not put live">
            {(promote.error as Error).message}
          </Alert>
        )}
      </Stack>

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
