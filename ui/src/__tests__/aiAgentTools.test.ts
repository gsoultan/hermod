import { describe, expect, it } from 'vitest'
import {
  agentIssues,
  goDurationMs,
  isWriteTool,
  needsApproval,
  toolList,
} from '@/components/workflow/Transformation/configs/ai-agent/agentTools'

const sink = { id: 'k', type: 'sink', data: { label: 'CRM' } }
const issues = (cfg: Record<string, unknown>, nodes: any[] = [sink]) =>
  agentIssues(cfg, nodes).map((i) => `${i.severity}${i.toolIndex === undefined ? '' : ` [${i.toolIndex}]`}: ${i.message}`)

const base = { provider: 'ollama', goal: 'g' }

// agentNodeIssues (workflow_validation_ai.go) and parseTools / parseTool
// (nodes/ai/agent/config.go): what validation reports, plus what makes every
// message fail at run time.
describe('ai_agent validation', () => {
  it('needs a provider first, as aiNodeIssues does', () => {
    expect(issues({ goal: 'g' })).toEqual(['error: Choose the AI provider the agent runs on.'])
  })

  it('needs a goal (or the older prompt key) and at least one tool', () => {
    expect(issues({ provider: 'ollama' })).toEqual([
      'error: The agent needs a goal.',
      'error: The agent needs at least one tool.',
    ])
    expect(issues({ provider: 'ollama', prompt: 'g', tools: [{ name: 't', kind: 'db_lookup' }] })).toEqual([])
  })

  it('reads tools given as JSON text', () => {
    expect(issues({ ...base, tools: '[{"name":"t","kind":"sink","nodeId":"k"}]' })).toEqual([])
    expect(issues({ ...base, tools: '[{' })[0]).toMatch(/^error: The tools are not valid JSON/)
  })

  it('needs a sink tool to name a sink node of this workflow', () => {
    expect(issues({ ...base, tools: [{ name: 'send', kind: 'sink', nodeId: 'nope' }] })).toEqual([
      'error [0]: Tool "send" writes to node "nope", which is not a sink node of this workflow.',
    ])
    expect(issues({ ...base, tools: [{ name: 'send', kind: 'sink' }] })).toEqual([
      'error [0]: Pick the sink node tool "send" writes to.',
    ])
    expect(
      issues({ ...base, tools: [{ name: 'send', kind: 'sink', nodeId: 't1' }] }, [{ id: 't1', type: 'transformation' }]),
    ).toEqual(['error [0]: Tool "send" writes to node "t1", which is not a sink node of this workflow.'])
  })

  it('warns about every write tool that skips approval', () => {
    expect(
      issues({
        ...base,
        tools: [
          { name: 'send', kind: 'sink', nodeId: 'k', requireApproval: false },
          { name: 'post', kind: 'api_lookup', write: true, requireApproval: false },
          { name: 'find', kind: 'db_lookup', requireApproval: false },
        ],
      }),
    ).toEqual([
      'warning [0]: Tool "send" can write without anyone approving it.',
      'warning [1]: Tool "post" can write without anyone approving it.',
    ])
  })

  it('reports the tool definitions the engine refuses', () => {
    expect(
      issues({
        ...base,
        tools: [
          { name: 'bad name', kind: 'db_lookup' },
          { name: 'x', kind: 'shell' },
          { name: 'x', kind: 'db_lookup' },
          { name: 'p', kind: 'api_lookup', parameters: [{ name: 'id', type: 'object' }, { name: '' }] },
        ],
      }),
    ).toEqual([
      'error [0]: Tool name "bad name" must be 1-64 letters, digits, _ or -.',
      'error [1]: Tool "x" needs a kind: a lookup, an API call, a vector search or a sink.',
      'error [2]: Tool name "x" is used twice.',
      'error [3]: Tool "p" parameter "id" has type "object"; use string, number, integer or boolean.',
      'error [3]: Tool "p" has a parameter with a missing, invalid or repeated name.',
    ])
  })

  it('warns about an API key typed into the node', () => {
    expect(issues({ provider: 'openai', apiKey: 'sk-1', goal: 'g', tools: [{ name: 't', kind: 'db_lookup' }] })).toEqual([
      'warning: The API key is stored in the workflow. Use a vhost secret instead.',
    ])
  })
})

describe('agent tools', () => {
  it('reads a list or its JSON text', () => {
    expect(toolList([{ name: 'a', kind: 'sink' }])).toEqual({ tools: [{ name: 'a', kind: 'sink' }] })
    expect(toolList('[{"name":"a"}]')).toEqual({ tools: [{ name: 'a' }] })
    expect(toolList('')).toEqual({ tools: [] })
    expect(toolList(undefined)).toEqual({ tools: [] })
    expect(toolList('nope').error).toMatch(/not valid JSON/)
  })

  // config.go: a sink always writes, a read kind only when write is true,
  // and approval is on unless requireApproval is exactly false.
  it('knows which tools write and which wait for a person', () => {
    expect(isWriteTool({ name: 'a', kind: 'sink' })).toBe(true)
    expect(isWriteTool({ name: 'a', kind: 'api_lookup' })).toBe(false)
    expect(isWriteTool({ name: 'a', kind: 'api_lookup', write: true })).toBe(true)
    expect(needsApproval({ name: 'a', kind: 'sink' })).toBe(true)
    expect(needsApproval({ name: 'a', kind: 'sink', requireApproval: false })).toBe(false)
    expect(needsApproval({ name: 'a', kind: 'db_lookup', requireApproval: true })).toBe(false)
  })

  it('reads Go durations', () => {
    expect(goDurationMs('2m')).toBe(120_000)
    expect(goDurationMs('1m30s')).toBe(90_000)
    expect(goDurationMs('1.5h')).toBe(5_400_000)
    expect(goDurationMs('500ms')).toBe(500)
    expect(goDurationMs('10')).toBeNaN()
    expect(goDurationMs('')).toBeNaN()
  })
})
