import { TextInput, Stack, Textarea, Switch, Divider, Text, Checkbox } from '@mantine/core';
import { IconPlugConnected } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface WebSocketSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The WebSocket sink — a client that dials out and writes frames.
 *
 * Keys match `createSinkBase` case "websocket" plus `buildWSTLSConfig`, which
 * reads the four TLS keys from the same flat config map.
 */
export function WebSocketSinkConfig({ config, updateConfig }: WebSocketSinkConfigProps) {
  return (
    <Stack gap="md">
      <FormRow cols={1}>
        <TextInput
          label="WebSocket URL"
          placeholder="wss://receiver.example.com/in"
          value={config.url || ''}
          onChange={(e) => updateConfig('url', e.currentTarget.value)}
          description="ws:// or wss://. Use wss:// anywhere the connection leaves the host."
          leftSection={<IconPlugConnected size="1rem" />}
          required
        />
      </FormRow>

      <FormRow cols={1}>
        <Textarea
          label="Headers"
          placeholder="Authorization: Bearer token, X-Tenant: acme"
          value={config.headers || ''}
          onChange={(e) => updateConfig('headers', e.currentTarget.value)}
          description="Comma-separated Name: value pairs, sent on the opening handshake."
          autosize
          minRows={2}
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Subprotocols"
          placeholder="hermod.v1, json"
          value={config.subprotocols || ''}
          onChange={(e) => updateConfig('subprotocols', e.currentTarget.value)}
          description="Comma-separated, offered in order during the handshake."
        />
        <TextInput
          label="Connect timeout"
          placeholder="10s"
          value={config.connect_timeout || ''}
          onChange={(e) => updateConfig('connect_timeout', e.currentTarget.value)}
          description="Go duration. Bounds the dial and handshake."
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Write timeout"
          placeholder="10s"
          value={config.write_timeout || ''}
          onChange={(e) => updateConfig('write_timeout', e.currentTarget.value)}
          description="Go duration. Bounds a single frame write."
        />
        <TextInput
          label="Heartbeat interval"
          placeholder="30s"
          value={config.heartbeat_interval || ''}
          onChange={(e) => updateConfig('heartbeat_interval', e.currentTarget.value)}
          description="Go duration between pings. Keeps idle connections off proxy timeouts."
        />
      </FormRow>

      <Switch
        label="Wait for an acknowledgement"
        checked={config.require_ack === 'true'}
        onChange={(e) => updateConfig('require_ack', e.currentTarget.checked ? 'true' : 'false')}
        description="Treat a write as done only once the peer answers. Slower, but a dropped frame is no longer reported as delivered."
      />

      <Divider my="xs" label="TLS" labelPosition="left" />
      <Text size="xs" c="dimmed">
        Only applies to wss:// URLs.
      </Text>

      <FormRow cols={2}>
        <TextInput
          label="Server name (SNI)"
          placeholder="receiver.example.com"
          value={config.server_name || ''}
          onChange={(e) => updateConfig('server_name', e.currentTarget.value)}
          description="Override the name verified against the certificate. Leave blank to use the URL's host."
        />
        <TextInput
          label="Certificate pin (SHA-256)"
          placeholder="base64 SPKI hash"
          value={config.pin_sha256 || ''}
          onChange={(e) => updateConfig('pin_sha256', e.currentTarget.value)}
          description="Pin the peer's public key. Set this rather than skipping verification."
        />
      </FormRow>

      <Textarea
        label="CA certificate (PEM)"
        placeholder="-----BEGIN CERTIFICATE-----"
        value={config.ca_cert_pem || ''}
        onChange={(e) => updateConfig('ca_cert_pem', e.currentTarget.value)}
        description="Trust a private CA instead of the system roots."
        autosize
        minRows={2}
        maxRows={6}
      />

      <Checkbox
        label="Skip certificate verification"
        checked={config.insecure_skip_verify === 'true'}
        onChange={(e) =>
          updateConfig('insecure_skip_verify', e.currentTarget.checked ? 'true' : 'false')
        }
        description="Accepts any certificate, which means accepting anyone in the middle. For a local test only — pin the certificate instead."
      />
    </Stack>
  );
}
