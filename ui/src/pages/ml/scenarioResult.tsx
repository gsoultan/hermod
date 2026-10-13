import { useId, useState } from 'react'
import {
  Accordion, Alert, Badge, Button, Group, Modal, NumberInput, Paper, Radio, Select, Stack, Table, Text, TextInput, Title,
} from '@mantine/core'
import { IconAlertCircle, IconCalendar, IconTableExport } from '@tabler/icons-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { FormRow } from '@/components/common/FormRow'
import { RECOMMENDATION, listSQLSources, type TrainResult } from '@/lib/mlModels'
import {
  BASELINE_WORDS, MAX_WITHIN_DAYS, buildForScenario, exampleRequest, isMissingRoute, nextPeriod, putBuild, recommendScenario,
  recommendServingUrl, scenarioLabel, type BaselineName, type BuildFor, type RecommendRequest, type Scenario,
  type ScenarioItem, type ScenarioVersion,
} from '@/lib/mlRecommend'
import { MetricsTable } from './metrics'
import { ModelTestModal } from './ModelTestModal'
import { NextSteps } from './nextSteps'
import { statsSentence } from './recommendSteps'
import { RecommendAnswer, ScenarioItemList } from './scenarioItems'
import { VERDICT_TEXT, type Verdict } from './trainVerdict'

const isNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const pct = (v: number) => `${Math.round(v * 100)}%`
const per100 = (v: number) => Math.round(v * 100)

/** Good, fair or weak: how much better than the simple list, either way round. */
function judge(score: number | undefined, base: number | undefined, lowerIsBetter = false): Verdict | null {
  if (!isNumber(score) || !isNumber(base)) return null
  const [better, worse] = lowerIsBetter ? [base, score] : [score, base]
  if (better <= worse) return 'weak'
  if (worse === 0) return 'good'
  const ratio = better / worse
  return ratio >= 1.5 ? 'good' : ratio >= 1.1 ? 'fair' : 'weak'
}

const named = (v: ScenarioVersion, name: BaselineName, key: string) =>
  v.baselines_named?.find((b) => b.name === name)?.metrics?.[key] ?? v.baseline?.[key]

/** What a verdict means next to the simple list. */
const MEANING: Record<Verdict, string> = {
  good: 'Much better than the simple list.',
  fair: 'Better than the simple list, with room to improve.',
  weak: 'Barely better than the simple list. More history, or the customers and visits tables, usually help.',
}

/**
 * A scenario version's result in one sentence, tested on the period after the
 * training data: for visits, how many planned customers ordered against the
 * "most overdue first" list; for products, how often the top 10 held what was
 * bought next against the most popular products; for reorder, how far the
 * date was off. `more` holds the sentences after it.
 */
export function scenarioHeadline(v: ScenarioVersion): { headline: string; more: string[]; verdict: Verdict | null } {
  const m = v.metrics ?? {}
  switch (v.scenario) {
    case 'visits': {
      const key = 'ordered_in_period@k'
      const score = m[key]
      const base = named(v, 'most_overdue', key)
      const more = isNumber(m['revenue_share@k']) ? [`They brought ${pct(m['revenue_share@k'])} of the period's revenue.`] : []
      if (!isNumber(score)) return { headline: 'The visit plan was trained', more, verdict: null }
      const said = `Of the customers Hermod would have planned last period, ${pct(score)} ordered`
      return {
        headline: isNumber(base) ? `${said} — ${BASELINE_WORDS.most_overdue} got ${pct(base)}` : said,
        more,
        verdict: judge(score, base),
      }
    }
    case 'reorder': {
      const off = m.mae_days
      const base = v.baseline?.mae_days
      const more = isNumber(m.on_time_share) ? [`${pct(m.on_time_share)} of the next orders came on time.`] : []
      if (!isNumber(off)) return { headline: 'The reorder model was trained', more, verdict: null }
      const said = `The next order came on average ${Math.round(off)} days from the date Hermod expected`
      return {
        headline: isNumber(base) ? `${said} — the simple guess was off by ${Math.round(base)} days` : said,
        more,
        verdict: judge(off, base, true),
      }
    }
    default: {
      const hits = m['hit_rate@10']
      const base = named(v, 'popular', 'hit_rate@10')
      const more = isNumber(m.coverage) ? [`Its top-10 lists used ${pct(m.coverage)} of the products.`] : []
      if (!isNumber(hits)) return { headline: 'The model was trained', more, verdict: null }
      const said = v.scenario === 'audience'
        ? `For ${per100(hits)} of 100 products, the top 10 customers held one who really bought it next`
        : v.scenario === 'similar'
          ? `For ${per100(hits)} of 100 tests, one of the 10 most similar products was really bought next`
          : `For ${per100(hits)} of 100 customers, one of the top 10 was something they really bought next`
      return {
        headline: isNumber(base) ? `${said} — ${BASELINE_WORDS.popular} got ${per100(base)}` : said,
        more,
        verdict: judge(hits, base),
      }
    }
  }
}

