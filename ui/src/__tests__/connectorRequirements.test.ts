import { describe, expect, it } from 'vitest';
import {
  missingConnectionFields,
  missingConnectionFieldsWithUri,
} from '../lib/connectorRequirements';

describe('missingConnectionFields', () => {
  it('names what a blank postgres connection still needs, in user words', () => {
    expect(missingConnectionFields('source', 'postgres', {})).toEqual(['Host', 'Port']);
  });

  it('empties as fields are filled', () => {
    expect(
      missingConnectionFields('source', 'postgres', { host: 'db', port: '5432' }),
    ).toEqual([]);
  });

  it('treats whitespace as blank — a space is not a host', () => {
    expect(missingConnectionFields('source', 'postgres', { host: '  ' })).toContain('Host');
  });

  it('requires nothing for an unlisted type, degrading to the old behaviour', () => {
    expect(missingConnectionFields('source', 'somefuturetype', {})).toEqual([]);
  });

  it('knows sinks differ from sources — kafka sink needs a topic', () => {
    expect(missingConnectionFields('sink', 'kafka', { brokers: 'b:9092' })).toEqual(['Topic']);
  });
});

describe('missingConnectionFieldsWithUri', () => {
  it('lets a full URI stand in for individual fields', () => {
    expect(
      missingConnectionFieldsWithUri('source', 'mongodb', { uri: 'mongodb://u:p@h/app' }),
    ).toEqual([]);
    expect(missingConnectionFieldsWithUri('source', 'mongodb', {})).toEqual([
      'Database',
      'Collection',
    ]);
  });
});
