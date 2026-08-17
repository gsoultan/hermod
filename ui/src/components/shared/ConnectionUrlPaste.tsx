import { useState } from 'react';
import { TextInput, Text } from '@mantine/core';
import { IconLink, IconCheck } from '@tabler/icons-react';
import { parseConnectionUrl } from '@/lib/connectionUrl';

interface ConnectionUrlPasteProps {
  /** Merges the parsed fields into the connector config. */
  updateConfig: (key: string, value: string) => void;
  /**
   * Which parsed keys to apply. Lets Cassandra-style configs take only what
   * they use, and MongoDB keep the whole `uri`.
   */
  applyKeys?: Array<'host' | 'port' | 'user' | 'password' | 'database' | 'sslmode' | 'uri'>;
}

const DEFAULT_KEYS: NonNullable<ConnectionUrlPasteProps['applyKeys']> = [
  'host',
  'port',
  'user',
  'password',
  'database',
  'sslmode',
];

/**
 * Paste-to-fill for database connections.
 *
 * The user already has a connection URL — in the provider dashboard, an env
 * file, a message from a teammate. Pasting it here fills the individual
 * fields below in one action instead of six copy-pastes, and every filled
 * field stays editable: this is a shortcut, not a different way of storing
 * the connection.
 */
export function ConnectionUrlPaste({ updateConfig, applyKeys = DEFAULT_KEYS }: ConnectionUrlPasteProps) {
  const [value, setValue] = useState('');
  const [applied, setApplied] = useState<number | null>(null);

  const apply = (raw: string) => {
    const parsed = parseConnectionUrl(raw);
    if (!parsed) {
      setApplied(null);
      return;
    }
    let n = 0;
    for (const key of applyKeys) {
      const v = parsed[key];
      if (v !== undefined) {
        updateConfig(key, v);
        n++;
      }
    }
    setApplied(n);
  };

  return (
    <TextInput
      label="Have a connection URL? Paste it"
      placeholder="postgres://user:password@host:5432/database"
      description={
        applied !== null ? (
          <Text span size="xs" c="teal" inherit>
            <IconCheck size="0.8rem" style={{ verticalAlign: '-2px' }} /> Filled {applied} field
            {applied === 1 ? '' : 's'} below — check them and go on.
          </Text>
        ) : (
          'The fields below fill themselves. You can still edit each one.'
        )
      }
      leftSection={<IconLink size="1rem" />}
      value={value}
      onChange={(e) => {
        setValue(e.target.value);
        apply(e.target.value);
      }}
    />
  );
}
