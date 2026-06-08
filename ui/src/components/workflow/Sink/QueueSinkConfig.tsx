import { TextInput, Group, Checkbox } from '@mantine/core';

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
            <TextInput 
              label="Client ID" 
              placeholder="Optional" 
              value={config.client_id || ''} 
              onChange={(e) => updateConfig('client_id', e.target.value)} 
              description="MQTT client ID"
              mih={80}
            />
            <TextInput 
              label="QoS" 
              placeholder="0 | 1 | 2" 
              value={config.qos || ''} 
              onChange={(e) => updateConfig('qos', e.target.value)} 
              description="Quality of Service"
              mih={80}
            />
          </Group>
          <Group grow>
            <TextInput 
              label="Username" 
              placeholder="Optional" 
              value={config.username || ''} 
              onChange={(e) => updateConfig('username', e.target.value)} 
              description="Broker username"
              mih={80}
            />
            <TextInput 
              label="Password" 
              type="password" 
              placeholder="Optional" 
              value={config.password || ''} 
              onChange={(e) => updateConfig('password', e.target.value)} 
              description="Broker password"
              mih={80}
            />
          </Group>
          <Group grow>
            <TextInput 
              label="Clean Session" 
              placeholder="true | false" 
              value={config.clean_session || ''} 
              onChange={(e) => updateConfig('clean_session', e.target.value)} 
              description="Start fresh session"
              mih={80}
            />
            <TextInput 
              label="Keepalive (e.g., 30s)" 
              placeholder="30s" 
              value={config.keepalive || ''} 
              onChange={(e) => updateConfig('keepalive', e.target.value)} 
              description="Heartbeat interval"
              mih={80}
            />
          </Group>
          <Group grow>
            <TextInput 
              label="Retain" 
              placeholder="false" 
              value={config.retain || ''} 
              onChange={(e) => updateConfig('retain', e.target.value)} 
              description="Retain messages"
              mih={80}
            />
            <TextInput 
              label="TLS Insecure Skip Verify" 
              placeholder="false" 
              value={config.tls_insecure_skip_verify || ''} 
              onChange={(e) => updateConfig('tls_insecure_skip_verify', e.target.value)} 
              description="Skip SSL verification"
              mih={80}
            />
          </Group>
        </>
      );
    case 'nats':
      return (
        <>
          <TextInput label="URL" placeholder="nats://localhost:4222" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Subject" placeholder="hermod.data" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
          <Group grow>
            <TextInput 
              label="Username" 
              placeholder="Optional" 
              value={config.username || ''} 
              onChange={(e) => updateConfig('username', e.target.value)} 
              description="Broker username"
              mih={80}
            />
            <TextInput 
              label="Password" 
              type="password" 
              placeholder="Optional" 
              value={config.password || ''} 
              onChange={(e) => updateConfig('password', e.target.value)} 
              description="Broker password"
              mih={80}
            />
          </Group>
          <TextInput label="Token" placeholder="Optional" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
        </>
      );
    case 'rabbitmq':
      return (
        <>
          {!config.host && (
            <TextInput label="RabbitMQ URL (Legacy)" placeholder="rabbitmq-stream://guest:guest@localhost:5552" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} />
          )}
          <Group grow>
            <TextInput 
              label="Host" 
              placeholder="localhost" 
              value={config.host || ''} 
              onChange={(e) => updateConfig('host', e.target.value)} 
              required={!config.url} 
              description="RabbitMQ server host"
              mih={80}
            />
            <TextInput 
              label="Port" 
              placeholder="5552" 
              value={config.port || ''} 
              onChange={(e) => updateConfig('port', e.target.value)} 
              description="RabbitMQ port"
              mih={80}
            />
          </Group>
          <Group grow>
            <TextInput 
              label="Username" 
              placeholder="guest" 
              value={config.username || ''} 
              onChange={(e) => updateConfig('username', e.target.value)} 
              description="Login username"
              mih={80}
            />
            <TextInput 
              label="Password" 
              type="password" 
              placeholder="guest" 
              value={config.password || ''} 
              onChange={(e) => updateConfig('password', e.target.value)} 
              description="Login password"
              mih={80}
            />
            <TextInput 
              label="Virtual Host" 
              placeholder="/" 
              value={config.dbname || ''} 
              onChange={(e) => updateConfig('dbname', e.target.value)} 
              description="RabbitMQ vhost"
              mih={80}
            />
          </Group>
          <TextInput label="Stream Name" placeholder="hermod-stream" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
          <Checkbox label="Use SSL/TLS" checked={config.use_ssl === 'true'} onChange={(e) => updateConfig('use_ssl', e.currentTarget.checked ? 'true' : 'false')} mt="xs" />
        </>
      );
    case 'rabbitmq_queue':
      return (
        <>
          {!config.host && (
            <TextInput label="RabbitMQ URL (Legacy)" placeholder="amqp://guest:guest@localhost:5672" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} />
          )}
          <Group grow>
            <TextInput 
              label="Host" 
              placeholder="localhost" 
              value={config.host || ''} 
              onChange={(e) => updateConfig('host', e.target.value)} 
              required={!config.url} 
              description="RabbitMQ server host"
              mih={80}
            />
            <TextInput 
              label="Port" 
              placeholder="5672" 
              value={config.port || ''} 
              onChange={(e) => updateConfig('port', e.target.value)} 
              description="RabbitMQ port"
              mih={80}
            />
          </Group>
          <Group grow>
            <TextInput 
              label="Username" 
              placeholder="guest" 
              value={config.username || ''} 
              onChange={(e) => updateConfig('username', e.target.value)} 
              description="Login username"
              mih={80}
            />
            <TextInput 
              label="Password" 
              type="password" 
              placeholder="guest" 
              value={config.password || ''} 
              onChange={(e) => updateConfig('password', e.target.value)} 
              description="Login password"
              mih={80}
            />
            <TextInput 
              label="Virtual Host" 
              placeholder="/" 
              value={config.dbname || ''} 
              onChange={(e) => updateConfig('dbname', e.target.value)} 
              description="RabbitMQ vhost"
              mih={80}
            />
          </Group>
          <TextInput label="Queue Name" placeholder="hermod-queue" value={config.queue_name || ''} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
          <Checkbox label="Use SSL/TLS" checked={config.use_ssl === 'true'} onChange={(e) => updateConfig('use_ssl', e.currentTarget.checked ? 'true' : 'false')} mt="xs" />
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
