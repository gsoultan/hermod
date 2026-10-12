import { useState } from 'react'
import { Alert, Button, Checkbox, Code, Group, Loader, Modal, Radio, Stack, Text, TextInput, Textarea } from '@mantine/core'
import { IconAlertCircle, IconPlayerPlay } from '@tabler/icons-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  isRecommender, listVersions, predict, recommend, versionsKey, type MLModel, type RecommendCall,
} from '@/lib/mlModels'
import {
  isMissingRoute, isScenarioVersion, nextPeriod, recommendScenario, type RecommendRequest, type RecommendResponse, type Scenario,
} from '@/lib/mlRecommend'
import { FormRow } from '@/components/common/FormRow'
import { RecommendAnswer, RecommendedList } from './scenarioItems'

/** A starting row: the model's declared features, each set to 0. */
function sampleRow(model: MLModel): string {
  const row = Object.fromEntries((model.features ?? []).map((f) => [f, 0]))
  return JSON.stringify(row, null, 2)
}

/** How many items the test asks a recommender for. */
const TEST_K = 10

/**
 * Sends one row to the model, or asks a recommender, and shows what it
 * answered. `initialRow`, such as a row of the dataset it was trained on,
 * replaces the row of zeros to start from; a recommender ignores it. A
 * recommender trained for a scenario is asked in that scenario's terms;
 * `scenario` says which, else its live version does.
 */
export function ModelTestModal({
  vhost, model, initialRow, scenario, onClose,
}: {
  vhost: string
  model: MLModel
  initialRow?: Record<string, unknown>
  scenario?: Scenario
  onClose: () => void
}) {
  return (
    <Modal opened onClose={onClose} title={`Test ${model.name}`} centered size="lg">
      {isRecommender(model)
        ? <RecommenderSwitch vhost={vhost} model={model} scenario={scenario} />
        : <RowTest vhost={vhost} model={model} initialRow={initialRow} />}
    </Modal>
  )
}

/** A scenario recommender gets the scenario's questions; one trained from one table, a person or an item. */
function RecommenderSwitch({ vhost, model, scenario }: { vhost: string; model: MLModel; scenario?: Scenario }) {
  const { data: versions, isLoading } = useQuery({
    queryKey: versionsKey(vhost, model.name),
    queryFn: ({ signal }) => listVersions(vhost, model.name, signal),
    enabled: !scenario,
    retry: false,
  })
  if (!scenario && isLoading) return <Group justify="center" p="md"><Loader size="sm" /></Group>
  const live = versions?.find((v) => v.version === model.remote_version) ?? versions?.[0]
  const s = scenario ?? (isScenarioVersion(live) ? live.scenario : undefined)
  return s ? <ScenarioTest vhost={vhost} model={model} scenario={s} /> : <RecommenderTest vhost={vhost} model={model} />
}

function RowTest({ vhost, model, initialRow }: { vhost: string; model: MLModel; initialRow?: Record<string, unknown> }) {
  const [row, setRow] = useState(() => (initialRow ? JSON.stringify(initialRow, null, 2) : sampleRow(model)))
  const [parseError, setParseError] = useState<string | null>(null)
  const run = useMutation({ mutationFn: (instance: Record<string, unknown>) => predict(vhost, model.name, [instance]) })

  const submit = () => {
    let parsed: unknown
    try {
      parsed = JSON.parse(row)
    } catch {
      setParseError('The row must be a JSON object.')
      return
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setParseError('The row must be a JSON object.')
      return
    }
    setParseError(null)
    run.mutate(parsed as Record<string, unknown>)
  }

  return (
    <Stack gap="md">
      <Textarea label="Row" description="One record as JSON, with the fields the model takes." autosize minRows={4} maxRows={14}
        value={row} onChange={(e) => setRow(e.currentTarget.value)} error={parseError}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
      <Group justify="flex-end">
        <Button leftSection={<IconPlayerPlay size="1rem" />} onClick={submit} loading={run.isPending}>Run</Button>
      </Group>
      {run.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
          {(run.error as Error).message}
        </Alert>
      )}
      {run.data && (
        <Stack gap={4}>
          <Text size="sm" fw={500}>Prediction</Text>
          <Code block>{JSON.stringify(run.data[0], null, 2)}</Code>
        </Stack>
      )}
    </Stack>
  )
}

