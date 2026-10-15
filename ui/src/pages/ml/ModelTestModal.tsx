import { useState } from 'react'
import { Alert, Autocomplete, Button, Checkbox, Code, Group, Loader, Modal, NumberInput, Radio, Stack, Text, TextInput, Textarea } from '@mantine/core'
import { IconAlertCircle, IconPlayerPlay } from '@tabler/icons-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  isRecommender, listVersions, predict, recommend, versionsKey, type MLModel, type RecommendCall,
} from '@/lib/mlModels'
import {
  DUE_WINDOWS, MAX_WITHIN_DAYS, isMissingRoute, isScenarioVersion, nextPeriod, recommendScenario, type RecommendRequest,
  type RecommendResponse, type Scenario,
} from '@/lib/mlRecommend'
import { FormRow } from '@/components/common/FormRow'
import { DueCustomerList, RecommendAnswer, RecommendedList } from './scenarioItems'

/** A customer category, size, price tier, channel and region a list of customers is kept to; empty for any. */
interface CustomerGroup {
  segment: string
  size: string
  price_tier: string
  channel: string
  region: string
}

const ANY_GROUP: CustomerGroup = { segment: '', size: '', price_tier: '', channel: '', region: '' }

/** The "for" keys that keep a list of customers to one of each. */
const groupFor = (g: CustomerGroup) =>
  Object.fromEntries((Object.keys(ANY_GROUP) as Array<keyof CustomerGroup>).filter((k) => g[k].trim()).map((k) => [k, g[k].trim()]))

/** Customer category, size, price tier, channel and region, to keep a list of customers to one of each. */
function CustomerGroupFields({ value, onChange }: { value: CustomerGroup; onChange: (g: CustomerGroup) => void }) {
  return (
    <>
      <FormRow cols={2}>
        <TextInput label="Category" description="Optional: only customers of this customer category." placeholder="any"
          value={value.segment} onChange={(e) => onChange({ ...value, segment: e.currentTarget.value })} />
        <Autocomplete label="Size" description="Optional: only customers of this size (small, medium, large or your own label)." placeholder="any"
          data={['small', 'medium', 'large']} value={value.size} onChange={(size) => onChange({ ...value, size })} />
      </FormRow>
      <FormRow cols={3}>
        <TextInput label="Price tier" description="Optional: only customers on this price tier." placeholder="any"
          value={value.price_tier} onChange={(e) => onChange({ ...value, price_tier: e.currentTarget.value })} />
        <TextInput label="Channel" description="Optional: only customers of this channel." placeholder="any"
          value={value.channel} onChange={(e) => onChange({ ...value, channel: e.currentTarget.value })} />
        <TextInput label="Region" description="Optional: only customers in this region." placeholder="any"
          value={value.region} onChange={(e) => onChange({ ...value, region: e.currentTarget.value })} />
      </FormRow>
    </>
  )
}

/** A brand and subcategory a list of products is kept to; empty for any. */
interface ProductKind {
  brand: string
  subcategory: string
}

const ANY_KIND: ProductKind = { brand: '', subcategory: '' }

/** The "for" keys that keep a list of products to one brand and subcategory. */
const kindFor = (k: ProductKind) => ({
  ...(k.brand.trim() ? { brand: k.brand.trim() } : {}),
  ...(k.subcategory.trim() ? { subcategory: k.subcategory.trim() } : {}),
})

/** Brand and subcategory, to keep a list of products to one of each. */
function ProductKindFields({ value, onChange }: { value: ProductKind; onChange: (k: ProductKind) => void }) {
  return (
    <FormRow cols={2}>
      <TextInput label="Brand" description="Optional: only products of this brand." placeholder="any"
        value={value.brand} onChange={(e) => onChange({ ...value, brand: e.currentTarget.value })} />
      <TextInput label="Subcategory" description="Optional: only products of this subcategory." placeholder="any"
        value={value.subcategory} onChange={(e) => onChange({ ...value, subcategory: e.currentTarget.value })} />
    </FormRow>
  )
}

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
  if (scenario === 'reorder') return <ReorderTest vhost={vhost} model={model} />
  return <ScenarioIdTest vhost={vhost} model={model} scenario={scenario} />
}

/** How many customers a reorder question lists. */
const DUE_K = 100

type DueWho = 'everyone' | 'salesman' | 'product' | 'customer'

const DUE_WHO: Array<{ value: DueWho; label: string }> = [
  { value: 'everyone', label: 'Everyone' },
  { value: 'salesman', label: "A salesman's customers" },
  { value: 'product', label: "A product's buyers" },
  { value: 'customer', label: 'One customer' },
]

