import { useMemo } from 'react'
import { Alert, Anchor, Autocomplete, Select, Stack, Text } from '@mantine/core'
import { TextInput } from '@/components/shared/ReferenceInputs'
import { IconInfoCircle } from '@tabler/icons-react'
import { BACKEND_LABEL, useVHostModels } from '@/lib/mlModels'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

interface MLFeedbackConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

/**
 * The Feedback node: tell a mimir model the real answer to one of its earlier
 * predictions, once the record that knows it comes through. The saved config
 * is what pkg/comm/transformer/ml reads: model, predictionIdField (default
 * prediction.prediction_id, where the Predict node leaves it), actualField
 * and outputField (default feedback, written as {right, prediction_id}).
 * Only a model on a mimir server takes feedback, so the others are listed
 * but cannot be chosen.
 */
export function MLFeedbackConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: MLFeedbackConfigProps) {
  const vhost = useWorkflowStore((s) => s.vhost) || 'default'
  const { data: models = [], isLoading } = useVHostModels(vhost)
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const model = useMemo(() => models.find((m) => m.name === config.model), [models, config.model])
  const mimirModels = models.filter((m) => m.backend === 'mimir')

  return (
    <Stack gap="sm">
      <Select
        label="Model"
        description={`A model in ${vhost} served by mimir-server. The others cannot take feedback.`}
        placeholder={isLoading ? 'Loading models…' : 'Choose a model'}
        data={models.map((m) => ({
          value: m.name,
          label: m.backend === 'mimir' ? m.name : `${m.name} (${BACKEND_LABEL[m.backend] ?? m.backend})`,
          disabled: m.backend !== 'mimir',
        }))}
        value={config.model || null}
        onChange={(v) => set({ model: v ?? '' })}
        searchable
        required
        nothingFoundMessage="No model by that name"
      />
      {!isLoading && mimirModels.length === 0 && (
        <Alert color="blue" icon={<IconInfoCircle size="1rem" />} title={`No mimir model in ${vhost} yet`}>
          Connect one on the <Anchor href="/ml/models">Machine Learning</Anchor> page: the mimir server's address,
          the secret holding its API key, and the model to call.
        </Alert>
      )}
      {model?.description && <Text size="xs" c="dimmed">{model.description}</Text>}

      <Autocomplete
        label="Prediction id field"
        description="Where the record carries the id the prediction came with. The Predict node writes it at prediction.prediction_id."
        placeholder="prediction.prediction_id"
        data={fieldPaths}
        value={config.predictionIdField ?? ''}
        onChange={(v) => set({ predictionIdField: v })}
      />
      <Autocomplete
        label="Real answer field"
        description="The record field holding what really happened: the label, or the number for a model that answers with one."
        placeholder="e.g. outcome"
        data={fieldPaths}
        value={config.actualField ?? ''}
        onChange={(v) => set({ actualField: v })}
        required
      />
      <TextInput
        label="Output field"
        description="Where the verdict is written: {right, prediction_id}."
        placeholder="feedback"
        value={config.outputField ?? ''}
        onChange={(e) => set({ outputField: e.currentTarget.value })}
      />
    </Stack>
  )
}
