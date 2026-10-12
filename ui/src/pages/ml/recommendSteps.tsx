import { useEffect, useRef, useState } from 'react'
import {
  Alert, Badge, Button, Checkbox, Fieldset, Group, List, Loader, NumberInput, Radio, Select, SimpleGrid, Slider, Stack, Table,
  TagsInput, Text, ThemeIcon,
} from '@mantine/core'
import {
  IconAlertCircle, IconAlertTriangle, IconBook, IconCircleCheck, IconCircleX, IconDownload, IconInfoCircle, IconUpload,
} from '@tabler/icons-react'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { FormRow } from '@/components/common/FormRow'
import { useDataset, type DatasetInfo } from '@/lib/mlModels'
import {
  DEFAULT_WEIGHTS, ROLES, SCENARIOS, WEEKDAYS, WEIGHT_QUESTIONS, csvTemplate, guessColumns, recommendCheck, ruleOptions,
  scenarioRoles, type CheckStatus, type RecommendCheck, type RecommendData, type RoleKey, type RoleMapping, type RuleChoices,
  type Scenario, type ScenarioRole, type Weights,
} from '@/lib/mlRecommend'
import { UploadModal } from './DatasetsPanel'
import { DataGuideDrawer } from './dataGuide'

/** The five scenario cards, each with a real example, and the data guide. */
export function ScenarioPicker({ value, onChange }: { value: Scenario | null; onChange: (s: Scenario) => void }) {
  const [guide, setGuide] = useState(false)
  return (
    <Stack gap="xs">
      <Radio.Group label="What do you want to recommend?" value={value} onChange={(v) => onChange(v as Scenario)}>
        <SimpleGrid cols={{ base: 1, sm: 2 }} mt="xs">
          {SCENARIOS.map((s) => (
            <Radio.Card key={s.value} value={s.value} radius="md" p="sm">
              <Group wrap="nowrap" align="flex-start" gap="sm">
                <Radio.Indicator mt={2} />
                <Stack gap={4}>
                  <Text fw={600} size="sm">{s.label}</Text>
                  <Text size="xs" c="dimmed">{s.example}</Text>
                </Stack>
              </Group>
            </Radio.Card>
          ))}
        </SimpleGrid>
      </Radio.Group>
      <Group>
        <Button variant="subtle" size="compact-sm" leftSection={<IconBook size="1rem" />} onClick={() => setGuide(true)}>
          How to prepare your data
        </Button>
      </Group>
      <DataGuideDrawer opened={guide} onClose={() => setGuide(false)} />
    </Stack>
  )
}

/** Whether every required role has a dataset, and every mapped role its required columns. */
export function rolesComplete(scenario: Scenario, data: RecommendData): boolean {
  return scenarioRoles(scenario).every((r) => {
    const m = data[r.role]
    if (!m?.dataset) return !r.required
    return r.columns.every((c) => !c.required || !!m[c.key])
  })
}

