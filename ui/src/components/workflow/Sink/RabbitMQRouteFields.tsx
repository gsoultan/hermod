import type { ReactNode } from 'react';
import { Alert, Checkbox, Code, Divider, Radio, SegmentedControl, Select, Stack, Text, Textarea } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';

/**
 * Where the RabbitMQ sink sends a record: straight to a queue, or to an
 * exchange of a type the user picks, optionally with a queue bound to it.
 *
 * Each exchange type routes differently, so the fields shown are only the ones
 * the chosen type uses, and each choice says what it does. The rules mirror
 * pkg/comm/sink/rabbitmq/queue_settings.go; the connection step's gate is in
 * lib/connectorRequirements.ts.
 */

type Config = Record<string, string | undefined>;

interface Props {
  config: Config;
  updateConfig: (key: string, value: string) => void;
}

type ExchangeType = 'direct' | 'topic' | 'fanout' | 'headers';

const EXCHANGE_TYPES: { value: ExchangeType; label: string; description: string }[] = [
  {
    value: 'direct',
    label: 'Direct',
    description: 'Exact match. A queue gets the message when its binding key equals the routing key. Needs a routing key.',
  },
  {
    value: 'topic',
    label: 'Topic',
    description: 'Pattern match on words separated by dots. In a binding key, * matches one word and # matches zero or more. Needs a routing key.',
  },
  {
    value: 'fanout',
    label: 'Fanout',
    description: 'Broadcast. Every queue bound to the exchange gets a copy. No routing key.',
  },
  {
    value: 'headers',
    label: 'Headers',
    description: 'Matches message headers instead of a routing key. Needs at least one header.',
  },
];

/** Every key the exchange mode writes, cleared when the user goes back to a queue. */
const EXCHANGE_KEYS = [
  'exchange',
  'exchange_type',
  'exchange_declare',
  'routing_key',
  'binding_key',
  'headers',
  'binding_headers',
  'headers_match',
];

const trimmed = (v: string | undefined) => (v ?? '').trim();

export function RabbitMQRouteFields({ config, updateConfig }: Props) {
  const usesExchange = trimmed(config.exchange) !== '' || trimmed(config.exchange_type) !== '';
  const type = (trimmed(config.exchange_type).toLowerCase() || 'direct') as ExchangeType;

  const chooseTarget = (target: string) => {
    if (target === 'exchange') {
      if (!usesExchange) updateConfig('exchange_type', 'direct');
      return;
    }
    for (const key of EXCHANGE_KEYS) updateConfig(key, '');
  };

  return (
    <Stack gap="sm" mt="xs">
      <div>
        <Text size="sm" fw={500} mb={4}>
          Send each record to
        </Text>
        <SegmentedControl
          fullWidth
          value={usesExchange ? 'exchange' : 'queue'}
          onChange={chooseTarget}
          data={[
            { value: 'queue', label: 'A queue' },
            { value: 'exchange', label: 'An exchange' },
          ]}
        />
      </div>

      {usesExchange ? (
        <ExchangeFields config={config} updateConfig={updateConfig} type={type} />
      ) : (
        <TextInput
          label="Queue name"
          placeholder="hermod-queue"
          description="Messages go straight to this queue through RabbitMQ's default exchange. Hermod creates it as a durable queue if it does not exist."
          value={config.queue_name || ''}
          onChange={(e) => updateConfig('queue_name', e.target.value)}
          required
        />
      )}
    </Stack>
  );
}

