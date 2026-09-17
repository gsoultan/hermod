import { Suspense } from 'react'
import { Card, Stack, Group, Text, Badge, Alert, Code } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import { resolveConfigComponent } from '../Transformation/configs/registry'
import type { NodeReviewReason, ReviewNode } from '@/utils/importBundle'

const REASON_LABEL: Record<NodeReviewReason, { text: string; color: string }> = {
  'source-reference': { text: 'points at a source', color: 'blue' },
  credential: { text: 'holds a credential', color: 'red' },
  endpoint: { text: 'calls an address', color: 'orange' },
}

interface ImportNodeCardProps {
  review: ReviewNode
  /**
   * Sources the node's pickers may choose from — the bundle's own plus the ones
   * already on this instance, so a lookup can be repointed at a local source
   * instead of importing another copy of it.
   */
  sources: any[]
  /**
   * Mirrors the canvas store's updateNodeConfig, `replace` included: the config
   * editors send a *patch* and expect it merged, and a few of them (the pipeline
   * editor) send a whole config with replace set. Treating a patch as a
   * replacement would silently drop every field the editor did not resend.
   */
  onChange: (nodeId: string, config: any, replace?: boolean) => void
}

/**
 * One node from the bundle that holds something environment-specific, rendered
 * with the editor the workflow canvas would use for it.
 *
 * Reusing that editor rather than writing a second one is the whole point: a
 * db_lookup's source picker, an encrypt node's key format rules and an API
 * lookup's auth fields already exist and already agree with what the engine
 * reads. A parallel set of fields here would drift from them within a release.
 */
export function ImportNodeCard({ review, sources, onChange }: ImportNodeCardProps) {
  const { node, transType, reasons, fields } = review
  const Config = resolveConfigComponent(node.type, transType)

  return (
    <Card withBorder radius="md" padding="lg">
      <Stack gap="md">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs">
            <Text fw={600}>{node.config?.label || transType || node.type}</Text>
            <Badge variant="light" size="sm">{node.id}</Badge>
          </Group>
          <Group gap={6}>
            {reasons.map((r) => (
              <Badge key={r} color={REASON_LABEL[r].color} variant="light" size="sm">
                {REASON_LABEL[r].text}
              </Badge>
            ))}
          </Group>
        </Group>

        <Text size="sm" c="dimmed">
          Check {fields.map((f, i) => (
            <span key={f}>
              {i > 0 && ', '}
              <Code>{f}</Code>
            </span>
          ))} before importing — {reasons.includes('credential')
            ? 'the bundle carries the key or password from the instance that exported it.'
            : 'these usually differ between instances.'}
        </Text>

        {Config ? (
          <Suspense fallback={<Text size="sm">Loading editor…</Text>}>
            <Config
              config={node.config ?? {}}
              nodeId={node.id}
              updateNodeConfig={(id: string, patch: any, replace?: boolean) => onChange(id, patch, replace)}
              transType={transType}
              sources={sources}
              availableFields={[]}
              fieldPaths={[]}
            />
          </Suspense>
        ) : (
          <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />}>
            No editor is registered for <Code>{transType || node.type}</Code>, so this node imports
            exactly as the file describes it. Open it on the canvas afterwards to change anything.
          </Alert>
        )}
      </Stack>
    </Card>
  )
}
