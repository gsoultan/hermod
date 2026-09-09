import { describe, expect, it } from 'vitest';
import { missingConnectionFields } from '../lib/connectorRequirements';

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

describe('a pasted whole connection string', () => {
  it('stands in for the host fields it actually replaces', () => {
    expect(
      missingConnectionFields('source', 'postgres', { uri: 'postgres://u:p@h:5432/db' }),
    ).toEqual([]);
    expect(
      missingConnectionFields('source', 'postgres', { connection_string: 'host=h port=5432' }),
    ).toEqual([]);
  });

  // BuildConnectionString substitutes a whole string for host/port only. The
  // factory still reads database and collection from their own keys, so a uri
  // that opened this gate let a user past a step they had not filled in.
  it('does not stand in for fields the factory reads separately', () => {
    expect(
      missingConnectionFields('source', 'mongodb', { uri: 'mongodb://u:p@h/app' }),
    ).toEqual(['Database', 'Collection']);
    expect(missingConnectionFields('source', 'mongodb', {})).toEqual([
      'Database',
      'Collection',
    ]);
  });

  it('does not open an unrelated gate — kafka brokers are not a host field', () => {
    expect(missingConnectionFields('sink', 'kafka', { uri: 'anything' })).toEqual([
      'Brokers',
      'Topic',
    ]);
  });
});

describe('rabbitmq', () => {
  // The gate named `url` and `queue`; the form writes `host`/`port`/... and
  // `queue_name`. Test Connection passed (the factory builds the AMQP URL from
  // the host fields) while Next stayed disabled on fields that do not exist.
  it('lets a host-filled queue source through, the way the form fills it', () => {
    expect(
      missingConnectionFields('source', 'rabbitmq_queue', {
        host: 'localhost',
        port: '5672',
        username: 'guest',
        password: 'guest',
        dbname: '/',
        queue_name: 'orders',
      }),
    ).toEqual([]);
  });

  it('takes a pasted URL in place of the host fields', () => {
    expect(
      missingConnectionFields('source', 'rabbitmq_queue', {
        url: 'amqp://guest:guest@localhost:5672/',
        queue_name: 'orders',
      }),
    ).toEqual([]);
  });

  it('still asks for the queue name — the factory reads it apart from the URL', () => {
    expect(
      missingConnectionFields('source', 'rabbitmq_queue', { host: 'localhost' }),
    ).toEqual(['Queue name']);
    expect(
      missingConnectionFields('source', 'rabbitmq_queue', {
        url: 'amqp://guest:guest@localhost:5672/',
      }),
    ).toEqual(['Queue name']);
  });

  it('asks the stream flavour for a stream name, not a queue', () => {
    expect(missingConnectionFields('source', 'rabbitmq', {})).toEqual([
      'Host',
      'Stream name',
    ]);
    expect(
      missingConnectionFields('source', 'rabbitmq', {
        host: 'localhost',
        stream_name: 'events',
      }),
    ).toEqual([]);
  });

  it('gates both sink flavours on the same fields the sink form writes', () => {
    expect(
      missingConnectionFields('sink', 'rabbitmq', {
        host: 'localhost',
        stream_name: 'events',
      }),
    ).toEqual([]);
    expect(
      missingConnectionFields('sink', 'rabbitmq_queue', {
        host: 'localhost',
        queue_name: 'orders',
      }),
    ).toEqual([]);
    expect(missingConnectionFields('sink', 'rabbitmq_queue', {})).toEqual([
      'Host',
      'Queue name',
    ]);
  });
});

describe('other connectors whose gate had drifted from the form', () => {
  // Same defect as rabbitmq: the gate named a key nobody writes. The form
  // writes broker_url (and mirrors it to url); pkg/comm/source/mqtt reads
  // broker_url then url, and refuses to start without a topic.
  it('gates an mqtt source on the broker and topic keys the form writes', () => {
    expect(
      missingConnectionFields('source', 'mqtt', {
        broker_url: 'tcp://localhost:1883',
        url: 'tcp://localhost:1883',
        topics: 'sensors/+/temp',
      }),
    ).toEqual([]);
    expect(missingConnectionFields('source', 'mqtt', {})).toEqual([
      'Broker URL',
      'Topics',
    ]);
  });

  it('accepts the legacy single-topic key', () => {
    expect(
      missingConnectionFields('source', 'mqtt', {
        url: 'tcp://localhost:1883',
        topic: 'sensors/temp',
      }),
    ).toEqual([]);
  });

  it('asks a snowflake sink for the field its form actually shows', () => {
    expect(missingConnectionFields('sink', 'snowflake', {})).toEqual([
      'Connection String',
    ]);
  });
});
