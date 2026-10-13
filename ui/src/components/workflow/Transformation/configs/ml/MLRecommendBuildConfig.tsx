import { useEffect, useMemo } from 'react'
import { Alert, Divider, Radio, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { FormRow } from '@/components/common/FormRow'
import { SQL_SOURCE_TYPES, isRecommender, listVersions, useVHostModels, versionsKey } from '@/lib/mlModels'
import { buildForScenario, isScenarioVersion, type BuildFor } from '@/lib/mlRecommend'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

interface MLRecommendBuildConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  sources?: any[]
}

const FOR_OPTIONS: Array<{ value: BuildFor; label: string }> = [
  { value: 'all_salesmen', label: 'Every salesman (their visit plan)' },
  { value: 'all_customers', label: 'Every customer' },
  { value: 'all_products', label: 'Every product' },
]

/**
 * The Build Recommendations node: each message that reaches it works out a
 * recommendation model's lists for everyone (every salesman's visit plan,
 * every customer's products, every product's customers) and emits one
 * message per row: model, version, recommendation_id, for_id, item_id, rank,
 * score, plan_date, due_date, reasons_text, built_at. With a table, it also
 * writes them there (due_date when the table has that column). A reorder
 * model can keep only the customers due within some days. The settings are
 * the model build schedule's (PUT .../build).
 */
export function MLRecommendBuildConfig({ config, updateNodeConfig, nodeId, sources = [] }: MLRecommendBuildConfigProps) {
  const vhost = useWorkflowStore((s) => s.vhost) || 'default'
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const { data: models = [], isLoading } = useVHostModels(vhost)
  const recommenders = models.filter(isRecommender)
  const model = useMemo(() => recommenders.find((m) => m.name === config.model), [recommenders, config.model])

  // Who a build is for follows the model's scenario until the person picks.
  const { data: versions } = useQuery({
    queryKey: versionsKey(vhost, model?.name ?? ''),
    queryFn: ({ signal }) => listVersions(vhost, model?.name as string, signal),
    enabled: !!model,
    retry: false,
    staleTime: 60_000,
  })
  const live = versions?.find((v) => v.version === model?.remote_version) ?? versions?.[0]
  const scenario = isScenarioVersion(live) ? live.scenario : undefined
  useEffect(() => {
    if (!config.for && versions) updateNodeConfig(nodeId, { for: scenario ? buildForScenario(scenario) : 'all_customers' })
  }, [config.for, versions, scenario, nodeId, updateNodeConfig])

  const dbSources = (Array.isArray(sources) ? sources : [])
    .filter((s: any) => SQL_SOURCE_TYPES.includes(s.type) && (s.vhost || 'default') === vhost)
    .map((s: any) => ({ value: s.id, label: s.name }))
  const plans = config.for === 'all_salesmen' && scenario !== 'reorder'
  const reorder = scenario === 'reorder'

  return (
    <Stack gap="sm">
      <Text size="xs" c="dimmed">
        Every message that reaches this node builds all the lists again, so put it in a workflow that runs on a
        schedule, such as every Monday morning.
      </Text>
      <Select label="Model" required searchable placeholder={isLoading ? 'Loading models…' : 'Choose a recommendation model'}
        description={`A recommendation model trained in ${vhost}.`}
        data={recommenders.map((m) => ({ value: m.name, label: m.name }))} value={config.model || null}
        onChange={(v) => set({ model: v ?? '', for: '' })} nothingFoundMessage="No recommendation model by that name" />
      {!isLoading && recommenders.length === 0 && (
        <Alert color="blue" icon={<IconInfoCircle size="1rem" />} title={`No recommendation models in ${vhost} yet`}>
          Train one on the Machine Learning page: Add model, Train a new model from my data, then Recommend items to people.
        </Alert>
      )}
      <FormRow cols={plans || reorder ? 3 : 2}>
        <Select label="For" data={FOR_OPTIONS} value={config.for || null} allowDeselect={false}
          onChange={(v) => set({ for: v ?? '' })} />
        {plans && (
          <TextInput label="Days to plan" placeholder="7" value={config.periodDays ?? ''}
            onChange={(e) => set({ periodDays: e.currentTarget.value })} />
        )}
        {reorder && (
          <TextInput label="Only customers due within (days)" placeholder="14" value={config.withinDays ?? ''}
            description="Overdue ones included; empty for everyone."
            onChange={(e) => set({ withinDays: e.currentTarget.value })} />
        )}
        <TextInput label="How many per list" placeholder="20" value={config.k ?? ''} onChange={(e) => set({ k: e.currentTarget.value })} />
      </FormRow>

      <Divider label="Also write them to a table (optional)" labelPosition="left" mt="xs" />
      <FormRow cols={2}>
        <Select label="Database" searchable clearable placeholder={dbSources.length ? 'None: only emit messages' : 'No database sources in this vhost'}
          data={dbSources} value={config.targetSourceId || null} onChange={(v) => set({ targetSourceId: v ?? '' })} />
        <TextInput label="Table" placeholder={plans ? 'visit_plan' : 'recommendations'} value={config.targetTable ?? ''}
          onChange={(e) => set({ targetTable: e.currentTarget.value })} />
      </FormRow>
      <Radio.Group label="Each run" value={config.targetMode || 'replace'} onChange={(v) => set({ targetMode: v })}>
        <Group gap="lg" mt={4}>
          <Radio value="replace" label="Replaces the table's rows" />
          <Radio value="append" label="Adds to them" />
        </Group>
      </Radio.Group>
    </Stack>
  )
}
