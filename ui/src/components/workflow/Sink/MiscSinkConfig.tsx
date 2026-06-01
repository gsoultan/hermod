import { TextInput } from '@mantine/core';

interface MiscSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: any) => void;
}

export function MiscSinkConfig({ type, config, updateConfig }: MiscSinkConfigProps) {
  switch (type) {
    case 'telegram':
      return (
        <>
          <TextInput label="Bot Token" placeholder="123456789:ABCDEF..." value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} required />
          <TextInput label="Chat ID" placeholder="-100123456789" value={config.chat_id || ''} onChange={(e) => updateConfig('chat_id', e.target.value)} required />
        </>
      );
    case 'http':
      return (
        <>
          <TextInput label="URL" placeholder="http://localhost:8080/webhook" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Headers" placeholder="Authorization: Bearer token, X-Custom: value" value={config.headers || ''} onChange={(e) => updateConfig('headers', e.target.value)} />
        </>
      );
    case 'fcm':
      return (
        <TextInput label="Credentials JSON" placeholder="Service account JSON content" value={config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} required />
      );
    case 'file':
        return (
          <TextInput label="Filename" placeholder="/tmp/hermod.log" value={config.filename || ''} onChange={(e) => updateConfig('filename', e.target.value)} required />
        );
    default:
      return null;
  }
}
