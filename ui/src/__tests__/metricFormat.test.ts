import { describe, expect, it } from 'vitest';
import { formatCount, formatLatency, formatPercent, formatUptime } from '@/utils/metricFormat';

// These all render straight into a stat card, and every one of them is fed by
// a number that arrives over the network. A dashboard that prints "NaN%" or
// "Infinity msg/s" reads as broken even when the pipeline behind it is fine,
// so the degenerate inputs matter more here than the ordinary ones.

describe('formatPercent', () => {
  it('renders a 0..1 rate as a percentage', () => {
    expect(formatPercent(0.2)).toBe('20%');
    expect(formatPercent(1)).toBe('100%');
    expect(formatPercent(0)).toBe('0%');
  });

  // A rate below a tenth of a percent is not zero, and saying "0%" next to a
  // non-zero error count is the kind of contradiction that makes someone stop
  // trusting the whole screen.
  it('does not round a small non-zero rate down to zero', () => {
    expect(formatPercent(0.0004)).toBe('<0.1%');
  });

  it('keeps one decimal below 10%', () => {
    expect(formatPercent(0.025)).toBe('2.5%');
  });

  it('survives values that are not finite numbers', () => {
    expect(formatPercent(Number.NaN)).toBe('—');
    expect(formatPercent(Number.POSITIVE_INFINITY)).toBe('—');
    expect(formatPercent(undefined as unknown as number)).toBe('—');
  });
});

describe('formatLatency', () => {
  it('uses milliseconds under a second', () => {
    expect(formatLatency(4.25)).toBe('4.3 ms');
    expect(formatLatency(999)).toBe('999 ms');
  });

  it('switches to seconds once past a second', () => {
    expect(formatLatency(1500)).toBe('1.50 s');
  });

  // An engine that is idle reports no latency at all. "0 ms" would claim it is
  // instantaneous, which is a different and much better-sounding thing.
  it('shows no reading rather than zero', () => {
    expect(formatLatency(0)).toBe('—');
    expect(formatLatency(Number.NaN)).toBe('—');
  });
});

describe('formatCount', () => {
  it('abbreviates large counts', () => {
    expect(formatCount(1500)).toBe('1.5K');
    expect(formatCount(2_400_000)).toBe('2.4M');
  });

  it('leaves small counts alone', () => {
    expect(formatCount(0)).toBe('0');
    expect(formatCount(42)).toBe('42');
  });

  it('survives values that are not finite numbers', () => {
    expect(formatCount(Number.NaN)).toBe('—');
    expect(formatCount(undefined as unknown as number)).toBe('—');
  });
});

describe('formatUptime', () => {
  it('renders the two most significant units', () => {
    expect(formatUptime(45)).toBe('45s');
    expect(formatUptime(90)).toBe('1m 30s');
    expect(formatUptime(3700)).toBe('1h 1m');
    expect(formatUptime(90_000)).toBe('1d 1h');
  });

  it('survives values that are not finite numbers', () => {
    expect(formatUptime(Number.NaN)).toBe('—');
    expect(formatUptime(-1)).toBe('—');
  });
});
