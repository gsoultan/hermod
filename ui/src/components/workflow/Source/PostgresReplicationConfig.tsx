import { useCallback, useRef, useState } from 'react';
import { Autocomplete, Loader, SimpleGrid, Stack, Text, Badge, Group, Alert } from '@mantine/core';
import { apiFetch } from '@/api';

interface ReplicationSlot {
  name: string;
  plugin: string;
  slot_type: string;
  database: string;
  active: boolean;
}

interface Publication {
  name: string;
  all_tables: boolean;
  tables: string[];
}

interface PostgresReplicationConfigProps {
  type: string;
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

// PostgresReplicationConfig lets the user pick an existing logical replication
// slot/publication or type a new name to have Hermod create it. It also surfaces
// which publications already cover the configured tables.
export function PostgresReplicationConfig({ type, config, updateConfig }: PostgresReplicationConfigProps) {
  const [slots, setSlots] = useState<ReplicationSlot[]>([]);
  const [publications, setPublications] = useState<Publication[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const discover = useCallback(async () => {
    if (abortRef.current) abortRef.current.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError(null);
    try {
      const res = await apiFetch('/api/sources/discover/replication', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type, config }),
        signal: controller.signal,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to discover replication slots');
      setSlots(data.slots || []);
      setPublications(data.publications || []);
      setLoaded(true);
    } catch (err: any) {
      if (err?.name !== 'AbortError') {
        setError(err.message);
      }
    } finally {
      setLoading(false);
    }
  }, [type, config]);

  const configuredTables = (config.tables || '')
    .split(',')
    .map((t: string) => t.trim())
    .filter(Boolean);

  const slotExists = slots.some((s) => s.name === config.slot_name);
  const pubExists = publications.some((p) => p.name === config.publication_name);

  const matchingPublications = publications.filter(
    (p) => p.all_tables || (configuredTables.length > 0 && configuredTables.some((t: string) => p.tables.includes(t)))
  );

  return (
    <Stack gap="sm">
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <Autocomplete
          label="Slot Name"
          placeholder="hermod_slot"
          data={slots.map((s) => s.name)}
          value={config.slot_name || ''}
          onChange={(val) => updateConfig('slot_name', val)}
          onDropdownOpen={discover}
          rightSection={loading ? <Loader size="xs" /> : null}
          mih={80}
          description={
            config.slot_name && loaded
              ? slotExists
                ? 'Reusing existing slot'
                : 'New slot will be created'
              : 'Select or type slot name'
          }
        />
        <Autocomplete
          label="Publication"
          placeholder="hermod_pub"
          data={publications.map((p) => p.name)}
          value={config.publication_name || ''}
          onChange={(val) => updateConfig('publication_name', val)}
          onDropdownOpen={discover}
          rightSection={loading ? <Loader size="xs" /> : null}
          mih={80}
          description={
            config.publication_name && loaded
              ? pubExists
                ? 'Reusing existing publication'
                : 'New publication will be created'
              : 'Select or type publication'
          }
        />
      </SimpleGrid>

      {error && (
        <Alert color="red" variant="light" title="Discovery failed">
          {error}
        </Alert>
      )}

      {loaded && !error && (
        <Stack gap={4}>
          <Text size="xs" fw={500} c="dimmed">
            Existing replication slots ({slots.length})
          </Text>
          {slots.length === 0 ? (
            <Text size="xs" c="dimmed">No replication slots found.</Text>
          ) : (
            <Group gap="xs">
              {slots.map((s) => (
                <Badge
                  key={s.name}
                  variant={s.name === config.slot_name ? 'filled' : 'light'}
                  color={s.active ? 'green' : 'gray'}
                  style={{ cursor: 'pointer' }}
                  onClick={() => updateConfig('slot_name', s.name)}
                >
                  {s.name}{s.active ? ' (active)' : ''}
                </Badge>
              ))}
            </Group>
          )}

          <Text size="xs" fw={500} c="dimmed" mt="xs">
            Publications {configuredTables.length > 0 ? 'covering your tables' : ''} ({matchingPublications.length}/{publications.length})
          </Text>
          {publications.length === 0 ? (
            <Text size="xs" c="dimmed">No publications found.</Text>
          ) : (
            <Group gap="xs">
              {publications.map((p) => {
                const covers = p.all_tables || (configuredTables.length > 0 && configuredTables.some((t: string) => p.tables.includes(t)));
                return (
                  <Badge
                    key={p.name}
                    variant={p.name === config.publication_name ? 'filled' : 'light'}
                    color={covers ? 'blue' : 'gray'}
                    style={{ cursor: 'pointer' }}
                    onClick={() => updateConfig('publication_name', p.name)}
                  >
                    {p.name}{p.all_tables ? ' (ALL TABLES)' : ` (${p.tables.length})`}
                  </Badge>
                );
              })}
            </Group>
          )}
        </Stack>
      )}
    </Stack>
  );
}
