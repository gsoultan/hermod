import { describe, it, expect } from 'vitest';
import { preparePayload, getAllFieldsWithTypes } from '@/utils/transformationUtils';

/**
 * When a source delivers plain strings rather than JSON objects, the sample the
 * editor receives carries the body under "payload". These assert what the
 * transformation panel's left pane does with that sample: the body has to show
 * up as a mappable field, or the queue looks empty to the user.
 *
 * Before non-object payloads were decoded, the sample was {id, metadata} only
 * and the pane listed no data field at all.
 */
describe('available fields for non-object source payloads', () => {
  const paths = (sample: unknown) =>
    getAllFieldsWithTypes(preparePayload(sample)).map((f) => f.path);

  it('lists the body of a plain-string payload as a field', () => {
    const sample = {
      id: 'm1',
      payload: 'hello world',
      metadata: { delivery_tag: '1' },
    };

    const found = getAllFieldsWithTypes(preparePayload(sample));
    const payloadField = found.find((f) => f.path === 'payload');

    expect(payloadField).toBeDefined();
    expect(payloadField?.type).toBe('string');
  });

  it('types a numeric payload as a number and an array payload as an array', () => {
    expect(
      getAllFieldsWithTypes(preparePayload({ id: 'm1', payload: 42 }))
        .find((f) => f.path === 'payload')?.type,
    ).toBe('number');

    expect(
      getAllFieldsWithTypes(preparePayload({ id: 'm1', payload: ['a', 'b'] }))
        .find((f) => f.path === 'payload')?.type,
    ).toBe('array');
  });

  it('still lists object payload fields at the root, unchanged', () => {
    expect(paths({ id: 'm1', customer_name: 'ACME', qty: 3 })).toEqual(
      expect.arrayContaining(['customer_name', 'qty']),
    );
  });

  it('does not explode a bare string sample into per-character fields', () => {
    // preparePayload passes non-objects through; getAllFieldsWithTypes returns
    // nothing for them. The guard matters: spreading a string would list 0..n
    // single-character "fields".
    expect(paths('hello world')).toEqual([]);
  });
});
