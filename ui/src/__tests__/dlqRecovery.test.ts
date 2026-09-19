import { describe, it, expect } from 'vitest';
import { dlqRecoverySupported } from '../utils/dlqRecovery';

describe('dlqRecoverySupported', () => {
  // The four types the hardcoded list used to advertise but that are not
  // source types. Offering them enabled a checkbox whose workflow then failed
  // to start with "cannot be used as a source for PrioritizeDLQ".
  it('rejects sink types the engine cannot read back', () => {
    const capable = ['postgres', 'kafka', 'mongodb'];
    for (const type of ['elasticsearch', 'kinesis', 'pubsub', 'pulsar']) {
      expect(dlqRecoverySupported({ type }, capable)).toBe(false);
    }
  });

  // The nine that were missing. The checkbox was greyed out and the feature
  // could not be turned on at all for these.
  it('accepts sink types the server reports as drainable', () => {
    const capable = ['http', 'websocket', 'sap', 'mqtt', 'file', 'postgres'];
    for (const type of ['http', 'websocket', 'sap', 'mqtt', 'file']) {
      expect(dlqRecoverySupported({ type }, capable)).toBe(true);
    }
  });

  it('treats no selected sink as nothing to warn about', () => {
    expect(dlqRecoverySupported(null, ['postgres'])).toBe(true);
    expect(dlqRecoverySupported(undefined, ['postgres'])).toBe(true);
  });

  // While the request is in flight the editor must not flash a warning and
  // disable a checkbox that is about to be valid.
  it('is optimistic until the capability list arrives', () => {
    expect(dlqRecoverySupported({ type: 'postgres' }, null)).toBe(true);
    expect(dlqRecoverySupported({ type: 'elasticsearch' }, undefined)).toBe(true);
  });

  // An empty list is an answer, not a pending state: the server replied and
  // said nothing is drainable.
  it('rejects everything when the server reports no capable types', () => {
    expect(dlqRecoverySupported({ type: 'postgres' }, [])).toBe(false);
  });

  it('rejects a sink with no type', () => {
    expect(dlqRecoverySupported({}, ['postgres'])).toBe(false);
  });
});