/**
 * "Which customers will buy in the next 2 weeks, or month?": a reorder model
 * asked for everyone, a salesman's customers, a product's buyers or one
 * customer, in a period from today. The answer lists the customers with the
 * date each is expected to order (overdue ones marked), the products due and
 * why. "Any time" asks as before, with no period.
 */
function ReorderTest({ vhost, model }: { vhost: string; model: MLModel }) {
  const [who, setWho] = useState<DueWho>('everyone')
  const [id, setId] = useState('')
  const [span, setSpan] = useState<string>(String(DUE_WINDOWS[0].days))
  const [customDays, setCustomDays] = useState<number | string>(7)
  const [group, setGroup] = useState<CustomerGroup>(ANY_GROUP)
  const run = useMutation({ mutationFn: (body: RecommendRequest) => recommendScenario(vhost, model.name, body) })
  const days = span === 'any' ? 0 : span === 'custom' ? (typeof customDays === 'number' ? customDays : 0) : Number(span)
  const ready = (who === 'everyone' || !!id.trim()) && (span !== 'custom' || (days >= 1 && days <= MAX_WITHIN_DAYS))
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!ready) return
    const subject = { ...(who === 'everyone' ? { all_customers: true } : { [who]: id.trim() }), ...groupFor(group) } as RecommendRequest['for']
    run.mutate({ for: subject, ...(days > 0 ? { within_days: days } : {}), k: DUE_K })
  }
  const label = { everyone: '', salesman: 'Salesman', product: 'Product', customer: 'Customer' }[who]
  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        <Radio.Group label="Who" value={who} onChange={(v) => { setWho(v as DueWho); setId(''); run.reset() }}>
          <Group gap="lg" mt={4}>
            {DUE_WHO.map((o) => <Radio key={o.value} value={o.value} label={o.label} />)}
          </Group>
        </Radio.Group>
        {who !== 'everyone' && (
          <TextInput label={label} description="Their id, as in your data." placeholder={`${who} id`} required
            value={id} onChange={(e) => setId(e.currentTarget.value)} />
        )}
        <CustomerGroupFields value={group} onChange={setGroup} />
        <Radio.Group label="Likely to buy within" value={span} onChange={setSpan}
          description="From today. Customers whose date has already passed are listed too, marked overdue.">
          <Group gap="lg" mt={4}>
            {DUE_WINDOWS.map((w) => <Radio key={w.days} value={String(w.days)} label={w.label} />)}
            <Radio value="custom" label="Custom" />
            <Radio value="any" label="Any time" />
          </Group>
        </Radio.Group>
        {span === 'custom' && (
          <NumberInput label="Days" description={`1 to ${MAX_WITHIN_DAYS} days from today.`} min={1} max={MAX_WITHIN_DAYS}
            allowDecimal={false} value={customDays} onChange={setCustomDays} w={160} />
        )}
        <Group justify="flex-end">
          <Button type="submit" leftSection={<IconPlayerPlay size="1rem" />} loading={run.isPending} disabled={!ready}>Find customers</Button>
        </Group>
        {run.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
            {isMissingRoute(run.error) ? 'This Hermod cannot answer this question yet: it needs a newer server.' : (run.error as Error).message}
          </Alert>
        )}
        {run.data && (
          <DueCustomerList items={run.data.results?.[0]?.items ?? []}
            feedback={{ vhost, model: model.name, recommendationId: run.data.recommendation_id }} />
        )}
      </Stack>
    </form>
  )
}

/** A scenario asked for one id: a salesman's plan, a customer's products, or a product's customers or look-alikes. */
function ScenarioIdTest({ vhost, model, scenario }: { vhost: string; model: MLModel; scenario: Scenario }) {
  const forKey = scenario === 'visits' ? 'salesman' : scenario === 'products' || scenario === 'reorder' ? 'customer' : 'product'
  const [id, setId] = useState('')
  const [period, setPeriod] = useState(() => nextPeriod(7))
  const [excludeSeen, setExcludeSeen] = useState(true)
  const [group, setGroup] = useState<CustomerGroup>(ANY_GROUP)
  const [kind, setKind] = useState<ProductKind>(ANY_KIND)
  // A visit plan and a product's audience are lists of customers; a
  // customer's products and a product's look-alikes are lists of products.
  const listsCustomers = scenario === 'visits' || scenario === 'audience'
  const listsProducts = scenario === 'products' || scenario === 'similar'
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
      for: { [forKey]: id.trim(), ...(listsCustomers ? groupFor(group) : {}), ...(listsProducts ? kindFor(kind) : {}) } as RecommendRequest['for'],
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
        {listsCustomers && <CustomerGroupFields value={group} onChange={setGroup} />}
        {listsProducts && <ProductKindFields value={kind} onChange={setKind} />}
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
