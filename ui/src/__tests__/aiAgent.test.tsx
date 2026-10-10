import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { AIAgentConfig } from '@/components/workflow/Transformation/configs/ai-agent/AIAgentConfig'
import { NODE_TYPE_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { rendersNodeEditor } from '@/pages/workflows/WorkflowEditor/components/nodeEditorSurfaces'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { AIAgentNode } from '@/pages/workflows/WorkflowEditor/nodes/AIAgentNode'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { guideFor } from '@/lib/transformationGuide'

let saved: Record<string, any> = {}

function Harness({ initial }: { initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  return (
    <AIAgentConfig
      config={config}
      nodeId="a1"
      updateNodeConfig={(_id: string, patch: any) => setConfig((c: any) => (saved = { ...c, ...patch }))}
    />
  )
}

const renderConfig = (initial: Record<string, any>) => {
  saved = initial
  return render(
    <MantineProvider>
      <Harness initial={initial} />
    </MantineProvider>,
  )
}

const initialState = useWorkflowStore.getState()

beforeEach(() => {
  useWorkflowStore.setState({
    nodes: [
      { id: 'a1', type: 'ai_agent', position: { x: 0, y: 0 }, data: { label: 'Agent' } },
      { id: 'crm', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'CRM tickets', type: 'postgres' } },
      { id: 'mail', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'Mailer' } },
      { id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders' } },
    ] as any,
  })
})

afterEach(() => useWorkflowStore.setState(initialState, true))

const tool = (i: number) => within(screen.getByRole('group', { name: `Tool ${i}` }))

describe('AI Agent settings', () => {
  it('is the editor for an ai_agent node', () => {
    expect(NODE_TYPE_CONFIGS.ai_agent).toBe(AIAgentConfig)
    expect(rendersNodeEditor('ai_agent')).toBe(true)
  })

  it('asks for a goal and a tool, and saves the goal under goal', () => {
    renderConfig({ provider: 'anthropic' })
    expect(screen.getByText('The agent needs a goal.')).toBeInTheDocument()
    expect(screen.getByText('The agent needs at least one tool.')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: /^goal/i }), { target: { value: 'Answer the ticket' } })
    expect(saved.goal).toBe('Answer the ticket')
  })

  it('shows a goal saved under the older prompt key and moves it to goal on edit', () => {
    renderConfig({ provider: 'anthropic', prompt: 'Old goal' })
    const goal = screen.getByRole('textbox', { name: /^goal/i })
    expect(goal).toHaveValue('Old goal')
    fireEvent.change(goal, { target: { value: 'New goal' } })
    expect(saved.goal).toBe('New goal')
    expect(saved.prompt).toBe('')
  })

  it('adds a read tool with a name, description and arguments', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', goal: 'g' })
    await user.click(screen.getByRole('button', { name: /add tool/i }))
    fireEvent.change(tool(1).getByRole('textbox', { name: /^name/i }), { target: { value: 'find_customer' } })
    fireEvent.change(tool(1).getByRole('textbox', { name: /^description/i }), {
      target: { value: 'Looks a customer up by email' },
    })
    await user.click(tool(1).getByRole('button', { name: /add argument/i }))
    fireEvent.change(tool(1).getByRole('textbox', { name: /argument 1 name/i }), { target: { value: 'email' } })
    await user.click(tool(1).getByRole('checkbox', { name: /argument 1 required/i }))
    expect(saved.tools).toEqual([
      {
        name: 'find_customer',
        description: 'Looks a customer up by email',
        kind: 'db_lookup',
        parameters: [{ name: 'email', type: 'string', description: '', required: true }],
        config: {},
      },
    ])
  })

  it('saves the fixed config only when it is a JSON object', () => {
    renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'find', kind: 'db_lookup', config: {} }] })
    const box = tool(1).getByRole('textbox', { name: /fixed settings/i })
    fireEvent.change(box, { target: { value: '{"table": ' } })
    expect(tool(1).getByText(/not valid JSON/i)).toBeInTheDocument()
    expect(saved.tools[0].config).toEqual({})
    fireEvent.change(box, { target: { value: '{"table": "customers", "keyField": "email"}' } })
    expect(saved.tools[0].config).toEqual({ table: 'customers', keyField: 'email' })
  })

  it('lists the workflow’s sink nodes for a sink tool, with approval on by default', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'open_ticket', kind: 'sink' }] })
    expect(tool(1).getByText(/Pick the sink node/)).toBeInTheDocument()
    await user.click(tool(1).getByRole('combobox', { name: /sink node/i }))
    expect(screen.getByText('Mailer')).toBeInTheDocument()
    expect(screen.queryByText('Orders')).toBeNull()
    await user.click(screen.getByText('CRM tickets (postgres)'))
    expect(saved.tools[0].nodeId).toBe('crm')
    expect(tool(1).getByRole('switch', { name: /approve each call/i })).toBeChecked()
    expect(tool(1).queryByText(/without anyone approving/i)).toBeNull()
  })

  it('warns when approval is turned off for a write tool, and saves requireApproval false', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'open_ticket', kind: 'sink', nodeId: 'crm' }] })
    await user.click(tool(1).getByRole('switch', { name: /approve each call/i }))
    expect(saved.tools[0].requireApproval).toBe(false)
    expect(tool(1).getByText(/without anyone approving/i)).toBeInTheDocument()
  })

  it('lets a read tool be marked as one that writes, which then needs approval', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'post', kind: 'api_lookup' }] })
    expect(tool(1).queryByRole('switch', { name: /approve each call/i })).toBeNull()
    await user.click(tool(1).getByRole('switch', { name: /changes something/i }))
    expect(saved.tools[0].write).toBe(true)
    expect(tool(1).getByRole('switch', { name: /approve each call/i })).toBeChecked()
  })

  describe('an mcp tool', () => {
    const mcpTool = { name: 'search', kind: 'mcp', server: { url: 'https://mcp.example.com/mcp' }, tool: 'search_docs' }

    it('switches a tool to mcp: server, remote tool, server schema, write unset and approval on', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'search', kind: 'db_lookup', parameters: [], config: {} }] })
      await user.click(tool(1).getByRole('combobox', { name: /^kind/i }))
      await user.click(screen.getByText('Remote MCP server tool (mcp)'))
      expect(saved.tools).toEqual([{ name: 'search', kind: 'mcp', server: { url: '' }, tool: '' }])
      expect(tool(1).getByText(/needs the server's url/)).toBeInTheDocument()
      expect(tool(1).getByText(/needs the name of the remote tool/)).toBeInTheDocument()
      fireEvent.change(tool(1).getByRole('textbox', { name: /server url/i }), {
        target: { value: 'https://mcp.example.com/mcp' },
      })
      fireEvent.change(tool(1).getByRole('textbox', { name: /remote tool/i }), { target: { value: 'search_docs' } })
      expect(saved.tools[0].server).toEqual({ url: 'https://mcp.example.com/mcp' })
      expect(saved.tools[0].tool).toBe('search_docs')
      expect(tool(1).getByRole('checkbox', { name: /server's input schema/i })).toBeChecked()
      expect(tool(1).queryByRole('button', { name: /add argument/i })).toBeNull()
      expect(tool(1).getByRole('radio', { name: /not set/i })).toBeChecked()
      expect(tool(1).getByRole('switch', { name: /approve each call/i })).toBeChecked()
      expect(tool(1).queryByText(/without anyone approving/i)).toBeNull()
    })

    it('leaves a tool switched away from mcp without its server', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ ...mcpTool, write: true }] })
      await user.click(tool(1).getByRole('combobox', { name: /^kind/i }))
      await user.click(screen.getByText('Write to a sink node'))
      expect(saved.tools).toEqual([{ name: 'search', kind: 'sink' }])
    })

    it('says plainly when an mcp tool can run without approval', () => {
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [mcpTool] })
      expect(tool(1).getByText(/only when you mark it read-only here and the server marks it read-only/i)).toBeInTheDocument()
    })

    it('asks whether the tool changes anything: unset, read-only or writes', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [mcpTool] })
      await user.click(tool(1).getByRole('radio', { name: /read-only \(write: false\)/i }))
      expect(saved.tools[0].write).toBe(false)
      expect(tool(1).getByText(/still needs the server's read-only annotation/i)).toBeInTheDocument()
      expect(tool(1).getByRole('switch', { name: /approve each call/i })).toBeChecked()
      await user.click(tool(1).getByRole('radio', { name: /writes \(write: true\)/i }))
      expect(saved.tools[0].write).toBe(true)
      expect(tool(1).queryByText(/still needs the server's read-only annotation/i)).toBeNull()
      await user.click(tool(1).getByRole('radio', { name: /not set/i }))
      expect('write' in saved.tools[0]).toBe(false)
    })

    it('declares its own arguments, or none, instead of the server schema', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [mcpTool] })
      const schema = tool(1).getByRole('checkbox', { name: /server's input schema/i })
      await user.click(schema)
      expect(saved.tools[0].parameters).toEqual([])
      await user.click(tool(1).getByRole('button', { name: /add argument/i }))
      expect(saved.tools[0].parameters).toHaveLength(1)
      await user.click(schema)
      expect('parameters' in saved.tools[0]).toBe(false)
    })

    it('suggests a secret for header values and warns about a typed-in credential', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [mcpTool] })
      await user.click(tool(1).getByRole('button', { name: /add header/i }))
      const value = tool(1).getByRole('textbox', { name: /header 1 value/i })
      expect(value).toHaveAttribute('placeholder', expect.stringContaining('{{secret("NAME")}}'))
      fireEvent.change(tool(1).getByRole('textbox', { name: /header 1 name/i }), { target: { value: 'Authorization' } })
      fireEvent.change(value, { target: { value: 'Bearer abc' } })
      expect(saved.tools[0].server).toEqual({ url: 'https://mcp.example.com/mcp', headers: { Authorization: 'Bearer abc' } })
      expect(tool(1).getByText(/stores header "Authorization" in the workflow/)).toBeInTheDocument()
      fireEvent.change(value, { target: { value: 'Bearer {{secret("MCP_TOKEN")}}' } })
      expect(tool(1).queryByText(/stores header "Authorization"/)).toBeNull()
      await user.click(tool(1).getByRole('button', { name: /remove header 1/i }))
      expect(saved.tools[0].server).toEqual({ url: 'https://mcp.example.com/mcp' })
    })

    it('refuses a header the MCP transport sets and a non-http url', () => {
      renderConfig({
        provider: 'anthropic',
        goal: 'g',
        tools: [{ ...mcpTool, server: { url: 'ftp://mcp.example.com', headers: { 'Mcp-Session-Id': 'x' } } }],
      })
      expect(tool(1).getByText(/needs an http or https server url/)).toBeInTheDocument()
      expect(tool(1).getByText(/"Mcp-Session-Id" is set by the MCP transport/)).toBeInTheDocument()
    })

    it('warns when approval is turned off', async () => {
      const user = userEvent.setup()
      renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ ...mcpTool, write: false }] })
      await user.click(tool(1).getByRole('switch', { name: /approve each call/i }))
      expect(saved.tools[0].requireApproval).toBe(false)
      expect(tool(1).getByText(/without anyone approving/i)).toBeInTheDocument()
    })
  })

  it('removes a tool', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', goal: 'g', tools: [{ name: 'a', kind: 'db_lookup' }, { name: 'b', kind: 'db_lookup' }] })
    await user.click(screen.getByRole('button', { name: /remove tool 1/i }))
    expect(saved.tools).toEqual([{ name: 'b', kind: 'db_lookup' }])
  })

  it('shows the right fixed settings after an earlier tool is removed', async () => {
    const user = userEvent.setup()
    renderConfig({
      provider: 'anthropic',
      goal: 'g',
      tools: [
        { name: 'a', kind: 'db_lookup', config: { table: 'first' } },
        { name: 'b', kind: 'db_lookup', config: { table: 'second' } },
      ],
    })
    await user.click(screen.getByRole('button', { name: /remove tool 1/i }))
    expect(tool(1).getByRole('textbox', { name: /fixed settings/i })).toHaveValue('{\n  "table": "second"\n}')
  })

  it('edits tools that arrived as JSON text and saves them back as a list', () => {
    renderConfig({ provider: 'anthropic', goal: 'g', tools: '[{"name":"a","kind":"db_lookup"}]' })
    fireEvent.change(tool(1).getByRole('textbox', { name: /^name/i }), { target: { value: 'b' } })
    expect(saved.tools).toEqual([{ name: 'b', kind: 'db_lookup' }])
  })

  it('saves the limits as text and says where the engine caps them', () => {
    renderConfig({ provider: 'anthropic', goal: 'g' })
    const steps = screen.getByRole('textbox', { name: /max steps/i })
    expect(steps).toHaveAttribute('placeholder', '5')
    fireEvent.change(steps, { target: { value: '30' } })
    expect(saved.maxSteps).toBe('30')
    expect(screen.getByText(/capped at 20/i)).toBeInTheDocument()
    const tokens = screen.getByRole('textbox', { name: /max total tokens/i })
    expect(tokens).toHaveAttribute('placeholder', '50000')
    fireEvent.change(tokens, { target: { value: '2000' } })
    expect(saved.maxTotalTokens).toBe('2000')
    expect(screen.getByRole('textbox', { name: /answer field/i })).toHaveAttribute('placeholder', 'ai_agent_answer')
    expect(screen.getByRole('textbox', { name: /transcript field/i })).toHaveAttribute('placeholder', 'ai_agent_transcript')
  })
})

