import type { Node } from '@xyflow/react'
import { advanceFlow } from '@/pages/workflows/WorkflowEditor/live/edgeFlow'
import {
  edgeLiveView,
  isEngineLive,
  liveProblems,
  nodeLiveProblem,
  type LiveInputs,
} from '@/pages/workflows/WorkflowEditor/live/liveHealth'

/**
 * On a live run the canvas has to say, without anyone hunting for it, when a
 * node is failing, stuck, hung or stopped. Each case here is one thing an
 * operator would otherwise only learn from the logs or from a sink that went
 * quiet.
 */
const T = 1_793_000_000_000
const moving = (rate: number) => advanceFlow(advanceFlow(undefined, 0, T - 1000), rate, T)
const still = () => advanceFlow(advanceFlow(undefined, 5, T - 1000), 5, T)

const nodes = [
  { id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders CDC', ref_id: 'src-1' } },
  { id: 'lookup', type: 'transformation', position: { x: 0, y: 0 }, data: { label: 'Customer lookup' } },
  { id: 'eu', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'Kafka EU', ref_id: 'snk-eu' } },
  { id: 'us', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'Kafka US', ref_id: 'snk-us' } },
] as Node[]

const base = (over: Partial<LiveInputs> = {}): LiveInputs => ({
  nodes,
  workflowStatus: 'running',
  statusFeedStale: false,
  edgeFlows: {},
  nodeErrorFlows: {},
  nodeBusy: {},
  nodeHeld: {},
  nodeLatencies: {},
  sourceStatus: 'running',
  sinkStatuses: { 'snk-eu': 'running', 'snk-us': 'running' },
  sinkCBStatuses: { 'snk-eu': 'closed', 'snk-us': 'closed' },
  sinkBufferFill: { 'snk-eu': 0.05, 'snk-us': 0.05 },
  ...over,
})

describe('what an edge shows on a live run', () => {
  const toEU = { id: 'e-eu', target: 'eu' }

  it('does not move while the workflow is not running', () => {
    for (const status of ['Stopped', 'stopped', 'Error: boom', '', 'paused']) {
      expect(isEngineLive(status), status).toBe(false)
      expect(edgeLiveView(base({ workflowStatus: status, edgeFlows: { 'e-eu': moving(500) } }), toEU).state).toBe('idle')
    }
    expect(isEngineLive('reconnecting:sink:snk-us')).toBe(true)
    expect(isEngineLive('stalled')).toBe(true)
  })

  it('moves at the speed of its own measured rate, and says the rate', () => {
    const view = edgeLiveView(base({ edgeFlows: { 'e-eu': moving(142) } }), toEU)
    expect(view).toMatchObject({ state: 'flowing', band: 3, label: '142/s' })
  })

  it('is quiet, not busy, when nothing crosses it', () => {
    expect(edgeLiveView(base({ edgeFlows: { 'e-eu': still() } }), toEU)).toMatchObject({ state: 'quiet', band: 0, label: '0/s' })
    // An edge with no count at all has carried nothing.
    expect(edgeLiveView(base(), toEU)).toMatchObject({ state: 'quiet', band: 0 })
  })

  it('stops claiming anything once the status feed is lost', () => {
    const view = edgeLiveView(base({ statusFeedStale: true, edgeFlows: { 'e-eu': moving(142) } }), toEU)
    expect(view.state).toBe('stale')
    expect(view.band).toBe(0)
  })

  it('is blocked into a sink whose circuit breaker is open', () => {
    const view = edgeLiveView(base({ sinkCBStatuses: { 'snk-eu': 'open' } }), toEU)
    expect(view).toMatchObject({ state: 'blocked', band: 0 })
    expect(view.label).toMatch(/circuit open/i)
  })

  it('is stuck into a node that has made no progress for too long', () => {
    const view = edgeLiveView(
      base({ nodeBusy: { eu: { in_flight: 3, since_progress_ms: 45_000 } }, edgeFlows: { 'e-eu': still() } }),
      toEU,
    )
    expect(view.state).toBe('hung')
    expect(view.label).toMatch(/stuck 45s/i)
  })

  it('is failing into a node that is producing errors, and keeps moving', () => {
    const view = edgeLiveView(
      base({ edgeFlows: { 'e-eu': moving(40) }, nodeErrorFlows: { eu: moving(3) } }),
      toEU,
    )
    expect(view).toMatchObject({ state: 'failing', band: 2 })
    expect(view.label).toMatch(/3 err\/s/)
  })

  it('is falling behind into a sink whose buffer is filling', () => {
    const view = edgeLiveView(base({ edgeFlows: { 'e-eu': moving(38) }, sinkBufferFill: { 'snk-eu': 0.86 } }), toEU)
    expect(view.state).toBe('backpressure')
    expect(view.label).toBe('38/s · buffer 86%')
  })

  it('is held when the debugger is holding a message at its target', () => {
    const view = edgeLiveView(base({ nodeHeld: { eu: 1 } }), toEU)
    expect(view.state).toBe('held')
    expect(view.label).toMatch(/1 held/)
  })
})

