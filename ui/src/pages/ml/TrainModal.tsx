import { useCallback, useState } from 'react'
import {
  Accordion, Alert, Button, Checkbox, Group, List, Loader, Modal, Radio, ScrollArea, Select, SimpleGrid, Stack, Stepper, Text, TextInput,
} from '@mantine/core'
import { IconAlertCircle, IconInfoCircle } from '@tabler/icons-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { FormRow } from '@/components/common/FormRow'
import {
  DEFAULT_PRESET, DEVICE_OPTIONS, LEAVE_OUT_ROLES, MODEL_NAME_PATTERN, PRESET_OPTIONS, RECOMMENDATION, RECOMMEND_ALGORITHM_OPTIONS,
  TASK_OPTIONS, algorithmLabel, algorithmOptions, isCustomAlgorithm, isDeepAlgorithm, listVersions, mlModelsKey, modelNameRule,
  trainCheck, useDataset, useVHostDatasets, useVHostScripts, useWorkerStatus, versionsKey, type GoLive,
  type RecommendAlgorithm, type TrainAlgorithm, type TrainCheck, type TrainResult, type TrainSpec, type TrainDevice, type TrainPreset, type TrainTask,
} from '@/lib/mlModels'
import {
  DEFAULT_WEIGHTS, ROLES, defaultRuleChoices, isScenarioVersion, rulesFromChoices, scenarioLabel, scenariosSupported,
  type RecommendData, type RoleKey, type Scenario, type ScenarioTrainSpec, type ScenarioVersion, type Weights,
} from '@/lib/mlRecommend'
import { algorithmDescription, algorithmOptionRenderer } from './algorithmOption'
import { DatasetPreview } from './datasetPreview'
import { DeepParamsFields, readDeepParams, type DeepParamsText } from './deepParams'
import { GoLiveField } from './goLive'
import { PresetPicker } from './presetPicker'
import {
  HealthStep, RoleMappingStep, RulesStep, ScenarioPicker, healthAllowsNext, rolesComplete, rulesValid, useRecommendCheck,
} from './recommendSteps'
import { TrainResultView } from './TrainResultView'
import { TrainingProgress, useTraining } from './trainingJob'

/** The steps in order: predicting and the one-table recommender, or a recommendation scenario. */
type StepName = 'data' | 'target' | 'care' | 'needs' | 'health' | 'rules' | 'train' | 'result'
const PLAIN_STEPS: StepName[] = ['data', 'target', 'care', 'train', 'result']
const SCENARIO_STEPS: StepName[] = ['data', 'target', 'needs', 'health', 'rules', 'train', 'result']

/** What the model is for: predict one column, or recommend items to people. */
type Goal = 'predict' | 'recommend'

/** The columns a recommender learns from; strength and time are optional. */
type RecommendCols = { user: string | null; item: string | null; strength: string | null; time: string | null }
const NO_COLS: RecommendCols = { user: null, item: null, strength: null, time: null }

const GOALS: Array<{ value: Goal; label: string; description: string }> = [
  {
    value: 'predict', label: 'Predict a column',
    description: 'Learn the answer in one column from the others, such as whether a customer will leave.',
  },
  {
    value: 'recommend', label: 'Recommend items to people',
    description: 'Learn who had what, such as order lines, and suggest each person what they are likely to pick next.',
  },
]

/** The target as the check sees it, in words. */
function targetSentence(check: TrainCheck): string {
  if (check.target_kind === 'yes_no') return 'This is a yes/no question: Hermod will learn to predict yes or no.'
  if (check.target_kind === 'category') {
    return `This is a category: Hermod will learn to pick one of ${check.classes ?? 'several'} answers.`
  }
  return 'This is a number: Hermod will learn to estimate it.'
}

/**
 * Trains a model on one of the vhost's datasets, a step at a time, for someone
 * who knows nothing about machine learning: the data, what to predict (checked
 * first, with fixes for what would spoil the model), how careful to be, train,
 * and the result in plain words. Instead of predicting a column, it can
 * learn to recommend: on a server with recommendation scenarios, the person
 * picks one of five cards, points each table it needs at a dataset, sees the
 * data health check and sets the rules in sentences; an older server gets the
 * one-table recommender from who-had-what rows. Training `modelName` again
 * starts from its last version's choices, on the same path: `task` is the
 * model's, so a recommender opens as one before its versions load. The expert
 * controls stay under Advanced.
 */
