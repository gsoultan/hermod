import { useId, useState } from 'react'
import { Accordion, Badge, Group, Paper, Stack, Table, Text, Title } from '@mantine/core'
import { RECOMMENDATION, RECOMMEND_ALGORITHM_OPTIONS, describeRecommendation, type TrainResult } from '@/lib/mlModels'
import { MetricsTable, formatMetric } from './metrics'
import { ModelTestModal } from './ModelTestModal'
import { NextSteps, servingCurl, servingCurlAsync } from './nextSteps'
import { RECOMMEND_VERDICT_TEXT, VERDICT_TEXT, recommendVerdict } from './trainVerdict'

/** How many items the headline, the samples and the API call speak of. */
const TOP_K = 10
/** How many of a sample person's recommendations are shown. */
const SAMPLE_ITEMS = 5

const methodLabel = (algorithm: string) => RECOMMEND_ALGORITHM_OPTIONS.find((o) => o.value === algorithm)?.label ?? algorithm

/**
 * A recommender's outcome in plain words: for how many of 100 people the top
 * 10 held something they really picked next, good, fair or weak next to
 * recommending everyone the most popular items, a few real people of the
 * test, what Auto tried, and what to do next. The raw metrics sit behind
 * Details.
 */
export function RecommendResultView({ vhost, result }: { vhost: string; result: TrainResult }) {
  const v = result.version
  const name = result.model?.name ?? v.model
  const [trying, setTrying] = useState(false)
  const said = describeRecommendation(v.metrics ?? {})
  const judged = recommendVerdict(v)
  const person = v.samples?.[0]?.user ?? '<person id>'
  const tried = Object.entries(v.candidates ?? {}).sort(([, a], [, b]) => b - a)

  return (
    <Stack gap="md">
      <Paper withBorder radius="md" p="md">
        <Stack gap="xs">
          <Group justify="space-between" align="flex-start" wrap="nowrap">
            <Title order={3} fz="lg">{said.hits.replace(/\.$/, '')}</Title>
            {judged && (
              <Badge size="lg" variant="light" color={VERDICT_TEXT[judged].color} tt="none">{VERDICT_TEXT[judged].label}</Badge>
            )}
          </Group>
          {judged && <Text size="sm">{RECOMMEND_VERDICT_TEXT[judged]}</Text>}
          {said.baseline && <Text size="sm" c="dimmed">{said.baseline}</Text>}
          {said.coverage && <Text size="sm" c="dimmed">{said.coverage}</Text>}
          <Text size="sm" c="dimmed">
            {`Version ${v.version}`}
            {v.catalog ? `, which knows ${v.catalog.users.toLocaleString()} people and ${v.catalog.items.toLocaleString()} items` : ''}
            {`, trained on ${v.rows.train.toLocaleString()} rows and tested on ${v.rows.test.toLocaleString()} it had not seen.`}
          </Text>
        </Stack>
      </Paper>

      {!!v.samples?.length && <Samples samples={v.samples} />}

      {tried.length > 1 && <Tried entries={tried} kept={v.algorithm} />}

      <NextSteps vhost={vhost} result={result} apiCall={servingCurl(vhost, name, { user: person, k: TOP_K })}
        asyncCall={servingCurlAsync(vhost, name, { user: person, k: TOP_K })}
        onTry={() => setTrying(true)}
        workflow={`To use it in a workflow, add a Predict node from Machine Learning, pick ${name} and choose Recommend.`} />

      <Accordion variant="contained" keepMounted={false}>
        <Accordion.Item value="details">
          <Accordion.Control>Details</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="xs">
              {result.reason && <Text size="sm">{result.reason}.</Text>}
              <Group gap="xs">
                <Badge variant="light" tt="none">{v.task}</Badge>
                <Badge variant="light" tt="none">{v.algorithm}</Badge>
                <Badge variant="light" tt="none">{v.rows.train} rows trained, {v.rows.test} held back</Badge>
              </Group>
              <MetricsTable metrics={v.metrics} />
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

      {trying && (
        <ModelTestModal vhost={vhost} model={{ ...result.model, name, task: RECOMMENDATION }} onClose={() => setTrying(false)} />
      )}
    </Stack>
  )
}

/** A few people of the test: what they were recommended, and what they really picked next. */
function Samples({ samples }: { samples: NonNullable<TrainResult['version']['samples']> }) {
  const id = useId()
  return (
    <Stack gap={6}>
      <Text fw={600} size="sm" id={id}>Sample recommendations</Text>
      <Text size="xs" c="dimmed">
        A few people of the test: what Hermod recommended them, and what they really picked next. Bold is both.
      </Text>
      <Table fz="sm" verticalSpacing={4} aria-labelledby={id}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Person</Table.Th>
            <Table.Th>Recommended</Table.Th>
            <Table.Th>Really picked next</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {samples.map((s) => (
            <Table.Tr key={s.user}>
              <Table.Td>{s.user}</Table.Td>
              <Table.Td>
                {s.recommended.length ? s.recommended.slice(0, SAMPLE_ITEMS).map((item, i) => (
                  <Text key={item} span size="sm" fw={s.picked.includes(item) ? 700 : undefined}>
                    {i ? `, ${item}` : item}
                  </Text>
                )) : 'nothing'}
              </Table.Td>
              <Table.Td>{s.picked.join(', ')}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}

/** What Auto measured for each method (NDCG@10 on rows held back from training), best first. */
function Tried({ entries, kept }: { entries: Array<[string, number]>; kept: string }) {
  const id = useId()
  return (
    <Stack gap={6}>
      <Text fw={600} size="sm" id={id}>What Auto tried</Text>
      <Table fz="sm" verticalSpacing={4} aria-labelledby={id}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Method</Table.Th>
            <Table.Th>Score</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {entries.map(([algorithm, score]) => (
            <Table.Tr key={algorithm}>
              <Table.Td>{methodLabel(algorithm)}</Table.Td>
              <Table.Td title="NDCG@10: how high what they really picked sat in the top 10" ff="var(--mantine-font-family-monospace)">
                {formatMetric(score)}
              </Table.Td>
              <Table.Td>{algorithm === kept && <Badge size="sm" color="green" variant="light" tt="none">Kept</Badge>}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}
