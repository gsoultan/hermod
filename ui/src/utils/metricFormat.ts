/**
 * Formatting for the dashboard's stat cards.
 *
 * Every value here arrives over the network, which means every one of them can
 * be missing, NaN, or infinite before it ever reaches a card. A monitoring
 * screen that prints "NaN%" has told the reader nothing except that it cannot
 * be trusted, so each of these funnels anything non-finite into a single
 * em-dash meaning "no reading" — which is a real and different answer from
 * zero.
 */

/** What a card shows when there is no reading to show. */
export const NO_READING = '—';

function isReadable(value: number): boolean {
  return typeof value === 'number' && Number.isFinite(value);
}

/**
 * Renders a 0..1 rate as a percentage.
 *
 * Anything above zero but below a tenth of a percent renders as "<0.1%" rather
 * than rounding to "0%". A dashboard showing "0% errors" beside a non-zero
 * error count is the kind of contradiction that costs the whole screen its
 * credibility.
 */
export function formatPercent(rate: number): string {
  if (!isReadable(rate)) return NO_READING;

  const pct = rate * 100;
  if (pct === 0) return '0%';
  if (pct > 0 && pct < 0.1) return '<0.1%';
  if (pct < 10) return `${pct.toFixed(1)}%`;
  return `${Math.round(pct)}%`;
}

/**
 * Renders a millisecond latency.
 *
 * Zero means "no engine reported a latency", not "instantaneous", so it shows
 * as no reading. Claiming a pipeline that is not running has zero latency is
 * the most flattering possible lie.
 */
export function formatLatency(ms: number): string {
  if (!isReadable(ms) || ms <= 0) return NO_READING;
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 10) return `${ms.toFixed(1)} ms`;
  return `${Math.round(ms)} ms`;
}

/** Abbreviates a counter for a card that has room for about five characters. */
export function formatCount(value: number): string {
  if (!isReadable(value)) return NO_READING;
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (Math.abs(value) >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return value.toLocaleString();
}

/**
 * Renders an uptime in seconds as its two most significant units.
 *
 * Two units is the point: "1d 1h" is the answer to "how long has this been
 * up?", while "1d 1h 3m 12s" makes the reader do the truncation themselves.
 */
export function formatUptime(seconds: number): string {
  if (!isReadable(seconds) || seconds < 0) return NO_READING;

  const total = Math.floor(seconds);
  const days = Math.floor(total / 86_400);
  const hours = Math.floor((total % 86_400) / 3_600);
  const minutes = Math.floor((total % 3_600) / 60);
  const secs = total % 60;

  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${secs}s`;
  return `${secs}s`;
}
