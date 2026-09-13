import { TextInput, PasswordInput, Select, Stack, Switch, Checkbox, Divider } from '@mantine/core';
import { IconAntenna } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface MqttSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The MQTT sink. `sinkmqtt.New` is handed the whole config map, so these keys
 * are read straight out of it — see pkg/comm/sink/mqtt/mqtt.go.
 *
 * `broker_url` is the key the sink prefers; it falls back to `url`, but writing
 * the preferred one keeps the stored config readable.
 */
export function MqttSinkConfig({ config, updateConfig }: MqttSinkConfigProps) {
  const secure = /^(ssl|tls|wss):\/\//.test(String(config.broker_url || config.url || ''));

  return (
    <Stack gap="md">
      <FormRow cols={1}>
        <TextInput
          label="Broker URL"
          placeholder="tcp://broker.example.com:1883"
          value={config.broker_url || config.url || ''}
          onChange={(e) => updateConfig('broker_url', e.currentTarget.value)}
          description="tcp://, ssl://, tls:// or wss://. TLS is configured only for the secure schemes."
          leftSection={<IconAntenna size="1rem" />}
          required
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Topic"
          placeholder="hermod/events"
          value={config.topic || ''}
          onChange={(e) => updateConfig('topic', e.currentTarget.value)}
          description="Every message is published here."
          required
        />
        <TextInput
          label="Client ID"
          placeholder="hermod-sink-1"
          value={config.client_id || ''}
          onChange={(e) => updateConfig('client_id', e.currentTarget.value)}
          description="Leave blank and the broker assigns one. Two connections sharing an ID evict each other."
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Username"
          value={config.username || ''}
          onChange={(e) => updateConfig('username', e.currentTarget.value)}
          description="A password is only sent when a username is set."
        />
        <PasswordInput
          label="Password"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.currentTarget.value)}
        />
      </FormRow>

      <FormRow cols={3}>
        <Select
          label="QoS"
          value={String(config.qos ?? '')}
          onChange={(value) => updateConfig('qos', value || '')}
          data={[
            { value: '0', label: '0 — at most once' },
            { value: '1', label: '1 — at least once' },
            { value: '2', label: '2 — exactly once' },
          ]}
          description="0 can drop messages."
        />
        <TextInput
          label="Keepalive"
          placeholder="30s"
          value={config.keepalive || ''}
          onChange={(e) => updateConfig('keepalive', e.currentTarget.value)}
          description="Go duration between keepalive pings."
        />
        <Switch
          mt="lg"
          label="Retain"
          checked={config.retain === 'true'}
          onChange={(e) => updateConfig('retain', e.currentTarget.checked ? 'true' : 'false')}
          description="The broker keeps the last message for new subscribers."
        />
      </FormRow>

      <Switch
        label="Clean session"
        checked={config.clean_session !== 'false'}
        onChange={(e) => updateConfig('clean_session', e.currentTarget.checked ? 'true' : 'false')}
        description="Off keeps the broker's subscription and queue state across reconnects, which needs a stable client ID."
      />

      {secure && (
        <>
          <Divider my="xs" label="TLS" labelPosition="left" />
          <Checkbox
            label="Skip certificate verification"
            checked={config.tls_insecure_skip_verify === 'true'}
            onChange={(e) =>
              updateConfig('tls_insecure_skip_verify', e.currentTarget.checked ? 'true' : 'false')
            }
            description="Accepts any certificate, which means accepting anyone in the middle. For a local test only."
          />
        </>
      )}
    </Stack>
  );
}
