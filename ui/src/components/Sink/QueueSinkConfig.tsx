import { TextInput, Group } from '@mantine/core';

interface QueueSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: string) => void;
}

export function QueueSinkConfig({ type, config, updateConfig }: QueueSinkConfigProps) {
  switch (type) {
    case 'mqtt':
      return (
        <>
          <TextInput label="Broker URL" placeholder="tcp://localhost:1883" value={config.broker_url || config.url || ''} onChange={(e) => { updateConfig('broker_url', e.target.value); updateConfig('url', e.target.value); }} required />
          <TextInput label="Topic" placeholder="hermod/topic" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
          <Group grow>
            <TextInput label="Client ID" placeholder="Optional" value={config.client_id || ''} onChange={(e) => updateConfig('client_id', e.target.value)} />
            <TextInput label="QoS" placeholder="0 | 1 | 2" value={config.qos || ''} onChange={(e) => updateConfig('qos', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="Username" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="Clean Session" placeholder="true | false" value={config.clean_session || ''} onChange={(e) => updateConfig('clean_session', e.target.value)} />
            <TextInput label="Keepalive (e.g., 30s)" placeholder="30s" value={config.keepalive || ''} onChange={(e) => updateConfig('keepalive', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="Retain" placeholder="false" value={config.retain || ''} onChange={(e) => updateConfig('retain', e.target.value)} />
            <TextInput label="TLS Insecure Skip Verify" placeholder="false" value={config.tls_insecure_skip_verify || ''} onChange={(e) => updateConfig('tls_insecure_skip_verify', e.target.value)} />
          </Group>
        </>
      );
    case 'nats':
      return (
        <>
          <TextInput label="URL" placeholder="nats://localhost:4222" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Subject" placeholder="hermod.data" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
          <Group grow>
            <TextInput label="Username" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <TextInput label="Token" placeholder="Optional" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
        </>
      );
    case 'rabbitmq':
      return (
        <>
          <TextInput label="URL" placeholder="rabbitmq-stream://guest:guest@localhost:5552" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Stream Name" placeholder="hermod-stream" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
        </>
      );
    case 'rabbitmq_queue':
      return (
        <>
          <TextInput label="URL" placeholder="amqp://guest:guest@localhost:5672" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Queue Name" placeholder="hermod-queue" value={config.queue_name || ''} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
        </>
      );
    case 'redis':
      return (
        <>
          <TextInput label="Address" placeholder="localhost:6379" value={config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
          <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          <TextInput label="Stream" placeholder="hermod-stream" value={config.stream || ''} onChange={(e) => updateConfig('stream', e.target.value)} required />
        </>
      );
    case 'kafka':
      return (
        <>
          <TextInput label="Brokers" placeholder="localhost:9092,localhost:9093" value={config.brokers || ''} onChange={(e) => updateConfig('brokers', e.target.value)} required />
          <TextInput label="Topic" placeholder="hermod-topic" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
          <Group grow>
            <TextInput label="Username (SASL)" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password (SASL)" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
        </>
      );
    case 'pulsar':
      return (
        <>
          <TextInput label="URL" placeholder="pulsar://localhost:6650" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Topic" placeholder="persistent://public/default/hermod" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
          <TextInput label="Token" placeholder="Optional" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
        </>
      );
    case 'kinesis':
      return (
        <>
          <TextInput label="Region" placeholder="us-east-1" value={config.region || ''} onChange={(e) => updateConfig('region', e.target.value)} required />
          <TextInput label="Stream Name" placeholder="hermod-stream" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
          <Group grow>
            <TextInput label="Access Key" placeholder="Optional" value={config.access_key || ''} onChange={(e) => updateConfig('access_key', e.target.value)} />
            <TextInput label="Secret Key" type="password" placeholder="Optional" value={config.secret_key || ''} onChange={(e) => updateConfig('secret_key', e.target.value)} />
          </Group>
        </>
      );
    case 'pubsub':
      return (
        <>
          <TextInput label="Project ID" placeholder="my-project" value={config.project_id || ''} onChange={(e) => updateConfig('project_id', e.target.value)} required />
          <TextInput label="Topic ID" placeholder="hermod-topic" value={config.topic_id || ''} onChange={(e) => updateConfig('topic_id', e.target.value)} required />
          <TextInput label="Credentials JSON" placeholder="Optional service account JSON content" value={config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} />
        </>
      );
    default:
      return null;
  }
}
