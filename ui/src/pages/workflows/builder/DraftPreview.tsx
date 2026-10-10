import { Alert, Badge, Button, Code, Group, List, Paper, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertTriangle, IconCircleX, IconExternalLink, IconInfoCircle } from '@tabler/icons-react'
import type { BuilderIssue, BuildResponse } from '@/lib/workflowBuilder'

const severityMeta: Record<string, { color: string; label: string; icon: typeof IconInfoCircle }> = {
  error: { color: 'red', label: 'Error', icon: IconCircleX },
  warning: { color: 'yellow', label: 'Warning', icon: IconAlertTriangle },
}

function issueMeta(severity: string) {
  return severityMeta[severity] ?? { color: 'blue', label: severity || 'Note', icon: IconInfoCircle }
}

/** What a node is: its transformation for a transformation node, else its type. */
function kindOf(node: { type: string; config?: Record<string, unknown> }): string {
  const tt = node.config?.transType
  return node.type === 'transformation' && typeof tt === 'string' && tt ? tt : node.type
}

/**
 * The drafted workflow as a node and edge list, with every issue the builder
 * and the save-time validator found. Read-only: it is changed in the editor.
 */
export function DraftPreview({ result, onOpenInEditor }: { result: BuildResponse; onOpenInEditor: () => void }) {
  const { workflow } = result
  const nodes = workflow.nodes ?? []
  const edges = workflow.edges ?? []
  const issues: BuilderIssue[] = result.issues ?? []
  const errors = issues.filter((i) => i.severity === 'error').length

  return (
    <Paper withBorder p="md" radius="md" aria-live="polite">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={2}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Draft</Text>
            <Title order={4}>{workflow.name}</Title>
            <Text size="xs" c="dimmed">
              Drafted with {result.provider} / {result.model} · {result.usage.input_tokens.toLocaleString()} in ·{' '}
              {result.usage.output_tokens.toLocaleString()} out tokens
            </Text>
          </Stack>
          <Button leftSection={<IconExternalLink size="1rem" />} onClick={onOpenInEditor}>
            Open in editor
          </Button>
        </Group>

        <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />}>
          Nothing has been saved. The draft opens in the editor as a new, stopped workflow; review it, fill in what
          is missing, and save it there. It does not run until you start it.
        </Alert>

        {issues.length > 0 && (
          <Stack gap="xs">
            <Title order={6}>
              {issues.length} issue{issues.length === 1 ? '' : 's'} to resolve
              {errors > 0 && ` (${errors} blocking)`}
            </Title>
            {issues.map((issue, i) => {
              const meta = issueMeta(issue.severity)
              const Icon = meta.icon
              return (
                <Alert key={i} color={meta.color} variant="light" p="xs" icon={<Icon size="1rem" />}>
                  <Stack gap={2}>
                    <Group gap={6}>
                      <Badge size="xs" color={meta.color} variant="filled">{meta.label}</Badge>
                      {issue.node_id && <Code>{issue.node_id}</Code>}
                    </Group>
                    <Text size="sm">{issue.message}</Text>
                    {issue.recommendation && <Text size="xs" c="dimmed">{issue.recommendation}</Text>}
                  </Stack>
                </Alert>
              )
            })}
          </Stack>
        )}

        <Stack gap="xs">
          <Title order={6}>Nodes ({nodes.length})</Title>
          <Table.ScrollContainer minWidth={420}>
            <Table aria-label="Draft nodes" verticalSpacing={4} fz="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Node</Table.Th>
                  <Table.Th>Kind</Table.Th>
                  <Table.Th>Label</Table.Th>
                  <Table.Th>Uses</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {nodes.map((n) => (
                  <Table.Tr key={n.id}>
                    <Table.Td><Code>{n.id}</Code></Table.Td>
                    <Table.Td>{kindOf(n)}</Table.Td>
                    <Table.Td>{typeof n.config?.label === 'string' ? n.config.label : ''}</Table.Td>
                    <Table.Td>{n.ref_id ? <Code>{n.ref_id}</Code> : <Text span size="xs" c="dimmed">—</Text>}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>

        <Stack gap="xs">
          <Title order={6}>Connections ({edges.length})</Title>
          {edges.length === 0 ? (
            <Text size="sm" c="dimmed">No connections.</Text>
          ) : (
            <List size="sm" spacing={2}>
              {edges.map((e) => (
                <List.Item key={e.id}>
                  <Text span ff="monospace" size="sm">
                    {e.source_id} → {e.target_id}
                    {e.source_handle ? ` (${e.source_handle})` : ''}
                  </Text>
                </List.Item>
              ))}
            </List>
          )}
        </Stack>
      </Stack>
    </Paper>
  )
}
