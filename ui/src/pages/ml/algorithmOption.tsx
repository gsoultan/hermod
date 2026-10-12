import { Stack, Text, type SelectProps } from '@mantine/core'
import { DEEP_IMAGE_REASON, type AlgorithmOption } from '@/lib/mlModels'

/**
 * Draws an Algorithm picker's choices: a disabled one says, under its name,
 * what it needs, so a choice the worker cannot train reads as "not here yet"
 * rather than broken.
 */
export function algorithmOptionRenderer(options: AlgorithmOption[]): SelectProps['renderOption'] {
  const why = new Map(options.filter((o) => o.description).map((o) => [o.value, o.description]))
  return ({ option }) => {
    const description = why.get(option.value)
    if (!description) return option.label
    return (
      <Stack gap={0}>
        <Text size="sm">{option.label}</Text>
        <Text size="xs" c="dimmed">{description}</Text>
      </Stack>
    )
  }
}

/** The Algorithm picker's help: what Auto does, and what the neural networks need when they are off. */
export function algorithmDescription(options: AlgorithmOption[]): string {
  const auto = 'Auto tries several and keeps the best; pick one only if you need it.'
  const deepOff = options.some((o) => o.disabled && o.description === DEEP_IMAGE_REASON)
  return deepOff
    ? `${auto} Neural networks (PyTorch, TensorFlow) need the deep-learning worker image (hermod-ml:…-dl).`
    : auto
}