/** A person's top 10, or the 10 items most like one item. */
function RecommenderTest({ vhost, model }: { vhost: string; model: MLModel }) {
  const [mode, setMode] = useState<'user' | 'item'>('user')
  const [id, setId] = useState('')
  const [excludeSeen, setExcludeSeen] = useState(true)
  const run = useMutation({ mutationFn: (call: RecommendCall) => recommend(vhost, model.name, call) })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!id.trim()) return
    run.mutate(mode === 'user'
      ? { user: id.trim(), k: TEST_K, exclude_seen: excludeSeen }
      : { item: id.trim(), k: TEST_K })
  }

  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        <Radio.Group value={mode} onChange={(v) => { setMode(v as typeof mode); setId(''); run.reset() }}>
          <Group gap="lg">
            <Radio value="user" label="Top 10 for a person" />
            <Radio value="item" label="Similar items" />
          </Group>
        </Radio.Group>
        {mode === 'user' ? (
          <>
            <TextInput label="Person" description="Their id, as in the training data." placeholder="customer id" required
              value={id} onChange={(e) => setId(e.currentTarget.value)} />
            <Checkbox label="Leave out what they already had" checked={excludeSeen}
              onChange={(e) => setExcludeSeen(e.currentTarget.checked)} />
          </>
        ) : (
          <TextInput label="Item" description="Its id, as in the training data." placeholder="product id" required
            value={id} onChange={(e) => setId(e.currentTarget.value)} />
        )}
        <Group justify="flex-end">
          <Button type="submit" leftSection={<IconPlayerPlay size="1rem" />} loading={run.isPending} disabled={!id.trim()}>
            {mode === 'user' ? 'Recommend' : 'Find similar'}
          </Button>
        </Group>
        {run.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
            {(run.error as Error).message}
          </Alert>
        )}
        {run.data && <RecommendedList items={run.data} />}
      </Stack>
    </form>
  )
}

/** The answer of a scenario test: from the recommend route, or the predict route of an older server. */
type ScenarioAnswer = { kind: 'scenario'; answer: RecommendResponse } | { kind: 'legacy'; items: Awaited<ReturnType<typeof recommend>> }

/**
 * Asks a scenario recommender as an application would: a salesman's plan for
 * some days, a customer's products or reorder date, or a product's customers
 * or look-alikes. Each answer carries its reasons and can be marked useful or
 * not, which is posted as feedback. A server without the recommend route is
 * asked through predict, the way a one-table recommender is.
 */
function ScenarioTest({ vhost, model, scenario }: { vhost: string; model: MLModel; scenario: Scenario }) {
  const forKey = scenario === 'visits' ? 'salesman' : scenario === 'products' || scenario === 'reorder' ? 'customer' : 'product'
  const [id, setId] = useState('')
  const [period, setPeriod] = useState(() => nextPeriod(7))
  const [excludeSeen, setExcludeSeen] = useState(true)
  const run = useMutation({
    mutationFn: async (body: RecommendRequest): Promise<ScenarioAnswer> => {
      try {
        return { kind: 'scenario', answer: await recommendScenario(vhost, model.name, body) }
      } catch (error) {
        if (!isMissingRoute(error) || forKey === 'salesman') throw error
        const call: RecommendCall = forKey === 'customer'
          ? { user: id.trim(), k: TEST_K, ...(scenario === 'products' ? { exclude_seen: excludeSeen } : {}) }
          : { item: id.trim(), k: TEST_K }
        return { kind: 'legacy', items: await recommend(vhost, model.name, call) }
      }
    },
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!id.trim()) return
    run.mutate({
      for: { [forKey]: id.trim() } as RecommendRequest['for'],
      k: TEST_K,
      ...(scenario === 'visits' ? { period } : {}),
      ...(scenario === 'products' ? { exclude_seen: excludeSeen } : {}),
    })
  }
  const label = { salesman: 'Salesman', customer: 'Customer', product: 'Product' }[forKey]
  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        <TextInput label={label} description="Their id, as in your data." placeholder={`${forKey} id`} required
          value={id} onChange={(e) => setId(e.currentTarget.value)} />
        {scenario === 'visits' && (
          <FormRow cols={2}>
            <TextInput type="date" label="From" value={period.from} onChange={(e) => setPeriod({ ...period, from: e.currentTarget.value })} />
            <TextInput type="date" label="To" value={period.to} onChange={(e) => setPeriod({ ...period, to: e.currentTarget.value })} />
          </FormRow>
        )}
        {scenario === 'products' && (
          <Checkbox label="Leave out what they already bought" checked={excludeSeen} onChange={(e) => setExcludeSeen(e.currentTarget.checked)} />
        )}
        <Group justify="flex-end">
          <Button type="submit" leftSection={<IconPlayerPlay size="1rem" />} loading={run.isPending} disabled={!id.trim()}>Recommend</Button>
        </Group>
        {run.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
            {isMissingRoute(run.error) ? 'This Hermod cannot plan visits yet: it needs a newer server.' : (run.error as Error).message}
          </Alert>
        )}
        {run.data?.kind === 'scenario' && <RecommendAnswer answer={run.data.answer} feedback={{ vhost, model: model.name }} />}
        {run.data?.kind === 'legacy' && <RecommendedList items={run.data.items} />}
      </Stack>
    </form>
  )
}
