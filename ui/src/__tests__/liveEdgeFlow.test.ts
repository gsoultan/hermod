import { advanceFlow, advanceFlows, flowBandOf, formatRate } from '@/pages/workflows/WorkflowEditor/live/edgeFlow'

/**
 * The engine reports each edge's message count as a running total. The edge
 * read that total as messages per second, so any workflow that had moved a few
 * thousand messages drew every edge at full speed for good, including after it
 * stopped. A rate is the change in the count over the time between two reads.
 */
describe('edge flow from cumulative counts', () => {
  const T = 1_793_000_000_000

  it('knows nothing about the rate from one reading', () => {
    const first = advanceFlow(undefined, 52_311, T)
    expect(first.band).toBe(0)
    expect(first.rate).toBe(0)
  })

  it('is quiet when a large lifetime total does not move', () => {
    let f = advanceFlow(undefined, 52_311, T)
    f = advanceFlow(f, 52_311, T + 1000)
    expect(f.band).toBe(0)
    expect(f.rate).toBe(0)
  })

  it('is the count delta over the time between reads, however uneven', () => {
    let f = advanceFlow(undefined, 1000, T)
    f = advanceFlow(f, 1150, T + 1000)
    expect(f.rate).toBeCloseTo(150)
    expect(f.band).toBe(3)

    // A five-second gap between frames carrying 500 messages is 100/s, not 500/s.
    let g = advanceFlow(undefined, 0, T)
    g = advanceFlow(g, 500, T + 5000)
    expect(g.rate).toBeCloseTo(100)
  })

  it('starts again from a counter that went backwards, as after an engine restart', () => {
    let f = advanceFlow(undefined, 9000, T)
    f = advanceFlow(f, 9500, T + 1000)
    f = advanceFlow(f, 20, T + 2000)
    expect(f.rate).toBe(0)
    expect(f.band).toBe(0)
    f = advanceFlow(f, 70, T + 3000)
    expect(f.rate).toBeCloseTo(50)
  })

  it('stays moving through a short gap and goes quiet after it', () => {
    let f = advanceFlow(undefined, 0, T)
    f = advanceFlow(f, 20, T + 1000)
    expect(f.band).toBe(2)
    f = advanceFlow(f, 20, T + 2000)
    expect(f.band).toBeGreaterThan(0)
    expect(f.rate).toBeLessThan(20)
    f = advanceFlow(f, 20, T + 4500)
    expect(f.band).toBe(0)
    expect(f.rate).toBe(0)
  })

  it('does not flicker between two speeds when the rate hovers at a band edge', () => {
    let f = advanceFlow(undefined, 0, T)
    f = advanceFlow(f, 120, T + 1000)
    expect(f.band).toBe(3)
    // Just under the floor of band 3, but within the hysteresis margin.
    for (let i = 2; i < 12; i++) f = advanceFlow(f, f.count + 85, T + i * 1000)
    expect(f.band).toBe(3)
    for (let i = 12; i < 30; i++) f = advanceFlow(f, f.count + 40, T + i * 1000)
    expect(f.band).toBe(2)
  })

  it('ignores a repeated frame', () => {
    const a = advanceFlow(advanceFlow(undefined, 0, T), 10, T + 1000)
    expect(advanceFlow(a, 10, T + 1000)).toBe(a)
  })

  it('maps rates to four bands', () => {
    expect(flowBandOf(0.2)).toBe(1)
    expect(flowBandOf(9.9)).toBe(1)
    expect(flowBandOf(10)).toBe(2)
    expect(flowBandOf(100)).toBe(3)
    expect(flowBandOf(1000)).toBe(4)
  })

  it('advances every edge of a frame and keeps an untouched map as it was', () => {
    const prev = advanceFlows({}, { a: 1, b: 5 }, T)
    const next = advanceFlows(prev, { a: 11, b: 5 }, T + 1000)
    expect(next.a.rate).toBeCloseTo(10)
    expect(next.b.band).toBe(0)
    // An edge the frame no longer reports is dropped.
    expect(Object.keys(advanceFlows(next, { a: 11 }, T + 2000))).toEqual(['a'])
    expect(advanceFlows(next, { a: 11, b: 5 }, T + 1000)).toBe(next)
  })

  it('writes a rate the way the chip shows it', () => {
    expect(formatRate(0)).toBe('0/s')
    expect(formatRate(0.42)).toBe('0.4/s')
    expect(formatRate(12.4)).toBe('12/s')
    expect(formatRate(1240)).toBe('1.2k/s')
    expect(formatRate(3_400_000)).toBe('3.4M/s')
  })
})
