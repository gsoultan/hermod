import { useMemo } from 'react'
import { Alert, Button, Divider, Group, NumberInput, Stack, Text, Textarea, TextInput } from '@mantine/core'
import { IconAlertTriangle, IconInfoCircle, IconPlus } from '@tabler/icons-react'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { AIConnectionSection, type AISectionProps } from '../ai/AIConnectionSection'
import { AIDataSection } from '../ai/AIDataSection'
import { AgentToolCard } from './AgentToolCard'
import { AGENT_FIELDS, AGENT_LIMITS, agentIssues, goDurationMs, sinkNodes, toolList, type AgentTool } from './agentTools'

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

/** Numbers are saved as text, which the engine's number() reads as well. */
const asText = (v: string | number) => (v === '' ? '' : String(v))

const capNote = (raw: unknown, cap: number) => {
  const n = Number(str(raw))
  return str(raw).trim() !== '' && n > cap ? `The engine caps this at ${cap.toLocaleString('en-US')}.` : undefined
}

const NEW_TOOL: AgentTool = { name: '', description: '', kind: 'db_lookup', parameters: [], config: {} }

/**
 * ai_agent: a model works towards a goal by calling only the tools listed
 * here, with only the arguments each declares. Tools that write wait for a
 * person on the Approvals page unless approval is turned off for them; an
 * mcp tool counts as one unless the workflow marks it write: false and its
 * server marks it read-only.
 * Keys match internal/engine/registry/nodes/ai/agent/config.go.
 */
export function AIAgentConfig({ config, nodeId, updateNodeConfig }: AISectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const nodes = useWorkflowStore((s) => s.nodes)
  const sinks = useMemo(() => sinkNodes(nodes as any), [nodes])
  const { tools, error: toolsError } = useMemo(() => toolList(config.tools), [config.tools])
  const issues = agentIssues(config, nodes as any)
  const fieldError = (field: string) => issues.find((i) => i.field === field && i.severity === 'error')?.message

  const goal = str(config.goal) || str(config.prompt)
  const timeoutMs = goDurationMs(str(config.timeout))
  const timeoutOver = timeoutMs > AGENT_LIMITS.timeout.capMs
    ? `Longer than the ${AGENT_LIMITS.timeout.cap} ceiling; the engine stops the run at ${AGENT_LIMITS.timeout.cap}.`
    : undefined

  const saveTools = (next: AgentTool[]) => set({ tools: next })
  const updateTool = (i: number, t: AgentTool) => saveTools(tools.map((x, j) => (j === i ? t : x)))
  const removeTool = (i: number) => saveTools(tools.filter((_, j) => j !== i))

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="grape" variant="light">
        <Text size="sm">
          The model works towards the goal in steps, calling only the tools you list here. A tool that writes pauses the
          run until a person approves the call on the Approvals page; the record then continues from where it stopped.
        </Text>
      </Alert>

      <AIConnectionSection
        config={config}
        nodeId={nodeId}
        updateNodeConfig={updateNodeConfig}
        timeoutHint={{
          placeholder: AGENT_LIMITS.timeout.default,
          description: `The whole run, every step and tool call included. At most ${AGENT_LIMITS.timeout.cap}.`,
          error: timeoutOver,
        }}
      />

      <Divider label="Goal" labelPosition="center" />
      <Textarea
        label="Goal"
        placeholder="e.g. Find the customer who sent this ticket, check their open orders, and open a CRM case if an order is late."
        value={goal}
        onChange={(e) => set({ goal: e.currentTarget.value, ...(config.prompt ? { prompt: '' } : {}) })}
        autosize
        minRows={3}
        required
        error={fieldError('goal')}
        description="What the agent should achieve for each record. The record is given to the model as data, never as instructions."
      />
      <Textarea
        label="System instructions"
        placeholder="e.g. Be brief. Never open more than one case per ticket."
        value={str(config.system)}
        onChange={(e) => set({ system: e.currentTarget.value })}
        autosize
        minRows={2}
        description="Optional. Rules the agent follows on every step."
      />

      <Divider label="Tools" labelPosition="center" />
      {(toolsError || tools.length === 0) && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />}>
          <Text size="sm">{fieldError('tools') ?? toolsError ?? 'The agent needs at least one tool.'}</Text>
        </Alert>
      )}
      <Stack gap="sm">
        {tools.map((t, i) => (
          <AgentToolCard
            key={i}
            index={i}
            tool={t}
            issues={issues.filter((x) => x.toolIndex === i)}
            sinks={sinks}
            onChange={(next) => updateTool(i, next)}
            onRemove={() => removeTool(i)}
          />
        ))}
      </Stack>
      <Button variant="light" leftSection={<IconPlus size="1rem" />} onClick={() => saveTools([...tools, { ...NEW_TOOL }])} w="fit-content">
        Add tool
      </Button>

      <Divider label="Limits" labelPosition="center" />
      <Group grow align="flex-start" gap="sm">
        <NumberInput
          label="Max steps"
          placeholder={String(AGENT_LIMITS.maxSteps.default)}
          min={1}
          allowDecimal={false}
          value={str(config.maxSteps)}
          onChange={(v) => set({ maxSteps: asText(v) })}
          error={capNote(config.maxSteps, AGENT_LIMITS.maxSteps.cap) && `Capped at ${AGENT_LIMITS.maxSteps.cap} by the engine.`}
          description={`Model turns before the run fails. At most ${AGENT_LIMITS.maxSteps.cap}.`}
        />
        <NumberInput
          label="Max total tokens"
          placeholder={String(AGENT_LIMITS.maxTotalTokens.default)}
          min={1}
          allowDecimal={false}
          value={str(config.maxTotalTokens)}
          onChange={(v) => set({ maxTotalTokens: asText(v) })}
          error={capNote(config.maxTotalTokens, AGENT_LIMITS.maxTotalTokens.cap)}
          description="Across every step of one record. Max tokens above limits each answer."
        />
      </Group>

      <Divider label="Output" labelPosition="center" />
      <Group grow align="flex-start" gap="sm">
        <TextInput
          label="Answer field"
          placeholder={AGENT_FIELDS.target}
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description="Receives the agent's final answer."
        />
        <TextInput
          label="Transcript field"
          placeholder={AGENT_FIELDS.transcript}
          value={str(config.transcriptField)}
          onChange={(e) => set({ transcriptField: e.currentTarget.value })}
          description="Receives every step, tool call and token count."
        />
      </Group>

      <AIDataSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />
    </Stack>
  )
}
