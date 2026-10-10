import { ActionIcon, Alert, Badge, Box, Group, Select, Stack, Switch, Text, Textarea, TextInput } from '@mantine/core'
import { IconAlertTriangle, IconHandStop, IconTrash } from '@tabler/icons-react'
import { AgentFixedConfig } from './AgentFixedConfig'
import { AgentParamsEditor } from './AgentParamsEditor'
import { McpToolSettings } from './McpToolSettings'
import {
  isWriteTool,
  MCP_KIND,
  needsApproval,
  SINK_KIND,
  TOOL_KINDS,
  type AgentIssue,
  type AgentTool,
  type WorkflowNodeLike,
} from './agentTools'

interface AgentToolCardProps {
  index: number
  tool: AgentTool
  issues: AgentIssue[]
  sinks: WorkflowNodeLike[]
  onChange: (tool: AgentTool) => void
  onRemove: () => void
}

const sinkLabel = (n: WorkflowNodeLike) => {
  const name = n.data?.label || n.id
  return n.data?.type ? `${name} (${n.data.type})` : name
}

/** One tool of an ai_agent node: what the model sees, what runs, and who approves it. */
export function AgentToolCard({ index, tool, issues, sinks, onChange, onRemove }: AgentToolCardProps) {
  const set = (patch: Partial<AgentTool>) => onChange({ ...tool, ...patch })
  const errorFor = (field: string) =>
    issues.filter((i) => i.severity === 'error' && i.field === field).map((i) => i.message).join(' ') || undefined
  const isSink = tool.kind === SINK_KIND
  const isMcp = tool.kind === MCP_KIND
  const writes = isWriteTool(tool)
  const approval = needsApproval(tool)
  const label = `Tool ${index + 1}`

  const chooseKind = (kind: string) => {
    if (kind === tool.kind) return
    // Each kind keeps only its own settings.
    const { config: _config, nodeId: _nodeId, server: _server, tool: _remote, ...rest } = tool
    if (kind === SINK_KIND) {
      // A sink always writes.
      const { write: _write, ...sink } = rest
      onChange({ ...sink, kind })
    } else if (kind === MCP_KIND) {
      // With no arguments declared yet, the model sees the server's own schema.
      const { parameters, ...mcp } = rest
      onChange({ ...mcp, kind, server: { url: '' }, tool: '', ...(parameters?.length ? { parameters } : {}) })
    } else {
      onChange({ ...rest, kind, config: {} })
    }
  }

  return (
    <Box
      component="fieldset"
      aria-label={label}
      p="sm"
      m={0}
      style={{
        minWidth: 0,
        border: `1px solid ${writes && !approval ? 'var(--mantine-color-red-outline)' : 'var(--mantine-color-default-border)'}`,
        borderRadius: 'var(--mantine-radius-sm)',
      }}
    >
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs">
            <Text size="sm" fw={600}>
              {label}
            </Text>
            <Badge size="xs" variant="light" color={writes ? 'orange' : 'teal'}>
              {writes ? 'Writes' : 'Reads'}
            </Badge>
            {approval && (
              <Badge size="xs" variant="light" color="yellow" leftSection={<IconHandStop size="0.7rem" />}>
                Needs approval
              </Badge>
            )}
          </Group>
          <ActionIcon aria-label={`Remove tool ${index + 1}`} color="red" variant="subtle" onClick={onRemove}>
            <IconTrash size="1rem" />
          </ActionIcon>
        </Group>

        <Group grow align="flex-start" gap="sm">
          <TextInput
            label="Name"
            placeholder="e.g. find_customer"
            value={tool.name ?? ''}
            onChange={(e) => set({ name: e.currentTarget.value })}
            required
            error={errorFor('name')}
            description="Letters, digits, _ or -. The model calls the tool by it."
          />
          <Select
            label="Kind"
            data={TOOL_KINDS}
            value={tool.kind || null}
            onChange={(v) => v && chooseKind(v)}
            allowDeselect={false}
            required
            error={errorFor('kind')}
          />
        </Group>
        <Textarea
          label="Description"
          placeholder="e.g. Looks a customer up by email and returns their plan and open orders."
          value={tool.description ?? ''}
          onChange={(e) => set({ description: e.currentTarget.value })}
          autosize
          minRows={2}
          description="What the model is told the tool does and when to use it."
        />

        {isMcp ? (
          <McpToolSettings tool={tool} issues={issues} onChange={onChange} />
        ) : isSink ? (
          <Select
            label="Sink node"
            placeholder={sinks.length ? 'Choose the sink node' : 'Add a sink node to the workflow first'}
            data={sinks.map((n) => ({ value: n.id, label: sinkLabel(n) }))}
            value={tool.nodeId || null}
            onChange={(v) => set({ nodeId: v ?? '' })}
            required
            error={errorFor('nodeId')}
            description="Each call writes its arguments, as one record, to this sink."
          />
        ) : (
          tool.kind && (
            <>
              <AgentFixedConfig
                key={tool.kind}
                kind={tool.kind}
                value={tool.config}
                onChange={(config) => set({ config })}
              />
              <Switch
                label="This tool changes something outside Hermod"
                description="For example an API call that creates or updates a record. A tool that writes waits for approval."
                checked={tool.write === true}
                onChange={(e) => set({ write: e.currentTarget.checked })}
              />
            </>
          )
        )}

        {writes && (
          <Switch
            label="Ask a person to approve each call"
            description="The run pauses on the Approvals page until someone approves or rejects the call."
            checked={approval}
            onChange={(e) => set({ requireApproval: e.currentTarget.checked })}
            color="green"
          />
        )}
        {writes && !approval && (
          <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Writes without review">
            <Text size="sm">
              The model can call this tool without anyone approving it, and message data can steer the model. Turn
              approval off only when every call is safe to make unreviewed.
            </Text>
          </Alert>
        )}

        {!(isMcp && tool.parameters === undefined) && (
          <AgentParamsEditor params={tool.parameters ?? []} onChange={(parameters) => set({ parameters })} />
        )}
        {errorFor('parameters') && (
          <Text size="xs" c="var(--mantine-color-error)">
            {errorFor('parameters')}
          </Text>
        )}
      </Stack>
    </Box>
  )
}