const BASELINE_TITLE: Record<BaselineName, string> = {
  most_overdue: 'Most overdue first', biggest: 'Biggest customers first', popular: 'Most popular products',
}

/** The curl an application runs to ask scenario model `name`. */
export function recommendCurl(vhost: string, name: string, body: RecommendRequest): string {
  return [
    `curl -X POST '${window.location.origin}${recommendServingUrl(vhost, name)}' \\`,
    `  -H 'X-API-Key: <serving key>' -H 'Content-Type: application/json' \\`,
    `  -d '${JSON.stringify(body)}'`,
  ].join('\n')
}

/**
 * A scenario recommender's outcome in plain words: the headline against the
 * simple lists, good, fair or weak, what the data looked like, a few real
 * answers, for visits a salesman's plan to preview, and how to use it: try it,
 * copy the sync or async call, or build the recommendations into a table on a
 * schedule. The raw metrics sit behind Details.
 */
export function ScenarioResultView({ vhost, result }: { vhost: string; result: TrainResult }) {
  const v = result.version as unknown as ScenarioVersion & { scenario: Scenario }
  const name = result.model?.name ?? v.model
  const [trying, setTrying] = useState(false)
  const [building, setBuilding] = useState(false)
  const said = scenarioHeadline(v)
  const baselinesId = useId()
  const notGreen = v.health?.checks.filter((c) => c.status !== 'green') ?? []
  const body = exampleRequest(v.scenario)
  const asyncBody = { ...body, respond: 'async' as const, callback_url: 'https://your-app.example/hermod-callback' }

  return (
    <Stack gap="md">
      <Paper withBorder radius="md" p="md">
        <Stack gap="xs">
          <Group justify="space-between" align="flex-start" wrap="nowrap">
            <Title order={3} fz="lg">{said.headline}</Title>
            {said.verdict && (
              <Badge size="lg" variant="light" color={VERDICT_TEXT[said.verdict].color} tt="none">{VERDICT_TEXT[said.verdict].label}</Badge>
            )}
          </Group>
          {said.verdict && <Text size="sm">{MEANING[said.verdict]}</Text>}
          {said.more.map((t) => <Text key={t} size="sm" c="dimmed">{t}</Text>)}
          <Text size="sm" c="dimmed">
            {scenarioLabel(v.scenario)}, version {v.version}. Hermod learned from the data before a cut-off date and was
            tested on what really happened after it.
          </Text>
        </Stack>
      </Paper>

      {!!v.baselines_named?.length && (
        <Stack gap={6}>
          <Text fw={600} size="sm" id={baselinesId}>The simple lists it was compared with</Text>
          <Table fz="sm" verticalSpacing={4} aria-labelledby={baselinesId}>
            <Table.Tbody>
              {v.baselines_named.map((b) => {
                const [key, value] = Object.entries(b.metrics ?? {})[0] ?? []
                return (
                  <Table.Tr key={b.name}>
                    <Table.Td>{BASELINE_TITLE[b.name] ?? b.name}</Table.Td>
                    <Table.Td>{isNumber(value) ? (key === 'mae_days' ? `${Math.round(value)} days off` : pct(value)) : '—'}</Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Stack>
      )}

      {v.health && (
        <Stack gap={4}>
          <Text fw={600} size="sm">Your data</Text>
          <Text size="sm" c="dimmed">{statsSentence(v.health.stats)}</Text>
          {notGreen.map((c) => (
            <Text key={c.code} size="sm" c={c.status === 'red' ? 'red' : 'yellow.8'}>{c.message}{c.fix ? ` ${c.fix}` : ''}</Text>
          ))}
        </Stack>
      )}

      {!!v.samples?.length && (
        <Stack gap={6}>
          <Text fw={600} size="sm">Sample recommendations</Text>
          <ScenarioItemList items={v.samples as ScenarioItem[]} label="Sample recommendations" />
        </Stack>
      )}

      {v.scenario === 'visits' && <PlanPreview vhost={vhost} name={name} />}

      <NextSteps vhost={vhost} result={result} apiCall={recommendCurl(vhost, name, body)}
        asyncCall={recommendCurl(vhost, name, asyncBody)} onTry={() => setTrying(true)}
        workflow={`To use it in a workflow, add a Predict node from Machine Learning and pick ${name}, or a Build Recommendations node to write every list at once.`} />
      <Stack gap={4}>
        <Group gap="xs">
          <Button variant="light" size="xs" leftSection={<IconTableExport size="1rem" />} onClick={() => setBuilding(true)}>
            Build recommendations into a table
          </Button>
        </Group>
        <Text size="xs" c="dimmed">
          The async call answers at once with a job to read later, and posts the result to your callback; it requires
          Hermod with async jobs. A build writes every list to a table on a schedule, for a CRM or a mobile app to read.
        </Text>
      </Stack>

      <Accordion variant="contained" keepMounted={false}>
        <Accordion.Item value="details">
          <Accordion.Control>Details</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="xs">
              {result.reason && <Text size="sm">{result.reason}.</Text>}
              <Group gap="xs">
                <Badge variant="light" tt="none">{v.task}</Badge>
                <Badge variant="light" tt="none">{v.scenario}</Badge>
                <Badge variant="light" tt="none">{v.algorithm}</Badge>
              </Group>
              <MetricsTable metrics={v.metrics} />
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

      {trying && (
        <ModelTestModal vhost={vhost} model={{ ...result.model, name, task: RECOMMENDATION }} scenario={v.scenario}
          onClose={() => setTrying(false)} />
      )}
      {building && <BuildModal vhost={vhost} name={name} scenario={v.scenario} onClose={() => setBuilding(false)} />}
    </Stack>
  )
}

/** One real salesman's plan for a period, as a calendar list with reasons. */
function PlanPreview({ vhost, name }: { vhost: string; name: string }) {
  const [salesman, setSalesman] = useState('')
  const [period, setPeriod] = useState(() => nextPeriod(7))
  const ask = useMutation({
    mutationFn: () => recommendScenario(vhost, name, { for: { salesman: salesman.trim() }, period }),
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (salesman.trim()) ask.mutate()
  }
  return (
    <Paper withBorder radius="md" p="md">
      <form onSubmit={submit} noValidate>
        <Stack gap="sm">
          <Text fw={600} size="sm">See one salesman's plan</Text>
          <FormRow cols={3}>
            <TextInput label="Salesman" description="Their id, as in your data." required value={salesman}
              onChange={(e) => setSalesman(e.currentTarget.value)} />
            <TextInput type="date" label="From" value={period.from} onChange={(e) => setPeriod({ ...period, from: e.currentTarget.value })} />
            <TextInput type="date" label="To" value={period.to} onChange={(e) => setPeriod({ ...period, to: e.currentTarget.value })} />
          </FormRow>
          <Group>
            <Button type="submit" variant="light" leftSection={<IconCalendar size="1rem" />} loading={ask.isPending} disabled={!salesman.trim()}>
              Show the plan
            </Button>
          </Group>
          {ask.error && (
            <Alert color="red" variant="light" icon={<IconAlertCircle size="1rem" />} title="No plan">
              {isMissingRoute(ask.error)
                ? 'This Hermod cannot show plans yet. The model is trained; a newer server serves it.'
                : (ask.error as Error).message}
            </Alert>
          )}
          {ask.data && <RecommendAnswer answer={ask.data} />}
        </Stack>
      </form>
    </Paper>
  )
}

const BUILD_FOR_OPTIONS: Array<{ value: BuildFor; label: string }> = [
  { value: 'all_salesmen', label: 'Every salesman (their visit plan)' },
  { value: 'all_customers', label: 'Every customer' },
  { value: 'all_products', label: 'Every product' },
]

/** "Build recommendations weekly into a table": the model's build schedule (PUT .../build). */
function BuildModal({ vhost, name, scenario, onClose }: { vhost: string; name: string; scenario: Scenario; onClose: () => void }) {
  const visits = scenario === 'visits'
  const reorder = scenario === 'reorder'
  const [schedule, setSchedule] = useState('0 6 * * 1')
  const [withinDays, setWithinDays] = useState<number | string>('')
  const [buildFor, setBuildFor] = useState<BuildFor>(buildForScenario(scenario))
  const [periodDays, setPeriodDays] = useState<number | string>(7)
  const [k, setK] = useState<number | string>(20)
  const [source, setSource] = useState<string | null>(null)
  const [table, setTable] = useState(visits ? 'visit_plan' : 'recommendations')
  const [mode, setMode] = useState<'replace' | 'append'>('replace')
  const { data: sources = [], isLoading } = useQuery({
    queryKey: ['ml-sql-sources', vhost],
    queryFn: ({ signal }) => listSQLSources(vhost, signal),
    retry: false,
  })
  const save = useMutation({
    mutationFn: () => putBuild(vhost, name, {
      schedule: schedule.trim(),
      for: buildFor,
      ...(visits ? { period_days: typeof periodDays === 'number' ? periodDays : 7 } : {}),
      ...(reorder && typeof withinDays === 'number' && withinDays > 0 ? { within_days: withinDays } : {}),
      k: typeof k === 'number' ? k : 20,
      target: { source_id: source as string, table: table.trim(), mode },
    }),
  })
  const ready = !!schedule.trim() && !!source && !!table.trim()
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ready) save.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Build recommendations into a table" centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            On every run Hermod works out the lists for everyone and writes one row per recommendation: model, version,
            recommendation_id, for_id, item_id, rank, score, plan_date, reasons_text and built_at.
            {reorder && ' Add a due_date column to the table to get the date each customer is expected to order.'}
          </Text>
          <FormRow cols={2}>
            <TextInput label="Schedule" required description='Cron: "0 6 * * 1" is every Monday at 06:00.' value={schedule}
              onChange={(e) => setSchedule(e.currentTarget.value)} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
            <Select label="For" data={BUILD_FOR_OPTIONS} value={buildFor} allowDeselect={false}
              onChange={(v) => setBuildFor((v ?? buildForScenario(scenario)) as BuildFor)} />
          </FormRow>
          <FormRow cols={2}>
            {visits && <NumberInput label="Days to plan" min={1} max={92} value={periodDays} onChange={setPeriodDays} />}
            {reorder && (
              <NumberInput label="Only customers due within (days)" description="14 for 2 weeks, 30 for a month; empty for everyone."
                min={1} max={MAX_WITHIN_DAYS} allowDecimal={false} value={withinDays} onChange={setWithinDays} />
            )}
            <NumberInput label="How many per list" min={1} max={visits ? 500 : 100} value={k} onChange={setK} />
          </FormRow>
          <FormRow cols={2}>
            <Select label="Database" required searchable
              placeholder={isLoading ? 'Loading…' : sources.length ? 'Choose a database source' : 'No database sources in this vhost'}
              data={sources.map((s) => ({ value: s.id, label: s.name }))} value={source} onChange={setSource} />
            <TextInput label="Table" required value={table} onChange={(e) => setTable(e.currentTarget.value)} />
          </FormRow>
          <Radio.Group label="Each run" value={mode} onChange={(v) => setMode(v as typeof mode)}>
            <Group gap="lg" mt={4}>
              <Radio value="replace" label="Replaces the table's rows" />
              <Radio value="append" label="Adds to them" />
            </Group>
          </Radio.Group>
          {save.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The build was not saved">
              {isMissingRoute(save.error) ? 'This Hermod cannot build recommendations yet: it needs a newer server.' : (save.error as Error).message}
            </Alert>
          )}
          {save.isSuccess && <Alert color="green" title="Saved">{name} builds into {table.trim()} on {schedule.trim()}.</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{save.isSuccess ? 'Close' : 'Cancel'}</Button>
            <Button type="submit" loading={save.isPending} disabled={!ready}>Save</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
