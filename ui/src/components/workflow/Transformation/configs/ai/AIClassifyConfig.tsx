import { useMemo } from 'react'
import {
  ActionIcon, Alert, Box, Button, Divider, Group, NumberInput, Stack, Text, Textarea, TextInput,
} from '@mantine/core'
import { IconAlertTriangle, IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import { classifyLabels, UNSURE_BRANCH } from '@/pages/workflows/WorkflowEditor/nodes/branchHandleId'
import { AIConnectionSection, type AISectionProps } from './AIConnectionSection'
import { AIDataSection } from './AIDataSection'

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

interface LabelRow {
  label: string
  description?: string
}

/**
 * The rows being edited. A saved list is kept as it is — blank rows included,
 * so a label just added stays on screen — while text forms (a JSON list or
 * comma-separated names, from the API or an import) are read the way the
 * engine reads them and saved back as a list on the first edit.
 */
function labelRows(raw: unknown): LabelRow[] {
  if (Array.isArray(raw)) {
    return raw.map((item) =>
      typeof item === 'string' ? { label: item, description: '' } : { ...item, label: str(item?.label) },
    )
  }
  return classifyLabels(raw)
}

/**
 * ai_classify: the model puts each record into one of the labels, and the
 * record leaves on that label's branch — or on `unsure` when the model is
 * not confident enough or answers with something else.
 */
export function AIClassifyConfig({ config, nodeId, updateNodeConfig }: AISectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const rows = useMemo(() => labelRows(config.labels), [config.labels])
  const names = rows.map((r) => r.label.trim()).filter(Boolean)
  const repeated = [...new Set(names.filter((n, i) => names.indexOf(n) !== i))]
  const reserved = names.includes(UNSURE_BRANCH)

  const update = (i: number, patch: Partial<LabelRow>) =>
    set({ labels: rows.map((r, j) => (j === i ? { ...r, ...patch } : r)) })
  const add = () => set({ labels: [...rows, { label: '', description: '' }] })
  const remove = (i: number) => set({ labels: rows.filter((_, j) => j !== i) })

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="grape" variant="light">
        <Text size="sm">
          Routes each record down the branch of the label the model picks. Answers below the confidence threshold,
          or outside the labels, go down <b>unsure</b>. Connect each label&apos;s output on the canvas.
        </Text>
      </Alert>

      <AIConnectionSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />

      <Divider label="Labels" labelPosition="center" />
      {names.length === 0 && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />}>
          <Text size="sm">At least one label is required. Add one for each branch you want.</Text>
        </Alert>
      )}
      <Stack gap="sm">
        {rows.map((row, i) => (
          <Box
            key={i}
            p="xs"
            style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 'var(--mantine-radius-sm)' }}
          >
            <Group gap="xs" align="flex-start" wrap="nowrap">
              <TextInput
                label={`Label ${i + 1} name`}
                placeholder="e.g. billing"
                value={row.label}
                onChange={(e) => update(i, { label: e.currentTarget.value })}
                flex={1}
                size="sm"
              />
              <TextInput
                label={`Label ${i + 1} description`}
                placeholder="When to choose it (optional)"
                value={str(row.description)}
                onChange={(e) => update(i, { description: e.currentTarget.value })}
                flex={2}
                size="sm"
              />
              <ActionIcon
                aria-label={`Remove label ${i + 1}`}
                color="red"
                variant="subtle"
                onClick={() => remove(i)}
                mt={28}
              >
                <IconTrash size="1rem" />
              </ActionIcon>
            </Group>
          </Box>
        ))}
      </Stack>
      {repeated.length > 0 && (
        <Text size="sm" c="var(--mantine-color-yellow-text)">
          {repeated.map((r) => `“${r}”`).join(', ')} is used more than once; those labels share one branch.
        </Text>
      )}
      {reserved && (
        <Text size="sm" c="var(--mantine-color-yellow-text)">
          “{UNSURE_BRANCH}” is already the branch for low-confidence answers; a label with that name shares it.
        </Text>
      )}
      <Button variant="light" leftSection={<IconPlus size="1rem" />} onClick={add} w="fit-content">
        Add label
      </Button>
      <Text size="xs" c="dimmed">
        Renaming a label disconnects the edges drawn from its output; reconnect them on the canvas.
      </Text>

      <Textarea
        label="Instructions"
        placeholder="e.g. A message that mentions a refund is billing, even when it also reports a bug."
        value={str(config.instructions)}
        onChange={(e) => set({ instructions: e.currentTarget.value })}
        autosize
        minRows={2}
        description="Optional. How to choose between labels."
      />
      <NumberInput
        label="Confidence threshold"
        placeholder="0 (always take the model's label)"
        min={0}
        max={1}
        step={0.05}
        decimalScale={2}
        value={str(config.threshold)}
        onChange={(v) => set({ threshold: v === '' ? '' : String(v) })}
        description="0 to 1. Below it, the record goes down unsure."
      />
      <Group grow align="flex-start" gap="sm">
        <TextInput
          label="Label field"
          placeholder="ai_label"
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description="Receives the chosen label."
        />
        <TextInput
          label="Confidence field"
          placeholder="ai_confidence"
          value={str(config.confidenceField)}
          onChange={(e) => set({ confidenceField: e.currentTarget.value })}
          description="Receives the model's confidence, 0 to 1."
        />
      </Group>

      <AIDataSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />
    </Stack>
  )
}