const renderNode = (data: Record<string, any>) =>
  render(
    <MantineProvider>
      <ReactFlowProvider>
        <AIAgentNode id="a1" data={{ label: 'Agent', ...data }} selected={false} />
      </ReactFlowProvider>
    </MantineProvider>,
  )

describe('the ai_agent canvas node', () => {
  it('is drawn by AIAgentNode, and has a renderer on the detail page', () => {
    expect(canvasNodeTypes.ai_agent).toBe(AIAgentNode)
    expect(detailNodeTypes.ai_agent).toBeTruthy()
  })

  it('says when it will wait for a person', () => {
    renderNode({ tools: [{ name: 'find', kind: 'db_lookup' }, { name: 'send', kind: 'sink', nodeId: 'crm' }] })
    expect(screen.getAllByText('2 tools').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Waits for approval').length).toBeGreaterThan(0)
  })

  it('flags a write tool that skips approval', () => {
    renderNode({ tools: [{ name: 'send', kind: 'sink', nodeId: 'crm', requireApproval: false }] })
    expect(screen.queryByText('Waits for approval')).toBeNull()
    expect(screen.getAllByText('Writes unreviewed').length).toBeGreaterThan(0)
  })

  it('is described in plain words', () => {
    expect(guideFor('ai_agent', 'ai_agent').what).toMatch(/tools/i)
  })
})
