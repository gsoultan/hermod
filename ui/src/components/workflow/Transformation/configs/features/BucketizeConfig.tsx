import { Select, Stack, TagsInput, TextInput } from '@mantine/core'
import { type FeatureConfigProps, FieldInput, OnMissingSelect } from './shared'

/** edges is saved as the text typed; a list from the API is shown as text. */
function edgesText(v: unknown): string {
  if (Array.isArray(v)) return v.join(', ')
  return typeof v === 'string' ? v : ''
}

function labelList(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(String)
  if (typeof v === 'string' && v.trim() !== '') return v.split(',').map((s) => s.trim()).filter(Boolean)
  return []
}

/** What the backend would refuse in edges, said before the workflow is saved. */
function edgesProblem(text: string): string | null {
  const parts = text.split(/[,\n]/).map((s) => s.trim()).filter(Boolean)
  if (parts.length === 0) return null
  const nums = parts.map(Number)
  const bad = parts.find((p, i) => p === '' || !Number.isFinite(nums[i]))
  if (bad !== undefined) return `"${bad}" is not a number`
  if (nums.length < 2) return 'Give at least two edges'
  for (let i = 1; i < nums.length; i++) {
    if (nums[i] <= nums[i - 1]) return `Edges must increase: ${nums[i]} comes after ${nums[i - 1]}`
  }
  return null
}

/**
 * Bucketize: put a number into a bin. Edges e0..en make n bins; each holds its
 * lower edge, and the last also holds en.
 */
export function BucketizeConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: FeatureConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const edges = edgesText(config.edges)
  const labels = labelList(config.labels)
  const edgeError = edgesProblem(edges)
  const edgeCount = edges.split(/[,\n]/).map((s) => s.trim()).filter(Boolean).length
  const labelError =
    !edgeError && labels.length > 0 && edgeCount >= 2 && labels.length !== edgeCount - 1
      ? `${edgeCount} edges make ${edgeCount - 1} bins, but there ${labels.length === 1 ? 'is 1 label' : `are ${labels.length} labels`}`
      : null

  return (
    <Stack gap="sm">
      <FieldInput value={config.field} onChange={(v) => set({ field: v })} fieldPaths={fieldPaths} />
      <TextInput
        label="Edges"
        description="Increasing numbers, comma-separated. 0, 18, 65, 120 makes [0, 18), [18, 65), [65, 120]."
        placeholder="0, 18, 65, 120"
        value={edges}
        onChange={(e) => set({ edges: e.currentTarget.value })}
        error={edgeError}
        required
      />
      <TagsInput
        label="Labels"
        description="One per bin, in order. Without labels the bin's number (from 0) is written."
        placeholder="Type a label and press Enter"
        value={labels}
        onChange={(v) => set({ labels: v })}
        error={labelError}
        clearable
      />
      <Select
        label="Outside the edges"
        data={[
          { value: 'null', label: 'Write null' },
          { value: 'clip', label: 'Use the first or last bin' },
          { value: 'fail', label: 'Fail the record' },
        ]}
        value={config.outOfRange || 'null'}
        onChange={(v) => set({ outOfRange: v ?? 'null' })}
        allowDeselect={false}
      />
      <TextInput
        label="Target field"
        placeholder={`${config.field || 'field'}_bin`}
        value={config.targetField ?? ''}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
      />
      <OnMissingSelect value={config.onMissing} onChange={(v) => set({ onMissing: v })} />
    </Stack>
  )
}
