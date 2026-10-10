import { Stack, Switch, Text, TextInput } from '@mantine/core'

interface FieldDiffConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
}

/**
 * field_diff (pkg/comm/transformer/structure/field_diff.go): the columns a
 * change event changed, each as {"old", "new"}. Saves targetField,
 * ignoreColumns, onlyChanges and dropUnchanged.
 */
export function FieldDiffConfig({ config, updateNodeConfig, nodeId }: FieldDiffConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const target = config.targetField || 'changes'

  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed">
        Compares the change event's before- and after-image and lists the columns that differ. An insert has no
        before-image, so every column is new; a delete has no after-image, so every column goes to null. 2 and 2.0,
        or the same object in another key order, are not changes.
      </Text>
      <TextInput
        label="Target field"
        placeholder="changes"
        description="Where the changes are written"
        value={config.targetField ?? ''}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
        disabled={config.onlyChanges === true}
      />
      <TextInput
        label="Ignore columns"
        placeholder="updated_at, version"
        description="Never reported as changed; comma-separated"
        value={config.ignoreColumns ?? ''}
        onChange={(e) => set({ ignoreColumns: e.currentTarget.value })}
      />
      <Switch
        label="Replace the record with the changes"
        description={`Off: the record keeps its fields and gains ${target}. On: its data becomes the changes alone; the operation and table are kept.`}
        checked={config.onlyChanges === true || config.onlyChanges === 'true'}
        onChange={(e) => set({ onlyChanges: e.currentTarget.checked })}
      />
      <Switch
        label="Drop records with no changes"
        description="For updates that touched only ignored columns, or nothing at all"
        checked={config.dropUnchanged === true || config.dropUnchanged === 'true'}
        onChange={(e) => set({ dropUnchanged: e.currentTarget.checked })}
      />
    </Stack>
  )
}
