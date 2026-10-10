import { Group, Stack, Switch, Text, Textarea, TextInput } from '@mantine/core'

interface TemplateRenderConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
}

/**
 * template_render (pkg/comm/transformer/structure/template_render.go): a Go
 * text/template over the record's fields, written to one field. Saves
 * template, targetField, strict and maxBytes.
 */
export function TemplateRenderConfig({ config, updateNodeConfig, nodeId }: TemplateRenderConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)

  return (
    <Stack gap="sm">
      <Textarea
        label="Template"
        description="Go template syntax over the record's fields — {{.name}}, {{range .lines}}…{{end}}, {{printf &quot;%.2f&quot; .total}}"
        placeholder="Hello {{.customer.name}}, your order {{.id}} has shipped."
        autosize
        minRows={4}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        value={config.template ?? ''}
        onChange={(e) => set({ template: e.currentTarget.value })}
        required
      />
      <Group grow align="flex-start">
        <TextInput
          label="Target field"
          placeholder="rendered"
          description="Where the text is written"
          value={config.targetField ?? ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
        <TextInput
          label="Max bytes"
          placeholder="65536"
          description="Longer output fails the record (at most 1 MiB)"
          inputMode="numeric"
          value={config.maxBytes ?? ''}
          onChange={(e) => set({ maxBytes: e.currentTarget.value.replace(/\D/g, '') })}
        />
      </Group>
      <Switch
        label="Strict: a missing field fails the record"
        description="Off: a field the record does not have renders as empty text"
        checked={config.strict === true || config.strict === 'true'}
        onChange={(e) => set({ strict: e.currentTarget.checked })}
      />
      <Text size="xs" c="dimmed">
        The template sees the record's fields and nothing else: no secrets, no environment, no files.
      </Text>
    </Stack>
  )
}
