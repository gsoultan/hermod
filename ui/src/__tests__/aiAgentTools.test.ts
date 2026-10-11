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
      'error [1]: Tool "x" needs a kind: a lookup, an API call, a vector search, a sink or an MCP tool.',
      'error [2]: Tool name "x" is used twice.',
      'error [3]: Tool "p" parameter "id" has type "object"; use string, number, integer or boolean.',
      'error [3]: Tool "p" has a parameter with a missing, invalid or repeated name.',
    ])
  })

  // mcptool.ParseServer (server.go), parseMCP (config.go) and mcpToolIssues
  // (workflow_validation_ai.go).
  describe('an mcp tool', () => {
    const mcp = (extra: Record<string, unknown>) => issues({ ...base, tools: [{ name: 'm', kind: 'mcp', ...extra }] })

    it('needs a server url and the remote tool to call', () => {
      expect(mcp({})).toEqual([
        'error [0]: MCP tool "m" needs the server\'s url (server.url).',
        'error [0]: MCP tool "m" needs the name of the remote tool it calls (tool).',
      ])
      expect(mcp({ server: { url: '  ' }, tool: ' ' })).toEqual([
        'error [0]: MCP tool "m" needs the server\'s url (server.url).',
        'error [0]: MCP tool "m" needs the name of the remote tool it calls (tool).',
      ])
      expect(mcp({ server: { url: 'https://mcp.example.com/mcp' }, tool: 'search' })).toEqual([])
    })

    it('needs an http or https url unless the url is a template', () => {
      for (const url of ['ftp://mcp.example.com', 'file:///etc/passwd', 'mcp.example.com', 'https://']) {
        expect(mcp({ server: { url }, tool: 't' })).toEqual(['error [0]: MCP tool "m" needs an http or https server url.'])
      }
      expect(mcp({ server: { url: 'http://localhost:8080/mcp' }, tool: 't' })).toEqual([])
      expect(mcp({ server: { url: '{{secret("MCP_URL")}}' }, tool: 't' })).toEqual([])
    })

    it('refuses headers the MCP transport sets itself, in any case', () => {
      expect(
        mcp({
          server: { url: 'https://h', headers: { 'mcp-session-id': 'x', Accept: 'y', 'X-Team': 'ops' } },
          tool: 't',
        }),
      ).toEqual([
        'error [0]: Header "Mcp-Session-Id" is set by the MCP transport and cannot be configured.',
        'error [0]: Header "Accept" is set by the MCP transport and cannot be configured.',
      ])
    })

    it('refuses header names and values the engine refuses', () => {
      expect(
        mcp({ server: { url: 'https://h', headers: { 'Bad Name': 'x', 'X-N': 3, 'X-Split': 'a\nb' } }, tool: 't' }),
      ).toEqual([
        'error [0]: Header name "Bad Name" is not valid.',
        'error [0]: Header "X-N" must be text.',
        'error [0]: Header "X-Split" has a value with control characters.',
      ])
    })

    it('warns about a credential header typed into the workflow', () => {
      expect(
        mcp({
          server: {
            url: 'https://h',
            headers: {
              Authorization: 'Bearer abc',
              'Proxy-Authorization': 'Basic x',
              Cookie: 'sid=1',
              'X-Api-Key': 'k',
              'X-Auth-Token': 't',
              'X-Client-Secret': 's',
              'X-Password': 'p',
              'X-Team': 'ops',
              'X-Other-Key': '',
              'X-Key-Ref': 'Bearer {{secret("K")}}',
            },
          },
          tool: 't',
        }),
      ).toEqual(
        ['Authorization', 'Proxy-Authorization', 'Cookie', 'X-Api-Key', 'X-Auth-Token', 'X-Client-Secret', 'X-Password'].map(
          (h) => `warning [0]: MCP tool "m" stores header "${h}" in the workflow. Use {{secret("NAME")}} instead.`,
        ),
      )
    })

    it('warns when approval is turned off, as the server may say it writes', () => {
      expect(mcp({ server: { url: 'https://h' }, tool: 't', write: false, requireApproval: false })).toEqual([
        'warning [0]: Tool "m" can write without anyone approving it.',
      ])
    })
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

  // useRemote (config.go): an mcp tool is spared approval only at run time,
  // when the workflow says write: false and the server marks it read-only,
  // so the editor treats every mcp tool as one that may write.
  it('treats every mcp tool as one that may write', () => {
    expect(isWriteTool({ name: 'a', kind: 'mcp' })).toBe(true)
    expect(isWriteTool({ name: 'a', kind: 'mcp', write: false })).toBe(true)
    expect(needsApproval({ name: 'a', kind: 'mcp', write: false })).toBe(true)
    expect(needsApproval({ name: 'a', kind: 'mcp', requireApproval: false })).toBe(false)
  })

  it('reads Go durations', () => {
    expect(goDurationMs('2m')).toBe(120_000)
    expect(goDurationMs('1m30s')).toBe(90_000)
    expect(goDurationMs('1.5h')).toBe(5_400_000)
    expect(goDurationMs('500ms')).toBe(500)
    expect(goDurationMs('10')).toBeNaN()
    expect(goDurationMs('')).toBeNaN()
  })

  // config.go checkModel: an ml_predict tool names the vhost's model and
  // declares its features as parameters, or every call sends an empty record.
  it('needs an ml_predict tool to name its model and declare its features', () => {
    expect(issues({ ...base, tools: [{ name: 'score', kind: 'ml_predict', config: {}, parameters: [] }] })).toEqual([
      'error [0]: ML tool "score" needs the model it calls (config.model).',
      'error [0]: ML tool "score" needs the model\'s features as its parameters.',
    ])
    expect(
      issues({
        ...base,
        tools: [{ name: 'score', kind: 'ml_predict', config: { model: 'churn' }, parameters: [{ name: 'age', type: 'number' }] }],
      }),
    ).toEqual([])
    expect(isWriteTool({ name: 'score', kind: 'ml_predict' })).toBe(false)
  })
})
