import { TextInput, Stack, PasswordInput, Text, SimpleGrid } from '@mantine/core';

interface MessagingSourceConfigProps {
  type: string;
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

export function MessagingSourceConfig({ type, config, updateConfig }: MessagingSourceConfigProps) {
  if (type === 'kafka') {
    return (
      <Stack gap="md">
        <TextInput label="Brokers" placeholder="localhost:9092" value={config.brokers || ''} onChange={(e) => updateConfig('brokers', e.target.value)} required />
        <TextInput label="Topic" placeholder="topic" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
        <TextInput label="Group ID" placeholder="hermod-consumer" value={config.group_id || ''} onChange={(e) => updateConfig('group_id', e.target.value)} />
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Username" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
          <PasswordInput label="Password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
        </SimpleGrid>
      </Stack>
    );
  }

  if (type === 'nats') {
    return (
      <Stack gap="md">
        <TextInput label="NATS URL" placeholder="nats://localhost:4222" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
        <TextInput label="Subject" placeholder="events.>" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Queue Group" placeholder="hermod" value={config.queue || ''} onChange={(e) => updateConfig('queue', e.target.value)} />
          <TextInput label="Durable Name" placeholder="hermod-durable" value={config.durable_name || ''} onChange={(e) => updateConfig('durable_name', e.target.value)} />
        </SimpleGrid>
      </Stack>
    );
  }

  if (type === 'mqtt') {
    return (
      <Stack gap="md">
        <TextInput label="Broker URL" placeholder="tcp://localhost:1883" value={config.broker_url || config.url || ''} onChange={(e) => {
          updateConfig('broker_url', e.target.value);
          updateConfig('url', e.target.value);
        }} required />
        <TextInput label="Topics (comma separated)" placeholder="sensors/+/temp, devices/+/status" value={config.topics || config.topic || ''} onChange={(e) => {
          updateConfig('topics', e.target.value);
          updateConfig('topic', e.target.value);
        }} required />
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Client ID" placeholder="Optional" value={config.client_id || ''} onChange={(e) => updateConfig('client_id', e.target.value)} />
          <TextInput label="QoS" placeholder="0 | 1 | 2" value={config.qos || ''} onChange={(e) => updateConfig('qos', e.target.value)} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Username" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
          <PasswordInput label="Password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Keepalive (e.g., 30s)" placeholder="30s" value={config.keepalive || ''} onChange={(e) => updateConfig('keepalive', e.target.value)} />
          <TextInput label="Clean Session" placeholder="true | false" value={config.clean_session || ''} onChange={(e) => updateConfig('clean_session', e.target.value)} />
        </SimpleGrid>
        <TextInput label="TLS Insecure Skip Verify" placeholder="false" value={config.tls_insecure_skip_verify || ''} onChange={(e) => updateConfig('tls_insecure_skip_verify', e.target.value)} />
      </Stack>
    );
  }

  if (type.startsWith('rabbitmq')) {
    return (
      <Stack gap="md">
        {!config.host && (
           <TextInput label="RabbitMQ URL (Legacy)" placeholder="amqp://guest:guest@localhost:5672/" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required={!config.url} />
          <TextInput label="Port" placeholder={type === 'rabbitmq' ? '5552' : '5672'} value={config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
          <TextInput label="Username" placeholder="guest" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
          <PasswordInput label="Password" placeholder="guest" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          <TextInput label="Virtual Host" placeholder="/" value={config.dbname || ''} onChange={(e) => updateConfig('dbname', e.target.value)} />
        </SimpleGrid>
        {type === 'rabbitmq' ? (
          <>
            <TextInput label="Stream Name" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
            <TextInput label="Consumer Name" value={config.consumer_name || ''} onChange={(e) => updateConfig('consumer_name', e.target.value)} />
          </>
        ) : (
          <TextInput label="Queue Name" value={config.queue_name || ''} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
        )}
      </Stack>
    );
  }

  if (type === 'redis') {
    return (
      <Stack gap="md">
        <TextInput label="Redis Addr" placeholder="localhost:6379" value={config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
        <PasswordInput label="Password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
        <TextInput label="Stream Key" value={config.stream || ''} onChange={(e) => updateConfig('stream', e.target.value)} required />
        <TextInput label="Consumer Group" value={config.group || 'hermod'} onChange={(e) => updateConfig('group', e.target.value)} />
      </Stack>
    );
  }

  return <Text>Unsupported messaging source type</Text>;
}
