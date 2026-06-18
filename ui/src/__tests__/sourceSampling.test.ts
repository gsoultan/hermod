import { describe, it, expect } from 'vitest';
import {
  isNonDestructiveSample,
  validateSourceForSampling,
  humanizeSampleError,
} from '@/components/workflow/Source/sourceSampling';
import type { Source } from '@/types';

function makeSource(partial: Partial<Source>): Source {
  return {
    name: 'src',
    type: 'postgres',
    config: {},
    ...partial,
  } as Source;
}

describe('isNonDestructiveSample', () => {
  it('marks databases and files as non-destructive', () => {
    expect(isNonDestructiveSample('postgres')).toBe(true);
    expect(isNonDestructiveSample('file')).toBe(true);
    expect(isNonDestructiveSample('http')).toBe(true);
  });

  it('does not mark message queues as non-destructive', () => {
    expect(isNonDestructiveSample('kafka')).toBe(false);
    expect(isNonDestructiveSample('rabbitmq_queue')).toBe(false);
  });
});

describe('validateSourceForSampling', () => {
  const cases: Array<{ name: string; source: Source; valid: boolean }> = [
    {
      name: 'missing name is invalid',
      source: makeSource({ name: '', config: { host: 'db' } }),
      valid: false,
    },
    {
      name: 'database with host is valid',
      source: makeSource({ type: 'postgres', config: { host: 'db' } }),
      valid: true,
    },
    {
      name: 'database without connection is invalid',
      source: makeSource({ type: 'postgres', config: {} }),
      valid: false,
    },
    {
      name: 'kafka without broker/topic is invalid',
      source: makeSource({ type: 'kafka', config: {} }),
      valid: false,
    },
    {
      name: 'kafka with broker and topic is valid',
      source: makeSource({ type: 'kafka', config: { brokers: 'localhost:9092', topic: 't' } }),
      valid: true,
    },
    {
      name: 'mqtt with broker_url and topics is valid',
      source: makeSource({ type: 'mqtt', config: { broker_url: 'tcp://localhost:1883', topics: 'sensors/#' } }),
      valid: true,
    },
    {
      name: 'mqtt without broker/topic is invalid',
      source: makeSource({ type: 'mqtt', config: {} }),
      valid: false,
    },
    {
      name: 'local file with local_path is valid',
      source: makeSource({ type: 'file', config: { source_type: 'local', local_path: '/data' } }),
      valid: true,
    },
    {
      name: 'file without location is invalid',
      source: makeSource({ type: 'file', config: {} }),
      valid: false,
    },
    {
      name: 'unknown source type defaults to valid',
      source: makeSource({ type: 'some_new_type', config: {} }),
      valid: true,
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      const res = validateSourceForSampling(tc.source);
      expect(res.valid).toBe(tc.valid);
      if (!tc.valid) {
        expect(res.issues.length).toBeGreaterThan(0);
      }
    });
  }
});

describe('humanizeSampleError', () => {
  it('maps connection errors to actionable guidance', () => {
    expect(humanizeSampleError('dial tcp 127.0.0.1:5432: connect: connection refused')).toMatch(/reach the server/i);
  });

  it('maps auth failures', () => {
    expect(humanizeSampleError('pq: password authentication failed for user "x"')).toMatch(/authentication failed/i);
  });

  it('gives a queue-specific timeout message for messaging sources', () => {
    expect(humanizeSampleError('context deadline exceeded', 'kafka')).toMatch(/no message arrived/i);
  });

  it('gives a generic timeout message for non-messaging sources', () => {
    expect(humanizeSampleError('context deadline exceeded', 'postgres')).toMatch(/timed out/i);
  });

  it('falls back to the original text when no rule matches', () => {
    expect(humanizeSampleError('some weird unmatched error')).toBe('some weird unmatched error');
  });

  it('returns a default for empty input', () => {
    expect(humanizeSampleError('')).toMatch(/failed to fetch sample/i);
  });
});