function ExchangeFields({ config, updateConfig, type }: Props & { type: ExchangeType }) {
  const queue = trimmed(config.queue_name);
  const routingKey = trimmed(config.routing_key);
  const routesByKey = type === 'direct' || type === 'topic';
  const templatedKey = routingKey.includes('{{');

  return (
    <>
      <TextInput
        label="Exchange name"
        placeholder="hermod-events"
        value={config.exchange || ''}
        onChange={(e) => updateConfig('exchange', e.target.value)}
        required
      />

      <Radio.Group
        label="Exchange type"
        description="How the exchange decides which queues get a message."
        value={type}
        onChange={(v) => updateConfig('exchange_type', v)}
        required
      >
        <Stack gap="xs" mt="xs">
          {EXCHANGE_TYPES.map((t) => (
            <Radio key={t.value} value={t.value} label={t.label} description={t.description} />
          ))}
        </Stack>
      </Radio.Group>

      <Checkbox
        label="Create the exchange if it does not exist"
        description="Turn this off to use an exchange your RabbitMQ admin manages. Hermod then only checks that it exists. The built-in amq.* exchanges are never created."
        checked={config.exchange_declare !== 'false'}
        onChange={(e) => updateConfig('exchange_declare', e.currentTarget.checked ? 'true' : 'false')}
      />

      {routesByKey && (
        <TextInput
          label="Routing key"
          placeholder={type === 'topic' ? 'orders.created' : 'orders'}
          description={
            type === 'topic'
              ? 'Words separated by dots, sent with every message. Wildcards belong in the binding key, not here. Use {{.table}} or {{.operation}} to set it per record.'
              : 'Sent with every message. Use {{.table}} or {{.operation}} to set it per record.'
          }
          value={config.routing_key || ''}
          onChange={(e) => updateConfig('routing_key', e.target.value)}
          required
        />
      )}

      {type === 'headers' && (
        <>
          <Textarea
            label="Headers"
            placeholder={'region=eu\ntable={{.table}}'}
            description="One key=value per line. Sent with every message; the exchange routes on them."
            autosize
            minRows={2}
            value={config.headers || ''}
            onChange={(e) => updateConfig('headers', e.target.value)}
            required
          />
          <Select
            label="Match when"
            description="How a bound queue's headers are compared with a message's."
            data={[
              { value: 'all', label: 'All of the queue’s headers match (x-match: all)' },
              { value: 'any', label: 'Any one of them matches (x-match: any)' },
            ]}
            value={config.headers_match || 'all'}
            onChange={(v) => updateConfig('headers_match', v || 'all')}
            allowDeselect={false}
          />
        </>
      )}

      <Divider label="Queue (optional)" labelPosition="left" />

      <TextInput
        label="Queue to bind"
        placeholder="Optional, e.g. hermod-queue"
        description="Leave empty when your consumers bind their own queues. When set, Hermod creates it as a durable queue and binds it to the exchange, so messages wait there until they are read."
        value={config.queue_name || ''}
        onChange={(e) => updateConfig('queue_name', e.target.value)}
      />

      {queue !== '' && routesByKey && (
        <TextInput
          label="Binding key"
          placeholder={routingKey && !templatedKey ? routingKey : type === 'topic' ? 'orders.#' : 'orders'}
          description={
            type === 'topic'
              ? 'Which routing keys the queue receives. orders.* matches orders.created; orders.# matches orders and everything under it. Empty uses the routing key above.'
              : 'Which routing key the queue receives. Empty uses the routing key above.'
          }
          value={config.binding_key || ''}
          onChange={(e) => updateConfig('binding_key', e.target.value)}
          required={templatedKey}
          error={
            templatedKey && trimmed(config.binding_key) === ''
              ? 'The routing key changes per record, so say which of them this queue receives.'
              : undefined
          }
        />
      )}

      {queue !== '' && type === 'headers' && (
        <Textarea
          label="Binding headers"
          placeholder="region=eu"
          description="Which headers the queue receives, one key=value per line. Empty uses the headers above."
          autosize
          minRows={2}
          value={config.binding_headers || ''}
          onChange={(e) => updateConfig('binding_headers', e.target.value)}
        />
      )}

      {queue !== '' && type === 'fanout' && (
        <Text size="xs" c="dimmed">
          A fanout exchange binds without a key: this queue gets every message.
        </Text>
      )}

      {queue === '' && (
        <Alert color="yellow" title="No queue is bound by Hermod">
          If nothing is bound to this exchange when a record is sent, RabbitMQ has nowhere to deliver it. Hermod
          then reports the write as failed instead of losing the record. Bind a queue above, or make sure your
          consumers have bound theirs before the workflow starts.
        </Alert>
      )}

      <RouteSummary config={config} type={type} />
    </>
  );
}

/** One sentence that says where a record will go, in the user's own names. */
function RouteSummary({ config, type }: { config: Config; type: ExchangeType }) {
  const exchange = trimmed(config.exchange);
  const queue = trimmed(config.queue_name);
  const routingKey = trimmed(config.routing_key);
  const bindingKey = trimmed(config.binding_key) || routingKey;
  const name = (v: string, placeholder: string) => (v ? <Code>{v}</Code> : <Text span c="dimmed">({placeholder})</Text>);

  let to: ReactNode;
  if (!queue) to = <>, to whichever queues your consumers have bound</>;
  else if (type === 'fanout') to = <>, and queue {name(queue, '')} gets a copy of every record</>;
  else if (type === 'headers')
    to = <>, and queue {name(queue, '')} gets the records whose headers match {config.headers_match === 'any' ? 'any' : 'all'} of its binding headers</>;
  else
    to = (
      <>
        , and queue {name(queue, '')} gets the records whose routing key {type === 'topic' ? 'matches' : 'is'}{' '}
        {name(bindingKey, 'binding key')}
      </>
    );

  return (
    <Alert color="blue" title="Where each record goes">
      <Text size="sm" data-testid="rabbitmq-route-summary">
        Hermod publishes each record to the {type} exchange {name(exchange, 'exchange name')}
        {(type === 'direct' || type === 'topic') && <> with routing key {name(routingKey, 'routing key')}</>}
        {type === 'headers' && <> with the headers above</>}
        {to}.
      </Text>
    </Alert>
  );
}