/** Saves a role's CSV template, made here from its example rows. */
function downloadTemplate(role: RoleKey) {
  try {
    const url = URL.createObjectURL(new Blob([csvTemplate(role) + '\n'], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${role}-template.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    // A browser without object URLs has nothing to save to; the example table is still shown.
  }
}

/**
 * "What data you need": per role, what it holds and why this scenario wants it,
 * an example with a CSV template, the dataset that plays it (or an upload),
 * and its columns, guessed by name and type for the person to confirm.
 */
export function RoleMappingStep({ vhost, scenario, data, datasets, onChange }: {
  vhost: string
  scenario: Scenario
  data: RecommendData
  datasets: DatasetInfo[]
  onChange: (data: RecommendData) => void
}) {
  const [guide, setGuide] = useState(false)
  const setRole = (role: RoleKey, m: RoleMapping | undefined) => {
    const next = { ...data }
    if (m) next[role] = m
    else delete next[role]
    onChange(next)
  }
  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Text size="sm" c="dimmed" maw={520}>
          Point each table at one of your datasets. Hermod matches the columns by their names and types: check its
          guesses, then press Next.
        </Text>
        <Button variant="subtle" size="compact-sm" leftSection={<IconBook size="1rem" />} onClick={() => setGuide(true)}>
          How to prepare your data
        </Button>
      </Group>
      {scenarioRoles(scenario).map((r) => (
        <RoleFields key={r.role} vhost={vhost} spec={r} mapping={data[r.role]} datasets={datasets}
          onChange={(m) => setRole(r.role, m)} />
      ))}
      <DataGuideDrawer opened={guide} onClose={() => setGuide(false)} />
    </Stack>
  )
}

function RoleFields({ vhost, spec, mapping, datasets, onChange }: {
  vhost: string
  spec: ScenarioRole
  mapping: RoleMapping | undefined
  datasets: DatasetInfo[]
  onChange: (m: RoleMapping | undefined) => void
}) {
  const role = ROLES[spec.role]
  const [uploading, setUploading] = useState(false)
  const dataset = mapping?.dataset ?? null
  const { data: info } = useDataset(vhost, dataset)
  const columns = info?.columns ?? []
  // Guessed once per dataset; what the person picked is kept.
  const guessed = useRef<string | null>(null)
  useEffect(() => {
    if (!info || !mapping || guessed.current === info.name || info.name !== mapping.dataset) return
    guessed.current = info.name
    const guess = guessColumns(spec.role, info.columns)
    const kept = Object.fromEntries(Object.entries(mapping).filter(([k, v]) => k === 'dataset' || (v && info.columns.some((c) => c.name === v))))
    onChange({ ...guess, ...kept, dataset: info.name } as RoleMapping)
  }, [info, mapping, spec.role, onChange])

  const setColumn = (key: string, v: string | null) => {
    if (!mapping) return
    const next = { ...mapping }
    if (v) next[key] = v
    else delete next[key]
    onChange(next)
  }
  const free = (key: string) => columns.map((c) => c.name).filter((n) => !Object.entries(mapping ?? {}).some(([k, v]) => k !== key && k !== 'dataset' && v === n))

  return (
    <Fieldset legend={`${role.label}${spec.required ? '' : ' (optional)'}`} radius="md">
      <Stack gap="sm">
        <Text size="sm">{role.what} {spec.why}</Text>
        <Stack gap={4}>
          <Text size="xs" c="dimmed">Example</Text>
          <Table.ScrollContainer minWidth={420}>
            <Table fz="xs" withTableBorder striped aria-label={`Example ${role.label.toLowerCase()}`}>
              <Table.Thead><Table.Tr>{role.example.header.map((h) => <Table.Th key={h}>{h}</Table.Th>)}</Table.Tr></Table.Thead>
              <Table.Tbody>
                {role.example.rows.map((row, i) => (
                  <Table.Tr key={i}>{row.map((v, j) => <Table.Td key={j}>{v}</Table.Td>)}</Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
        <Group gap="xs" align="flex-end" wrap="wrap">
          <SelectDataset label={`${role.label} dataset`} required={spec.required} datasets={datasets} value={dataset}
            onChange={(v) => { guessed.current = null; onChange(v ? { dataset: v } as RoleMapping : undefined) }} />
          <Button variant="light" size="sm" leftSection={<IconUpload size="1rem" />} onClick={() => setUploading(true)}>Upload a file</Button>
          <Button variant="default" size="sm" leftSection={<IconDownload size="1rem" />} onClick={() => downloadTemplate(spec.role)}>
            Download CSV template
          </Button>
        </Group>
        {dataset && (
          <FormRow cols={2}>
            {spec.columns.map((c) => (
              <SelectColumn key={c.key} label={c.label} description={c.hint} required={c.required}
                data={free(c.key)} value={mapping?.[c.key] ?? null} onChange={(v) => setColumn(c.key, v)} />
            ))}
          </FormRow>
        )}
      </Stack>
      {uploading && <UploadModal vhost={vhost} initialName={spec.role} onClose={() => setUploading(false)} />}
    </Fieldset>
  )
}

function SelectDataset({ label, required, datasets, value, onChange }: {
  label: string; required: boolean; datasets: DatasetInfo[]; value: string | null; onChange: (v: string | null) => void
}) {
  return (
    <Select label={label} required={required} clearable={!required} searchable miw={240} style={{ flex: 1 }}
      placeholder={datasets.length ? 'Choose a dataset' : 'No datasets yet'}
      data={datasets.map((d) => ({ value: d.name, label: d.name }))} value={value} onChange={onChange}
      nothingFoundMessage="No dataset by that name" />
  )
}

function SelectColumn({ label, description, required, data, value, onChange }: {
  label: string; description: string; required: boolean; data: string[]; value: string | null; onChange: (v: string | null) => void
}) {
  return (
    <Select label={label} description={description} required={required} clearable={!required} searchable
      placeholder={required ? 'Choose a column' : 'Not in this dataset'} data={data} value={value} onChange={onChange} />
  )
}

/** The health check of a scenario's data; the dialog runs it on the Data health step. */
export function useRecommendCheck(vhost: string, scenario: Scenario | null, data: RecommendData, enabled: boolean) {
  const body = { task: 'recommendation' as const, scenario: scenario as Scenario, data }
  return useQuery({
    queryKey: ['ml-recommend-check', vhost, JSON.stringify(body)],
    queryFn: ({ signal }) => recommendCheck(vhost, body, signal),
    enabled: enabled && !!scenario,
    retry: false,
    staleTime: 30_000,
  })
}

/** Whether Next may leave the health step: the check passed, or could not run. */
export const healthAllowsNext = (check: UseQueryResult<RecommendCheck | null>) =>
  check.isError || (check.data !== undefined && (check.data === null || check.data.ok))

const STATUS: Record<CheckStatus, { color: string; label: string; icon: typeof IconCircleCheck }> = {
  green: { color: 'green', label: 'OK', icon: IconCircleCheck },
  amber: { color: 'yellow', label: 'Worth fixing', icon: IconAlertTriangle },
  red: { color: 'red', label: 'Must fix', icon: IconCircleX },
}

/** The numbers the check counted, in a sentence. */
export function statsSentence(s: RecommendCheck['stats']): string {
  const n = (v: number) => v.toLocaleString()
  return `${n(s.customers)} customers, ${n(s.products)} products and ${n(s.transactions)} order lines over ` +
    `${n(s.months_of_history)} months: about ${Number(s.tx_per_customer.toFixed(1)).toLocaleString()} order lines per customer.`
}

/** "Data health": green, amber or red per check, each with its plain fix. */
export function HealthStep({ check }: { check: UseQueryResult<RecommendCheck | null> }) {
  if (check.isFetching && !check.data) {
    return <Group gap="xs" aria-live="polite"><Loader size="xs" /><Text size="sm" c="dimmed">Checking your data…</Text></Group>
  }
  if (check.error) {
    return (
      <Alert color="yellow" variant="light" icon={<IconInfoCircle size="1rem" />} title="Your data was not checked">
        {(check.error as Error).message} You can still train.
      </Alert>
    )
  }
  if (check.data === null) {
    return <Text size="sm" c="dimmed">This Hermod does not check recommendation data before training. You can still train.</Text>
  }
  const r = check.data
  if (!r) return null
  return (
    <Stack gap="md">
      <Text size="sm">{statsSentence(r.stats)}</Text>
      {!r.ok && (
        <Alert color="red" variant="light" icon={<IconAlertCircle size="1rem" />} title="Hermod cannot train on this yet">
          Fix what is marked red, then check again.
        </Alert>
      )}
      <List spacing="sm" listStyleType="none" aria-label="Data health checks">
        {r.checks.map((c) => {
          const s = STATUS[c.status] ?? STATUS.amber
          const Icon = s.icon
          return (
            <List.Item key={c.code} icon={<ThemeIcon color={s.color} variant="light" size="sm" radius="xl"><Icon size="0.9rem" /></ThemeIcon>}>
              <Group gap="xs" wrap="wrap">
                <Text size="sm" fw={500}>{c.message}</Text>
                <Badge size="xs" variant="light" color={s.color} tt="none">{s.label}</Badge>
              </Group>
              {c.fix && <Text size="sm" c="dimmed">{c.fix}</Text>}
            </List.Item>
          )
        })}
      </List>
      <Group>
        <Button variant="default" size="xs" loading={check.isFetching} onClick={() => check.refetch()}>Check again</Button>
      </Group>
    </Stack>
  )
}

const DAY_LABEL: Record<string, string> = { mon: 'Mon', tue: 'Tue', wed: 'Wed', thu: 'Thu', fri: 'Fri', sat: 'Sat', sun: 'Sun' }

/** Whether the rules can be saved: a visit plan needs at least one visit a day and one working day. */
export const rulesValid = (scenario: Scenario, c: RuleChoices) =>
  scenario !== 'visits' || (c.visitsPerDay >= 1 && c.workingDays.length > 0)

// An emptied number field reads as 0, shown empty, so typing replaces it.
const days = (v: string | number, fallback: number) => (typeof v === 'number' ? v : fallback)

/**
 * "Rules": sentences the person switches on, each one a contract Rule. Visit
 * planning adds the visits per day, the working days, and how much each
 * signal counts, in words, with a reset.
 */
export function RulesStep({ scenario, data, choices, onChoices, weights, onWeights }: {
  scenario: Scenario
  data: RecommendData
  choices: RuleChoices
  onChoices: (c: RuleChoices) => void
  weights: Weights
  onWeights: (w: Weights) => void
}) {
  const can = ruleOptions(scenario, data)
  const set = (patch: Partial<RuleChoices>) => onChoices({ ...choices, ...patch })
  const customersAreItems = scenario === 'visits' || scenario === 'audience' || scenario === 'reorder'
  const excludeSentence = scenario === 'visits' ? 'Skip customers visited in the last'
    : scenario === 'audience' ? 'Skip customers who bought it in the last' : 'Do not offer what the customer bought in the last'
  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        These apply to every answer. A request can narrow them (a salesman, dates, more filters) but never loosen them.
      </Text>
      <Stack gap="sm">
        {can.ownCustomers && (
          <Checkbox label="Only the salesman's own customers" checked={choices.ownCustomers}
            onChange={(e) => set({ ownCustomers: e.currentTarget.checked })} />
        )}
        {can.territory && (
          <Checkbox label="Only customers in the salesman's territory" checked={choices.territory}
            onChange={(e) => set({ territory: e.currentTarget.checked })} />
        )}
        {can.exclude && (
          <Group gap="xs" wrap="wrap">
            <Checkbox label={excludeSentence} checked={choices.excludeOn} onChange={(e) => set({ excludeOn: e.currentTarget.checked })} />
            <NumberInput aria-label="Days" w={80} size="xs" min={1} max={365} disabled={!choices.excludeOn}
              value={choices.excludeDays || ''} onChange={(v) => set({ excludeDays: days(v, 0) })} />
            <Text size="sm">days</Text>
          </Group>
        )}
        {can.skipBlocked && (
          <Checkbox label="Skip customers on credit hold" checked={choices.skipBlocked}
            onChange={(e) => set({ skipBlocked: e.currentTarget.checked })} />
        )}
        {can.onlyActive && (
          <Checkbox label="Only active products" checked={choices.onlyActive} onChange={(e) => set({ onlyActive: e.currentTarget.checked })} />
        )}
        {can.preferMargin && (
          <Checkbox label="Prefer products with a higher margin" checked={choices.preferMargin}
            onChange={(e) => set({ preferMargin: e.currentTarget.checked })} />
        )}
        {can.maxPerCategory && (
          <Group gap="xs" wrap="wrap">
            <Checkbox label="At most" checked={choices.maxPerCategoryOn} onChange={(e) => set({ maxPerCategoryOn: e.currentTarget.checked })} />
            <NumberInput aria-label="Products per category" w={80} size="xs" min={1} max={100} disabled={!choices.maxPerCategoryOn}
              value={choices.maxPerCategory || ''} onChange={(v) => set({ maxPerCategory: days(v, 0) })} />
            <Text size="sm">products from one category</Text>
          </Group>
        )}
        {can.promote && (
          <TagsInput label={customersAreItems ? 'Give these customers a push' : 'Promote these products'}
            description={customersAreItems ? 'Their ids; press Enter after each.' : 'Such as this month\'s promotion list: their ids, Enter after each.'}
            value={choices.promoteIds} onChange={(ids) => set({ promoteIds: ids })} clearable />
        )}
      </Stack>
      {can.capacity && (
        <Stack gap="sm">
          <FormRow cols={2}>
            <NumberInput label="Visits per day" description="At most this many visits a day for each salesman." min={1} max={100}
              value={choices.visitsPerDay || ''} onChange={(v) => set({ visitsPerDay: days(v, 0) })}
              error={choices.visitsPerDay < 1 ? 'At least one visit a day.' : undefined} />
            <Checkbox.Group label="Working days"
              description={data.salesmen?.working_days ? 'Taken from the salesmen table.' : 'The days visits are planned on.'}
              value={choices.workingDays} onChange={(v) => set({ workingDays: v })}
              error={choices.workingDays.length ? undefined : 'Pick at least one day.'}>
              <Group gap="xs" mt={6}>
                {WEEKDAYS.map((d) => (
                  <Checkbox key={d} value={d} label={DAY_LABEL[d]} disabled={!!data.salesmen?.working_days} />
                ))}
              </Group>
            </Checkbox.Group>
          </FormRow>
          <WeightSliders weights={weights} onChange={onWeights} />
        </Stack>
      )}
    </Stack>
  )
}

function WeightSliders({ weights, onChange }: { weights: Weights; onChange: (w: Weights) => void }) {
  return (
    <Fieldset legend="What makes a customer worth a visit" radius="md">
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          Hermod adds these up for every customer and plans the highest first. They do not have to add up to 100%.
        </Text>
        {WEIGHT_QUESTIONS.map((q) => (
          <Stack key={q.key} gap={4}>
            <Text size="sm">{q.question}</Text>
            <Slider thumbLabel={q.question} min={0} max={100} step={5} label={(v) => `${v}%`}
              value={Math.round(weights[q.key] * 100)} onChange={(v) => onChange({ ...weights, [q.key]: v / 100 })} />
          </Stack>
        ))}
        <Group>
          <Button variant="default" size="xs" onClick={() => onChange(DEFAULT_WEIGHTS)}>Reset the weights</Button>
        </Group>
      </Stack>
    </Fieldset>
  )
}
