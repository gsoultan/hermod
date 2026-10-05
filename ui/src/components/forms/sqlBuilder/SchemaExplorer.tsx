import { useMemo, useState } from 'react';
import { ActionIcon, Alert, Badge, Box, Button, Group, Loader, Stack, Text, TextInput, Tooltip, UnstyledButton, rem } from '@mantine/core';
import { IconAlertCircle, IconColumns, IconPlus, IconRefresh, IconSearch, IconTable } from '@tabler/icons-react';
import type { SqlColumn } from './templates';

interface SchemaExplorerProps {
  tables: string[];
  loading: boolean;
  error: string | null;
  onLoad: () => void;
  selectedTable: string | null;
  columns: SqlColumn[];
  loadingColumns: boolean;
  onSelectTable: (table: string) => void;
  onInsert: (text: string) => void;
}

/**
 * The database's tables, and the columns of the one that is open.
 *
 * Opening a table is also what the Templates tab reads: its statements are
 * written for that table and those columns.
 */
export function SchemaExplorer({
  tables, loading, error, onLoad, selectedTable, columns, loadingColumns, onSelectTable, onInsert,
}: SchemaExplorerProps) {
  const [filter, setFilter] = useState('');

  const visible = useMemo(() => {
    const f = filter.trim().toLowerCase();
    if (!f) return tables;
    return tables.filter((t) => t.toLowerCase().includes(f));
  }, [tables, filter]);

  return (
    <Stack gap="xs">
      <Group justify="space-between" wrap="nowrap">
        <Text size="xs" fw={700} c="dimmed" tt="uppercase">Database explorer</Text>
        <Tooltip label="Reload table list">
          <ActionIcon aria-label="Reload table list" variant="subtle" size="sm" onClick={onLoad} loading={loading}>
            <IconRefresh size={14} />
          </ActionIcon>
        </Tooltip>
      </Group>

      {error && (
        <Alert icon={<IconAlertCircle size={16} />} color="red" variant="light" p="xs" title="Could not read the schema">
          <Text size="xs">{error}</Text>
        </Alert>
      )}

      {tables.length > 0 && (
        <TextInput
          size="xs"
          aria-label="Filter tables"
          placeholder="Filter tables..."
          value={filter}
          onChange={(e) => setFilter(e.currentTarget.value)}
          leftSection={<IconSearch size={12} />}
        />
      )}

      {tables.length === 0 && !loading && (
        <Box py="md" ta="center">
          <Button size="xs" variant="light" onClick={onLoad}>Load Tables</Button>
        </Box>
      )}
      {tables.length === 0 && loading && (
        <Group gap="xs" py="md" justify="center">
          <Loader size="xs" />
          <Text size="xs" c="dimmed">Reading tables…</Text>
        </Group>
      )}
      {tables.length > 0 && visible.length === 0 && (
        <Text size="xs" c="dimmed" ta="center" py="md">No tables match "{filter}"</Text>
      )}

      <Stack gap={2}>
        {visible.map((t) => {
          const open = selectedTable === t;
          return (
            <Box key={t}>
              <Group gap={4} wrap="nowrap" justify="space-between">
                <UnstyledButton
                  aria-expanded={open}
                  onClick={() => onSelectTable(t)}
                  style={{ flex: 1, minWidth: 0, padding: '3px 4px', borderRadius: rem(4) }}
                >
                  <Group gap={6} wrap="nowrap">
                    <IconTable size={13} color={open ? 'var(--mantine-color-blue-filled)' : 'var(--mantine-color-dimmed)'} />
                    <Text size="xs" truncate fw={open ? 700 : 400} c={open ? 'blue' : undefined}>{t}</Text>
                  </Group>
                </UnstyledButton>
                <Tooltip label="Insert table name">
                  <ActionIcon aria-label={`Insert table name ${t}`} size="sm" variant="subtle" onClick={() => onInsert(t)}>
                    <IconPlus size={12} />
                  </ActionIcon>
                </Tooltip>
              </Group>

              {open && (
                <Box pl="lg" mt={2} mb={6}>
                  {loadingColumns ? (
                    <Loader size="xs" mt="xs" />
                  ) : columns.length === 0 ? (
                    <Text size="xs" c="dimmed">No columns reported.</Text>
                  ) : (
                    <Stack gap={1}>
                      {columns.map((c) => (
                        <Tooltip key={c.name} label={`Insert ${c.name}`} position="left" openDelay={400}>
                          <UnstyledButton
                            onClick={() => onInsert(c.name)}
                            style={{ padding: '2px 4px', borderRadius: rem(4) }}
                          >
                            <Group gap={6} wrap="nowrap">
                              <IconColumns size={11} color="var(--mantine-color-dimmed)" />
                              <Text size="xs" truncate>{c.name}</Text>
                              {c.type && <Text size="xs" c="dimmed" truncate>{c.type}</Text>}
                              {c.is_pk && <Badge size="xs" variant="light" color="blue">key</Badge>}
                              {c.is_identity && <Badge size="xs" variant="light" color="gray">generated</Badge>}
                            </Group>
                          </UnstyledButton>
                        </Tooltip>
                      ))}
                    </Stack>
                  )}
                </Box>
              )}
            </Box>
          );
        })}
      </Stack>
    </Stack>
  );
}