describe('what a node says on a live run', () => {
  it('names a hung node, scaled to how long the node normally takes', () => {
    // An AI call that normally takes 20s is not hung at 45s; a lookup is.
    const lookup = base({ nodeBusy: { lookup: { in_flight: 1, since_progress_ms: 45_000 } }, nodeLatencies: { lookup: 5e6 } })
    expect(nodeLiveProblem(lookup, 'lookup')).toMatchObject({ kind: 'hung', severity: 'error' })
    const ai = base({ nodeBusy: { lookup: { in_flight: 1, since_progress_ms: 45_000 } }, nodeLatencies: { lookup: 20e9 } })
    expect(nodeLiveProblem(ai, 'lookup')).toBeNull()
  })

  it('names a stopped or reconnecting sink and a reconnecting source', () => {
    expect(nodeLiveProblem(base({ sinkStatuses: { 'snk-us': 'reconnecting' } }), 'us')).toMatchObject({ kind: 'reconnecting' })
    expect(nodeLiveProblem(base({ sinkStatuses: { 'snk-us': 'error:circuit_breaker_open' } }), 'us')).toMatchObject({ kind: 'stopped', severity: 'error' })
    expect(nodeLiveProblem(base({ sourceStatus: 'reconnecting' }), 'src')).toMatchObject({ kind: 'reconnecting' })
  })

  it('names a failing node and a full sink buffer', () => {
    expect(nodeLiveProblem(base({ nodeErrorFlows: { lookup: moving(2) } }), 'lookup')).toMatchObject({ kind: 'failing', severity: 'error' })
    expect(nodeLiveProblem(base({ sinkBufferFill: { 'snk-us': 1 } }), 'us')).toMatchObject({ kind: 'stuck', severity: 'error' })
    expect(nodeLiveProblem(base({ sinkBufferFill: { 'snk-us': 0.85 } }), 'us')).toMatchObject({ kind: 'backpressure', severity: 'warning' })
  })

  it('says nothing about a healthy node, or about any node when the workflow is stopped', () => {
    expect(nodeLiveProblem(base(), 'lookup')).toBeNull()
    expect(nodeLiveProblem(base({ workflowStatus: 'Stopped', sinkStatuses: { 'snk-us': 'reconnecting' } }), 'us')).toBeNull()
  })

  it('lists every problem, worst first, with the node it is on', () => {
    const problems = liveProblems(
      base({
        sinkBufferFill: { 'snk-eu': 0.9 },
        nodeBusy: { lookup: { in_flight: 1, since_progress_ms: 120_000 } },
      }),
    )
    expect(problems.map((p) => [p.nodeId, p.kind])).toEqual([
      ['lookup', 'hung'],
      ['eu', 'backpressure'],
    ])
    expect(problems[0].text).toBe('Customer lookup: no progress for 2m')
  })
})