export function TrainModal({ vhost, modelName, task: modelTask, onClose }: {
  vhost: string; modelName?: string; task?: string; onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [at, setAt] = useState<StepName>('data')
  const [name, setName] = useState(modelName ?? '')
  const [nameError, setNameError] = useState<string | null>(null)
  const [dataset, setDataset] = useState<string | null>(null)
  const [target, setTarget] = useState<string | null>(null)
  // The columns left out; every other column is learned from.
  const [left, setLeft] = useState<string[]>([])
  const [preset, setPreset] = useState<TrainPreset>(DEFAULT_PRESET)
  const [task, setTask] = useState<TrainTask>('auto')
  const [algorithm, setAlgorithm] = useState<TrainAlgorithm>('auto')
  const [device, setDevice] = useState<TrainDevice>('cpu')
  const [goLive, setGoLive] = useState<GoLive>({ mode: 'never' })
  const [deepText, setDeepText] = useState<DeepParamsText>({})
  const [deepErrors, setDeepErrors] = useState<ReturnType<typeof readDeepParams>['errors']>({})
  // The check whose flagged columns were left out, so a column put back stays in.
  const [applied, setApplied] = useState<string | null>(null)
  const [seeded, setSeeded] = useState(!modelName)
  const [seedFeatures, setSeedFeatures] = useState<string[] | null>(null)
  // What a model does is fixed once it exists.
  const [goal, setGoal] = useState<Goal>(modelTask === RECOMMENDATION ? 'recommend' : 'predict')
  const [cols, setCols] = useState<RecommendCols>(NO_COLS)
  const [recAlgorithm, setRecAlgorithm] = useState<RecommendAlgorithm>('auto')
  const recommending = goal === 'recommend'
  const [scenario, setScenario] = useState<Scenario | null>(null)
  const [recData, setRecData] = useState<RecommendData>({})
  const [choices, setChoices] = useState(() => defaultRuleChoices('visits'))
  const [weights, setWeights] = useState<Weights>(DEFAULT_WEIGHTS)
  const pickScenario = (s: Scenario) => {
    setScenario(s)
    setChoices(defaultRuleChoices(s))
  }
  // A server without the scenario routes gets the one-table recommender.
  const probe = useQuery({
    queryKey: ['ml-recommend-scenarios', vhost],
    queryFn: ({ signal }) => scenariosSupported(vhost, signal),
    enabled: recommending,
    retry: false,
    staleTime: 5 * 60_000,
  })
  const scenarioFlow = recommending && probe.data === true
  const steps = scenarioFlow ? SCENARIO_STEPS : PLAIN_STEPS
  const active = steps.indexOf(at)

  const { data: worker } = useWorkerStatus()
  const { data: datasets = [] } = useVHostDatasets(vhost)
  const { data: info } = useDataset(vhost, dataset)
  const columns = (info?.columns ?? []).map((c) => c.name)
  const caps = worker?.capabilities
  const { data: scripts = [] } = useVHostScripts(vhost, !!caps?.custom_scripts)
  const algorithms = algorithmOptions(worker, scripts)
  const custom = isCustomAlgorithm(algorithm)
  // A script trains on its own pool; the device applies to built-in algorithms.
  const showDevice = !!caps?.gpu && !custom
  const deep = isDeepAlgorithm(algorithm)
  const others = columns.filter((c) => c !== target)
  const numberColumns = (info?.columns ?? []).filter((c) => c.type === 'number').map((c) => c.name)
  const learnFrom = others.filter((c) => !left.includes(c))

  // Training again starts from the last version: its dataset, and its target
  // and features or, for a recommender, its columns.
  const { data: versions } = useQuery({
    queryKey: versionsKey(vhost, modelName ?? ''),
    queryFn: ({ signal }) => listVersions(vhost, modelName as string, signal),
    enabled: !!modelName,
    retry: false,
  })
  if (!seeded && versions) {
    setSeeded(true)
    const last = versions[0]
    if (last?.task === RECOMMENDATION && isScenarioVersion(last)) {
      const v = last as unknown as ScenarioVersion & { scenario: Scenario }
      setGoal('recommend')
      setDataset(v.roles?.transactions?.dataset ?? last.dataset)
      setScenario(v.scenario)
      setChoices(defaultRuleChoices(v.scenario))
      setRecData(v.roles ?? { transactions: { dataset: last.dataset } })
      if (v.weights) setWeights(v.weights)
      setAt('needs')
    } else if (last?.task === RECOMMENDATION && last.recommend) {
      const r = last.recommend
      setGoal('recommend')
      setDataset(last.dataset)
      setCols({ user: r.user, item: r.item, strength: r.strength ?? null, time: r.time ?? null })
      // On a server with scenarios, a one-table recommender trains again as "products to offer".
      setScenario('products')
      setChoices(defaultRuleChoices('products'))
      setRecData({
        transactions: {
          dataset: last.dataset, customer: r.user, product: r.item,
          ...(r.strength ? { quantity: r.strength } : {}), ...(r.time ? { date: r.time } : {}),
        },
      })
      setAt('target')
    } else if (last) {
      setDataset(last.dataset)
      setTarget(last.target)
      setSeedFeatures(last.features ?? [])
      setAt('target')
    }
  }
  if (seedFeatures && info && target) {
    setSeedFeatures(null)
    if (seedFeatures.length) setLeft((l) => [...new Set([...l, ...others.filter((c) => !seedFeatures.includes(c))])])
  }

  const checkKey = `${dataset}\u0000${target}\u0000${task}`
  // The check screens a column to predict; a recommender has none.
  const check = useQuery({
    queryKey: ['ml-train-check', vhost, dataset ?? '', target ?? '', task],
    queryFn: ({ signal }) => trainCheck(vhost, { dataset: dataset as string, target: target as string, task, go_live: { mode: 'never' } }, signal),
    enabled: !recommending && !!dataset && !!target,
    retry: false,
    staleTime: 30_000,
  })
  const checked = check.data ?? undefined
  if (checked && applied !== checkKey) {
    setApplied(checkKey)
    const flagged = checked.columns.filter((c) => LEAVE_OUT_ROLES.includes(c.role) && c.name !== target).map((c) => c.name)
    if (flagged.length) setLeft((l) => [...new Set([...l, ...flagged])])
  }
  const health = useRecommendCheck(vhost, scenario, recData, scenarioFlow && at === 'health')

  const notes = new Map((checked?.columns ?? []).filter((c) => c.note).map((c) => [c.name, c.note as string]))

  // What the Train button sends.
  const spec = (): TrainSpec => {
    if (scenarioFlow && scenario) {
      const scenarioSpec: ScenarioTrainSpec = {
        dataset: recData.transactions?.dataset ?? (dataset as string),
        target: '',
        task: RECOMMENDATION,
        scenario,
        data: recData,
        ...(scenario === 'visits' ? { weights } : {}),
        rules: rulesFromChoices(scenario, choices, recData),
        algorithm: recAlgorithm,
        go_live: goLive,
      }
      // The scenario fields are the contract's (ml-plan phase9-contract.md), beyond TrainSpec.
      return scenarioSpec as unknown as TrainSpec
    }
    if (recommending) {
      // No preset: Auto always tries both methods, and the server refuses one.
      return {
        dataset: dataset as string,
        target: '',
        task: RECOMMENDATION,
        algorithm: recAlgorithm as TrainAlgorithm,
        recommend: {
          user: cols.user as string,
          item: cols.item as string,
          ...(cols.strength ? { strength: cols.strength } : {}),
          ...(cols.time ? { time: cols.time } : {}),
        },
        go_live: goLive,
      }
    }
    const { params } = deep ? readDeepParams(deepText) : { params: undefined }
    return {
      dataset: dataset as string,
      target: target as string,
      features: left.length ? learnFrom : undefined,
      task,
      algorithm,
      device: showDevice && device === 'gpu' ? 'gpu' : undefined,
      params: deep ? params : undefined,
      preset: algorithm === 'auto' ? preset : undefined,
      go_live: goLive,
    }
  }
  // Training runs as a job: it goes on if this window closes, and the Jobs
  // list on the Machine Learning page follows it there.
  const trainedName = name.trim()
  const onTrained = useCallback((res: TrainResult) => {
    setAt('result')
    queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
    queryClient.invalidateQueries({ queryKey: versionsKey(vhost, res.model?.name ?? trainedName) })
  }, [queryClient, vhost, trainedName])
  const train = useTraining(vhost, onTrained)

  const pickDataset = (v: string | null) => {
    setDataset(v)
    setTarget(null)
    setLeft([])
    setApplied(null)
    setCols(NO_COLS)
    setRecData(v ? { transactions: { dataset: v } } : {})
  }
  const setCol = (key: keyof RecommendCols) => (v: string | null) => setCols((c) => ({ ...c, [key]: v }))
  // The columns another question already took.
  const free = (key: keyof RecommendCols) => columns.filter((c) => !Object.entries(cols).some(([k, v]) => k !== key && v === c))
  const pickTarget = (v: string | null) => {
    setTarget(v)
    setLeft([])
    setApplied(null)
  }
  const leaveOut = (column: string, out: boolean) => setLeft((l) => (out ? [...new Set([...l, column])] : l.filter((c) => c !== column)))

  const canGoOn = at === 'data' ? !!dataset && (!!modelName || !!name.trim())
    : at === 'target'
      ? (recommending
        ? (scenarioFlow ? !!scenario : !probe.isPending && !!cols.user && !!cols.item)
        : !!target && learnFrom.length > 0 && checked?.ok !== false)
      : at === 'needs' ? !!scenario && rolesComplete(scenario, recData)
        : at === 'health' ? healthAllowsNext(health)
          : at === 'rules' ? !!scenario && rulesValid(scenario, choices)
            : true

  const goOn = () => {
    if (at === 'data' && !modelName) {
      const bad = !MODEL_NAME_PATTERN.test(name.trim())
      setNameError(bad ? `"${name.trim()}" is not a valid name.` : null)
      if (bad) return
    }
    if (at === 'care' && !recommending) {
      const { errors } = deep ? readDeepParams(deepText) : { errors: {} }
      setDeepErrors(errors)
      if (Object.keys(errors).length) return
    }
    setAt(steps[active + 1])
  }

  const presetLabel = PRESET_OPTIONS.find((o) => o.value === preset)?.label
  const advancedOpen = recommending ? recAlgorithm !== 'auto' : algorithm !== 'auto' || task !== 'auto' || device === 'gpu'
  const recLabel = RECOMMEND_ALGORITHM_OPTIONS.find((o) => o.value === recAlgorithm)?.label

  return (
    <Modal opened onClose={onClose} title={modelName ? `Train ${modelName} again` : 'Train a model'} centered size="xl">
      <Stack gap="md">
        <Stepper active={active} size="sm" allowNextStepsSelect={false}
          onStepClick={(i) => { if (i < active && at !== 'result' && !train.running) setAt(steps[i]) }}>
          <Stepper.Step label="Your data">
            <Stack gap="md">
              {!modelName && (
                <TextInput label="Model name" description={modelNameRule} placeholder="churn" required value={name}
                  onChange={(e) => { setName(e.currentTarget.value); setNameError(null) }} error={nameError} data-autofocus />
              )}
              <Select label="Dataset" placeholder={datasets.length ? 'Choose a dataset' : 'No datasets yet'} required searchable
                description={datasets.length
                  ? 'The table of rows Hermod learns from. Each row is one example, such as one customer.'
                  : 'Add one under Datasets on the Machine Learning page: a CSV or Excel file, or rows from a database.'}
                data={datasets.map((d) => ({ value: d.name, label: d.name }))} value={dataset}
                onChange={pickDataset} nothingFoundMessage="No dataset by that name" />
              {info && (
                <Stack gap={4}>
                  <Text size="sm" c="dimmed">
                    {info.rows.toLocaleString()} rows, {info.columns.length} columns. {info.sample?.length ? 'The first rows:' : ''}
                  </Text>
                  {!!info.sample?.length && <DatasetPreview info={info} limit={5} caption={`First rows of ${info.name}`} />}
                </Stack>
              )}
            </Stack>
          </Stepper.Step>

          <Stepper.Step label="What to do">
            <Stack gap="md">
              {!modelName && (
                <Radio.Group label="What do you want to do?" value={goal} onChange={(v) => setGoal(v as Goal)}>
                  <SimpleGrid cols={{ base: 1, sm: 2 }} mt="xs">
                    {GOALS.map((g) => (
                      <Radio.Card key={g.value} value={g.value} radius="md" p="sm">
                        <Group wrap="nowrap" align="flex-start" gap="sm">
                          <Radio.Indicator mt={2} />
                          <Stack gap={4}>
                            <Text fw={600} size="sm">{g.label}</Text>
                            <Text size="xs" c="dimmed">{g.description}</Text>
                          </Stack>
                        </Group>
                      </Radio.Card>
                    ))}
                  </SimpleGrid>
                </Radio.Group>
              )}
              {recommending && probe.isPending ? (
                <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">Loading…</Text></Group>
              ) : scenarioFlow ? (
                <ScenarioPicker value={scenario} onChange={pickScenario} />
              ) : recommending ? (
                <>
                  <Text size="sm" c="dimmed">
                    Use rows where each one says someone had something: an order line, a view, a play. Hermod learns who had
                    what and recommends each person what people like them had.
                  </Text>
                  <FormRow cols={2}>
                    <Select label="Which column is the person?" description="Such as a customer or account id." required
                      searchable disabled={!dataset} data={free('user')} value={cols.user} onChange={setCol('user')} />
                    <Select label="Which column is the item?" description="Such as a product, article or song id." required
                      searchable disabled={!dataset} data={free('item')} value={cols.item} onChange={setCol('item')} />
                    <Select label="How much did they like it?" description="Optional: a rating, quantity or count. Without one, every row counts as liked."
                      searchable clearable disabled={!dataset} data={numberColumns.filter((c) => free('strength').includes(c))}
                      value={cols.strength} onChange={setCol('strength')} />
                    <Select label="When did it happen?" description="Optional: with it, Hermod tests on each person's latest picks."
                      searchable clearable disabled={!dataset} data={free('time')} value={cols.time} onChange={setCol('time')} />
                  </FormRow>
                  <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />} title="No check is needed">
                    Hermod holds back {cols.time ? 'each person\'s latest picks' : 'a random share of each person\'s picks'} and
                    tests whether the top 10 it recommends hold them, next to recommending everyone the most popular items.
                    {cols.time ? '' : ' With a "when" column, it tests on the latest picks, which is how recommendations are judged in real life.'}
                  </Alert>
                </>
              ) : (<>
              <Select label="Column to predict" description="The answer you want Hermod to learn, such as whether a customer left."
                placeholder="The target" required searchable disabled={!dataset} data={columns} value={target} onChange={pickTarget} />
              {target && check.isFetching && !checked && (
                <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">Checking your data…</Text></Group>
              )}
              {checked && (
                <Stack gap={2}>
                  <Text size="sm" fw={500}>{targetSentence(checked)}</Text>
                  <Text size="sm" c="dimmed">
                    {checked.usable_rows.toLocaleString()} of {checked.rows.toLocaleString()} rows can be used.
                  </Text>
                </Stack>
              )}
              {check.error && (
                <Alert color="yellow" variant="light" icon={<IconInfoCircle size="1rem" />} title="Your data was not checked">
                  {(check.error as Error).message} You can still train.
                </Alert>
              )}
              {!!checked?.warnings.length && (
                <Alert color={checked.ok ? 'yellow' : 'red'} variant="light" icon={<IconAlertCircle size="1rem" />}
                  title={checked.ok ? 'Things to know about your data' : 'Hermod cannot train on this yet'}>
                  <List size="sm" spacing="xs">
                    {checked.warnings.map((w, i) => (
                      <List.Item key={`${w.code}-${w.column ?? i}`}>
                        <Group gap="xs" wrap="wrap">
                          <Text size="sm">{w.message}</Text>
                          {w.column && w.column !== target && others.includes(w.column) && (
                            left.includes(w.column) ? (
                              <Button size="compact-xs" variant="subtle" onClick={() => leaveOut(w.column as string, false)}>
                                Use {w.column} anyway
                              </Button>
                            ) : (
                              <Button size="compact-xs" variant="light" onClick={() => leaveOut(w.column as string, true)}>
                                Leave out {w.column}
                              </Button>
                            )
                          )}
                          {w.code === 'few_rows' && (
                            <Button size="compact-xs" variant="light" onClick={() => setAt('data')}>Choose another dataset</Button>
                          )}
                          {w.code === 'many_classes' && (
                            <Button size="compact-xs" variant="light" onClick={() => pickTarget(null)}>Pick another column</Button>
                          )}
                        </Group>
                      </List.Item>
                    ))}
                  </List>
                </Alert>
              )}
              {target && others.length > 0 && (
                <Stack gap={6}>
                  <Text size="sm" fw={500}>Learn from these columns</Text>
                  <Text size="xs" c="dimmed">Untick a column to leave it out.</Text>
                  <ScrollArea.Autosize mah={260} type="auto">
                    <Stack gap={6}>
                      {others.map((c) => (
                        <Checkbox key={c} label={c} description={notes.get(c)} checked={!left.includes(c)}
                          onChange={(e) => leaveOut(c, !e.currentTarget.checked)} />
                      ))}
                    </Stack>
                  </ScrollArea.Autosize>
                  {learnFrom.length === 0 && <Text size="xs" c="red">Tick at least one column to learn from.</Text>}
                </Stack>
              )}
              </>)}
            </Stack>
          </Stepper.Step>

          {!scenarioFlow && <Stepper.Step label="How careful">
            {recommending ? (
              <Stack gap="md">
                {recAlgorithm === 'auto' && (
                  <Text size="sm">
                    Hermod will try both methods and keep the better one: one learns what people like each person had, the
                    other which items are had together. There is nothing more to choose.
                  </Text>
                )}
                <Accordion variant="contained" keepMounted={false} defaultValue={advancedOpen ? 'advanced' : null}>
                  <Accordion.Item value="advanced">
                    <Accordion.Control>Advanced</Accordion.Control>
                    <Accordion.Panel>
                      <Select label="Method" description="Auto tries both and keeps the better." data={RECOMMEND_ALGORITHM_OPTIONS}
                        value={recAlgorithm} allowDeselect={false} onChange={(v) => setRecAlgorithm((v ?? 'auto') as RecommendAlgorithm)} />
                    </Accordion.Panel>
                  </Accordion.Item>
                </Accordion>
              </Stack>
            ) : (
            <Stack gap="md">
              <PresetPicker value={preset} onChange={setPreset} disabled={algorithm !== 'auto'} />
              <Accordion variant="contained" keepMounted={false} defaultValue={advancedOpen ? 'advanced' : null}>
                <Accordion.Item value="advanced">
                  <Accordion.Control>Advanced</Accordion.Control>
                  <Accordion.Panel>
                    <Stack gap="sm">
                      <FormRow cols={showDevice ? 3 : 2}>
                        <Select label="Task" description="Leave it to decide from the target unless it guesses wrong."
                          data={TASK_OPTIONS} value={task} allowDeselect={false}
                          onChange={(v) => setTask((v ?? 'auto') as TrainTask)} />
                        <Select label="Algorithm" description={algorithmDescription(algorithms)} data={algorithms}
                          renderOption={algorithmOptionRenderer(algorithms)} value={algorithm} allowDeselect={false}
                          onChange={(v) => setAlgorithm((v ?? 'auto') as TrainAlgorithm)} />
                        {showDevice && (
                          <Select label="Device" description="GPU trains on the GPU worker pool." data={DEVICE_OPTIONS} value={device}
                            allowDeselect={false} onChange={(v) => setDevice((v ?? 'cpu') as TrainDevice)} />
                        )}
                      </FormRow>
                      {custom && (
                        <Text size="xs" c="dimmed">
                          The script runs in a sandbox on the custom-script worker pool. Its ONNX export is checked and scored on the
                          held-back rows like any other version.
                        </Text>
                      )}
                      {deep && (
                        <DeepParamsFields value={deepText} errors={deepErrors}
                          onChange={(patch) => { setDeepText((t) => ({ ...t, ...patch })); setDeepErrors({}) }} />
                      )}
                    </Stack>
                  </Accordion.Panel>
                </Accordion.Item>
              </Accordion>
            </Stack>
            )}
          </Stepper.Step>}

          {scenarioFlow && scenario && (
            <Stepper.Step label="Data you need">
              <RoleMappingStep vhost={vhost} scenario={scenario} data={recData} datasets={datasets} onChange={setRecData} />
            </Stepper.Step>
          )}
          {scenarioFlow && (
            <Stepper.Step label="Data health">
              <HealthStep check={health} />
            </Stepper.Step>
          )}
          {scenarioFlow && scenario && (
            <Stepper.Step label="Rules">
              <RulesStep scenario={scenario} data={recData} choices={choices} onChoices={setChoices} weights={weights} onWeights={setWeights} />
            </Stepper.Step>
          )}

          <Stepper.Step label="Train">
            <Stack gap="md">
              {scenarioFlow && scenario ? (
                <List size="sm" spacing={4}>
                  <List.Item>Model: {name.trim()}</List.Item>
                  <List.Item>Recommends: {scenarioLabel(scenario)}</List.Item>
                  {(Object.keys(recData) as RoleKey[]).map((r) => (
                    <List.Item key={r}>{ROLES[r].label}: {recData[r]?.dataset}</List.Item>
                  ))}
                  <List.Item>Rules: {rulesFromChoices(scenario, choices, recData).length || 'none'}</List.Item>
                </List>
              ) : recommending ? (
                <List size="sm" spacing={4}>
                  <List.Item>Model: {name.trim()}</List.Item>
                  <List.Item>Data: {dataset}{info ? ` (${info.rows.toLocaleString()} rows)` : ''}</List.Item>
                  <List.Item>People: {cols.user}</List.Item>
                  <List.Item>Items: {cols.item}</List.Item>
                  <List.Item>How much they liked it: {cols.strength ?? 'every row counts once'}</List.Item>
                  <List.Item>When: {cols.time ?? 'not given, so a random share is held back to test on'}</List.Item>
                  <List.Item>Method: {recLabel}</List.Item>
                </List>
              ) : (
                <List size="sm" spacing={4}>
                  <List.Item>Model: {name.trim()}</List.Item>
                  <List.Item>Data: {dataset}{info ? ` (${info.rows.toLocaleString()} rows)` : ''}</List.Item>
                  <List.Item>Predicts: {target}</List.Item>
                  <List.Item>Learns from: {left.length ? learnFrom.join(', ') : 'every other column'}</List.Item>
                  <List.Item>{algorithm === 'auto' ? `Care: ${presetLabel}` : `Algorithm: ${algorithmLabel(algorithm)}`}</List.Item>
                </List>
              )}
              <GoLiveField value={goLive} onChange={setGoLive} />
              {train.running && (
                <TrainingProgress job={train.job} cancelling={train.cancelling} onCancel={train.cancel}
                  about={(recommending ? recAlgorithm : algorithm) === 'auto'
                    ? `Hermod is trying ${recommending ? 'both methods and keeping the better' : 'a few methods and keeping the best'}. `
                    : ''} />
              )}
              {train.error && (
                <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not trained">
                  {train.error.message}
                </Alert>
              )}
              {train.cancelled && (
                <Alert color="gray" icon={<IconInfoCircle size="1rem" />} title="The training was stopped">
                  No new version was made. Train again when you are ready.
                </Alert>
              )}
              {train.cancelError && (
                <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The training was not stopped">
                  {train.cancelError.message}
                </Alert>
              )}
            </Stack>
          </Stepper.Step>

          <Stepper.Step label="Result">
            {train.trained && <TrainResultView vhost={vhost} result={train.trained} exampleRow={info?.sample?.[0]} />}
          </Stepper.Step>
        </Stepper>

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>{at === 'result' ? 'Close' : 'Cancel'}</Button>
          {active > 0 && at !== 'result' && (
            <Button variant="default" disabled={train.running} onClick={() => setAt(steps[active - 1])}>Back</Button>
          )}
          {active >= 0 && active < steps.indexOf('train') && <Button onClick={goOn} disabled={!canGoOn}>Next</Button>}
          {at === 'train' && <Button loading={train.running} onClick={() => train.start(trainedName, spec())}>Train</Button>}
        </Group>
      </Stack>
    </Modal>
  )
}
