import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApprovalsPage } from '@/pages/workflows/ApprovalsPage'
import { WorkflowDebugger } from '@/pages/workflows/WorkflowDebugger'
import {
  findTranscripts,
  pendingToolCall,
} from '@/components/workflow/AgentTranscript/agentTranscript'
import { AgentRunDetails } from '@/components/workflow/AgentTranscript/AgentRunDetails'

// The view transcript.go writes onto the message: status, steps, usage,
// entries (step, kind, text, tool, call_id, input, is_error, stop_reason,
// input_tokens, output_tokens), and truncated / error when set.
const transcript = {
  status: 'awaiting_approval',
  steps: 2,
  usage: { input_tokens: 1200, output_tokens: 340 },
  truncated: true,
  entries: [
    { step: 1, kind: 'model', text: 'I will look the customer up.', stop_reason: 'tool_use', input_tokens: 600, output_tokens: 40 },
    { step: 1, kind: 'tool_call', tool: 'find_customer', call_id: 'c1', input: '{"email":"a@b.c"}' },
    { step: 1, kind: 'tool_result', tool: 'find_customer', call_id: 'c1', text: 'error: no such customer', is_error: true },
    { step: 2, kind: 'tool_call', tool: 'open_ticket', call_id: 'c2', input: '{"subject":"Late order"}' },
    { step: 2, kind: 'approval_requested', tool: 'open_ticket', call_id: 'c2' },
  ],
}

const pending = {
  tool: 'open_ticket',
  description: 'Opens a CRM case',
  call_id: 'c2',
  arguments: { subject: 'Late order' },
}

describe('finding an agent run in message data', () => {
  it('finds transcripts by their shape, under any field name', () => {
    expect(findTranscripts({ ai_agent_transcript: transcript, other: { status: 'x' }, n: 1 })).toEqual([
      { field: 'ai_agent_transcript', transcript },
    ])
    expect(findTranscripts({ custom: { ...transcript, status: 'completed' } }).map((t) => t.field)).toEqual(['custom'])
    expect(findTranscripts(null)).toEqual([])
    expect(findTranscripts({ a: { status: 'completed', entries: 'no' } })).toEqual([])
  })

  it('reads the call an approval is for', () => {
    expect(pendingToolCall({ ai_agent_pending_tool_call: pending })).toEqual(pending)
    expect(pendingToolCall({})).toBeNull()
    expect(pendingToolCall({ ai_agent_pending_tool_call: 'x' })).toBeNull()
  })
})

describe('the agent run view', () => {
  it('shows the call waiting for a decision, with its arguments', () => {
    render(
      <MantineProvider>
        <AgentRunDetails data={{ ai_agent_pending_tool_call: pending, ai_agent_transcript: transcript }} />
      </MantineProvider>,
    )
    expect(screen.getByText(/wants to call/i)).toBeInTheDocument()
    expect(screen.getAllByText('open_ticket').length).toBeGreaterThan(0)
    expect(screen.getByText('Opens a CRM case')).toBeInTheDocument()
    expect(screen.getByText(/"subject": "Late order"/)).toBeInTheDocument()
  })

  it('shows each step of the transcript', () => {
    render(
      <MantineProvider>
        <AgentRunDetails data={{ ai_agent_transcript: transcript }} />
      </MantineProvider>,
    )
    expect(screen.getByText('Awaiting approval')).toBeInTheDocument()
    expect(screen.getByText(/2 steps/)).toBeInTheDocument()
    expect(screen.getByText(/1,200 in/)).toBeInTheDocument()
    expect(screen.getByText('I will look the customer up.')).toBeInTheDocument()
    expect(screen.getByText('{"email":"a@b.c"}')).toBeInTheDocument()
    expect(screen.getByText('error: no such customer')).toBeInTheDocument()
    expect(screen.getByText(/approval requested/i)).toBeInTheDocument()
    expect(screen.getByText(/older steps were dropped/i)).toBeInTheDocument()
  })

  it('shows why a run failed', () => {
    render(
      <MantineProvider>
        <AgentRunDetails data={{ t: { status: 'failed', steps: 5, entries: [], error: 'step limit of 5 reached' } }} />
      </MantineProvider>,
    )
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('step limit of 5 reached')).toBeInTheDocument()
  })

  it('renders nothing for data without an agent run', () => {
    render(
      <MantineProvider>
        <AgentRunDetails data={{ a: 1 }} />
      </MantineProvider>,
    )
    expect(screen.queryByText(/AI agent/i)).toBeNull()
  })
})

describe('where an agent run is shown', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('the Approvals page shows the pending tool call of an AI agent approval', async () => {
    const approval = {
      id: 'ap-123456789',
      workflow_id: 'wf',
      node_id: 'agent',
      message_id: 'm1',
      status: 'pending',
      created_at: '2026-10-01T00:00:00Z',
      data: { ai_agent_pending_tool_call: pending, ai_agent_transcript: transcript },
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        const body = String(url).includes('/api/approvals') ? { data: [approval], total: 1 } : { data: [] }
        return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }),
    )
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ApprovalsPage />
        </QueryClientProvider>
      </MantineProvider>,
    )
    await user.click(await screen.findByText('ap-12345'))
    expect(await screen.findByText(/wants to call a tool/i)).toBeInTheDocument()
    expect(screen.getByText('Awaiting approval')).toBeInTheDocument()
  })

  it('the debugger shows a transcript carried by a debug event', async () => {
    let socket: any
    // jsdom has no scrolling; the debugger scrolls to each new event.
    if (!Element.prototype.scrollTo) Element.prototype.scrollTo = () => {}
    vi.stubGlobal(
      'WebSocket',
      class {
        onopen: any
        onclose: any
        onmessage: any
        constructor() {
          // eslint-disable-next-line @typescript-eslint/no-this-alias
          socket = this
        }
        send() {}
        close() {}
      },
    )
    render(
      <MantineProvider>
        <WorkflowDebugger workflowId="wf" />
      </MantineProvider>,
    )
    act(() => {
      socket.onmessage({
        data: JSON.stringify({
          type: 'node',
          msg_id: 'm1',
          node_id: 'agent',
          timestamp: '2026-10-01T00:00:00Z',
          data: { ai_agent_transcript: { ...transcript, status: 'completed' } },
        }),
      })
    })
    expect(await screen.findByText('AI agent run')).toBeInTheDocument()
    expect(screen.getByText('Completed')).toBeInTheDocument()
  })
})
