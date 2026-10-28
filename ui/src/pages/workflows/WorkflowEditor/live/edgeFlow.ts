/**
 * How fast messages cross an edge, from the running totals the engine reports.
 *
 * The engine counts every message along every edge and sends the totals, with
 * the time it read them, in each status frame. A total says nothing about speed
 * on its own: a rate is the change between two frames over the time between
 * them. Frames arrive unevenly (on engine events, its health tick, and once a
 * second while the editor is open), so the time is the engine's, not a guess.
 */

/** 0 is quiet: nothing crossed lately. 1–4 are the speeds an edge is drawn at. */
export type FlowBand = 0 | 1 | 2 | 3 | 4;

export interface FlowSample {
  /** The running total at `at`. */
  count: number;
  /** When `count` was read, in Unix milliseconds. */
  at: number;
  /** Messages per second, smoothed. 0 while quiet. */
  rate: number;
  band: FlowBand;
  /** When the total last moved; null until it has been seen to. */
  movedAt: number | null;
}

/** How long after the last message an edge still counts as moving. */
export const QUIET_AFTER_MS = 3000;
/** Smoothing time constant: a rate follows a change within a few seconds. */
const SMOOTHING_MS = 3000;
/** The lowest rate of each band; band 1 is any movement at all. */
const BAND_FLOOR = [0, 0, 10, 100, 1000] as const;
/** A band is left only below this share of its floor, so it does not flicker. */
const HYSTERESIS = 0.7;

/** The band a rate falls in, for an edge that is moving. */
export function flowBandOf(rate: number): FlowBand {
  if (rate >= BAND_FLOOR[4]) return 4;
  if (rate >= BAND_FLOOR[3]) return 3;
  if (rate >= BAND_FLOOR[2]) return 2;
  return 1;
}

function bandWithHysteresis(rate: number, prev: FlowBand): FlowBand {
  const raw = flowBandOf(rate);
  if (raw < prev && rate >= BAND_FLOOR[prev] * HYSTERESIS) return prev;
  return raw;
}

function baseline(count: number, at: number): FlowSample {
  return { count, at, rate: 0, band: 0, movedAt: null };
}

/**
 * The flow after one more reading of an edge's total.
 *
 * `quietAfterMs` is how long a total may stand still before the flow reads as
 * quiet; error counts use a longer window than message counts.
 */
export function advanceFlow(
  prev: FlowSample | undefined,
  count: number,
  at: number,
  quietAfterMs: number = QUIET_AFTER_MS,
): FlowSample {
  // A total going backwards is a new counter: the engine restarted.
  if (!prev || count < prev.count) return baseline(count, at);
  const dt = at - prev.at;
  if (dt <= 0) return count === prev.count ? prev : { ...prev, count };

  const delta = count - prev.count;
  const decay = Math.exp(-dt / SMOOTHING_MS);
  if (delta > 0) {
    const instant = (delta * 1000) / dt;
    // From quiet, the first movement is shown at its own rate rather than
    // eased up from zero, which would understate it for seconds.
    const rate = prev.band === 0 ? instant : prev.rate * decay + instant * (1 - decay);
    const band = prev.band === 0 ? flowBandOf(rate) : bandWithHysteresis(rate, prev.band);
    return { count, at, rate, band, movedAt: at };
  }
  if (prev.movedAt !== null && at - prev.movedAt < quietAfterMs) {
    const rate = prev.rate * decay;
    return { count, at, rate, band: bandWithHysteresis(rate, prev.band || 1), movedAt: prev.movedAt };
  }
  if (prev.band === 0 && prev.rate === 0) return { ...prev, at };
  return { count, at, rate: 0, band: 0, movedAt: prev.movedAt };
}

/**
 * Every edge of a frame advanced by one reading. Entries the frame no longer
 * reports are dropped, and when nothing changed the previous map is returned.
 */
export function advanceFlows(
  prev: Record<string, FlowSample>,
  counts: Record<string, number>,
  at: number,
  quietAfterMs: number = QUIET_AFTER_MS,
): Record<string, FlowSample> {
  const keys = Object.keys(counts);
  let changed = keys.length !== Object.keys(prev).length;
  const next: Record<string, FlowSample> = {};
  for (const key of keys) {
    const before = prev[key];
    const after = advanceFlow(before, counts[key], at, quietAfterMs);
    next[key] = after;
    if (after !== before) changed = true;
  }
  return changed ? next : prev;
}

/** A rate as the edge's chip shows it. */
export function formatRate(rate: number): string {
  if (rate <= 0) return '0/s';
  if (rate < 1) return `${rate.toFixed(1)}/s`;
  if (rate < 1000) return `${Math.round(rate)}/s`;
  if (rate < 1_000_000) return `${(rate / 1000).toFixed(1)}k/s`;
  return `${(rate / 1_000_000).toFixed(1)}M/s`;
}
