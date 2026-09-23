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

/**
 * Byte units, largest first, so the first one a value clears is the one used.
 *
 * Binary (1024) rather than decimal (1000): these numbers come from the
 * operating system, which reports a 32 GiB machine as 34,359,738,368 bytes.
 * Dividing by 1000 would render that as "34.4 GB" next to a sticker that says
 * 32, and the reader would be right to distrust the number rather than the
 * unit.
 */
const BYTE_UNITS: ReadonlyArray<[string, number]> = [
  ['TB', 1024 ** 4],
  ['GB', 1024 ** 3],
  ['MB', 1024 ** 2],
  ['KB', 1024],
];

/** Drops a trailing ".0" so whole numbers read as whole numbers. */
function trim(value: number): string {
  return value.toFixed(1).replace(/\.0$/, '');
}

/**
 * Renders a size in bytes.
 *
 * Zero is no reading rather than "0 B". A worker running a release from before
 * capacity reporting leaves these columns null, and the API sends zero; showing
 * a machine with no memory would be inventing a fact about it. The same is true
 * of a negative value, which can only be a bug upstream.
 */
export function formatBytes(bytes: number): string {
  if (!isReadable(bytes) || bytes <= 0) return NO_READING;

  for (const [unit, size] of BYTE_UNITS) {
    if (bytes >= size) return `${trim(bytes / size)} ${unit}`;
  }
  return `${Math.round(bytes)} B`;
}

/**
 * Renders used against total — "16 / 32 GB".
 *
 * One unit for both sides, chosen from the total, because the pair only reads
 * as a fraction if the two halves are comparable at a glance: "512000 MB / 2 TB"
 * makes the reader do arithmetic to find out whether that is a full disk.
 *
 * A missing total is no reading: without it the used figure has no meaning
 * here, and a bare "16 GB used" in a column headed "Memory" would be read as
 * the machine's size.
 */
export function formatCapacity(used: number, total: number): string {
  if (!isReadable(used) || !isReadable(total) || total <= 0 || used < 0) return NO_READING;

  const [unit, size] = BYTE_UNITS.find(([, s]) => total >= s) ?? ['B', 1];
  return `${trim(used / size)} / ${trim(total / size)} ${unit}`;
}

/**
 * The used share of a total, 0..1, or null when there is nothing to divide by.
 *
 * Null rather than 0 on purpose. These feed progress rings, and a ring drawn at
 * 0% says "measured, and empty" — the opposite of "not measured". The caller
 * has to decide what an absent reading looks like, which is the point.
 */
export function usageFraction(used: number, total: number): number | null {
  if (!isReadable(used) || !isReadable(total) || total <= 0 || used < 0) return null;
  return Math.min(1, used / total);
}
