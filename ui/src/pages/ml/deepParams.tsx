import { Divider, Group, Stack, Text, TextInput } from '@mantine/core'
import { DEFAULT_TRAIN_PARAMS, parseHiddenLayers, type TrainParams } from '@/lib/mlModels'

/**
 * The hyperparameter fields as typed: text, so a half-typed number is kept.
 * The Train Model node saves exactly these strings under these names.
 */
export interface DeepParamsText {
  hiddenLayers?: string
  epochs?: string
  batchSize?: string
  learningRate?: string
  patience?: string
}

const WHOLE = /^\d+$/

/**
 * Turns the typed fields into TrainParams, leaving empty ones to the worker's
 * defaults. Returns the problems instead when a field cannot be read; the
 * worker checks the bounds.
 */
export function readDeepParams(text: DeepParamsText): { params?: TrainParams; errors: Partial<Record<keyof DeepParamsText, string>> } {
  const errors: Partial<Record<keyof DeepParamsText, string>> = {}
  const params: TrainParams = {}
  const layers = (text.hiddenLayers ?? '').trim()
  if (layers) {
    const sizes = parseHiddenLayers(layers)
    if (sizes === null) errors.hiddenLayers = 'Layer sizes are whole numbers above zero, separated by commas.'
    else params.hidden_layers = sizes
  }
  const whole = [['epochs', 'epochs'], ['batchSize', 'batch_size'], ['patience', 'patience']] as const
  for (const [field, key] of whole) {
    const v = (text[field] ?? '').trim()
    if (!v) continue
    if (!WHOLE.test(v)) errors[field] = 'A whole number.'
    else params[key] = Number(v)
  }
  const rate = (text.learningRate ?? '').trim()
  if (rate) {
    const n = Number(rate)
    if (!Number.isFinite(n) || n <= 0) errors.learningRate = 'A number above zero, such as 0.001.'
    else params.learning_rate = n
  }
  if (Object.keys(errors).length) return { errors }
  return { params: Object.keys(params).length ? params : undefined, errors }
}

/**
 * The "Advanced" section for the neural-network algorithms: layer sizes,
 * epochs, batch size, learning rate and early-stopping patience. Empty
 * fields use the worker's defaults, shown as placeholders.
 */
export function DeepParamsFields({
  value, onChange, errors = {},
}: {
  value: DeepParamsText
  onChange: (patch: DeepParamsText) => void
  errors?: Partial<Record<keyof DeepParamsText, string>>
}) {
  const d = DEFAULT_TRAIN_PARAMS
  return (
    <Stack gap="xs">
      <Divider label="Advanced" labelPosition="left" />
      <TextInput label="Hidden layers" description="Units per layer, first to last." placeholder={d.hidden_layers}
        value={value.hiddenLayers ?? ''} error={errors.hiddenLayers}
        onChange={(e) => onChange({ hiddenLayers: e.currentTarget.value })} />
      <Group grow align="flex-start">
        <TextInput label="Epochs" description="At most 1000." placeholder={d.epochs} inputMode="numeric"
          value={value.epochs ?? ''} error={errors.epochs} onChange={(e) => onChange({ epochs: e.currentTarget.value })} />
        <TextInput label="Batch size" placeholder={d.batch_size} inputMode="numeric"
          value={value.batchSize ?? ''} error={errors.batchSize} onChange={(e) => onChange({ batchSize: e.currentTarget.value })} />
      </Group>
      <Group grow align="flex-start">
        <TextInput label="Learning rate" placeholder={d.learning_rate} inputMode="decimal"
          value={value.learningRate ?? ''} error={errors.learningRate} onChange={(e) => onChange({ learningRate: e.currentTarget.value })} />
        <TextInput label="Patience" description="Epochs without improvement before stopping." placeholder={d.patience}
          inputMode="numeric" value={value.patience ?? ''} error={errors.patience}
          onChange={(e) => onChange({ patience: e.currentTarget.value })} />
      </Group>
      <Text size="xs" c="dimmed">
        A tenth of the training rows decides when to stop. Neural networks need the worker&apos;s &quot;-dl&quot; image.
      </Text>
    </Stack>
  )
}
