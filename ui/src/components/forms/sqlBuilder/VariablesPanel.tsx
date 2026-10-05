import { useMemo, useState } from 'react';
import { ActionIcon, Badge, Box, Group, Stack, Text, TextInput, Tooltip, UnstyledButton, rem } from '@mantine/core';
import { IconPlus, IconSearch } from '@tabler/icons-react';
import { getValByPath } from '@/utils/transformationUtils';
import { jsToSqlType } from './sqlText';

interface VariablesPanelProps {
  /** The {{ }} tokens the statement binds, without their dot. */
  variables: string[];
  availableFields: { path: string; type: string }[];
  sampleMessage?: any;
  engine?: string;
  onInsert: (text: string) => void;
}

const sectionLabel = (text: string) => (
  <Text size="xs" fw={700} c="dimmed" tt="uppercase">{text}</Text>
);

/**
 * What the statement binds, and what it could.
 *
 * The first list is the check: each token with the value the sample message
 * gives it. The second is the palette: every field of that message, one click
 * from being a token.
 */
export function VariablesPanel({ variables, availableFields, sampleMessage, engine, onInsert }: VariablesPanelProps) {
  const [filter, setFilter] = useState('');

  const fields = useMemo(() => {
    const f = filter.trim().toLowerCase();
    if (!f) return availableFields;
    return availableFields.filter((field) => field.path.toLowerCase().includes(f));
  }, [availableFields, filter]);

  return (
    <Stack gap="md">
      <Stack gap="xs">
        {sectionLabel('Detected variables')}
        {variables.length === 0 ? (
          <Text size="xs" c="dimmed">
            No {'{{ }}'} variables in this statement yet. Pick a message field below to bind its value.
          </Text>
        ) : (
          <Box
            p="xs"
            style={{
              background: 'var(--mantine-color-default-hover)',
              borderRadius: rem(4),
            }}
          >
            <Stack gap={6}>
              {variables.map((v) => {
                // Badged on the value the token resolves to, not on
                // its presence in availableFields. Those are different
                // questions: availableFields is built by recursing the
                // sample, so it listed `after.payload` -- which the
                // pipeline bound as NULL -- as Matched, and the one
                // warning that could have caught that pointed the wrong
                // way.
                const val = sampleMessage ? getValByPath(sampleMessage, v) : undefined;
                const exists = val !== undefined && val !== null;
                const displayVal = val !== undefined ? (typeof val === 'object' ? JSON.stringify(val) : String(val)) : 'N/A';

                return (
                  <Stack key={v} gap={2}>
                    <Group justify="space-between" wrap="nowrap">
                      <Text size="xs" style={{ fontFamily: 'monospace' }} fw={600} truncate>
                        {`{{.${v}}}`}
                      </Text>
                      {exists ? (
                        <Badge size="xs" color="green" variant="light">Matched</Badge>
                      ) : (
                        <Tooltip label="This variable is not in the current message context. It will be empty during preview.">
                          <Badge size="xs" color="orange" variant="light" style={{ cursor: 'help' }}>Missing</Badge>
                        </Tooltip>
                      )}
                    </Group>
                    <Text size="xs" c="dimmed" truncate>
                      Value: <span style={{ color: 'var(--mantine-color-blue-6)' }}>{displayVal}</span>
                    </Text>
                  </Stack>
                );
              })}
            </Stack>
          </Box>
        )}
      </Stack>

      <Stack gap="xs">
        {sectionLabel('Message context')}
        {availableFields.length === 0 ? (
          <Text size="xs" c="dimmed">
            No sample message yet, so its fields cannot be listed. Fetch a sample and they appear here.
          </Text>
        ) : (
          <>
            <TextInput
              size="xs"
              aria-label="Filter message fields"
              placeholder="Filter fields..."
              value={filter}
              onChange={(e) => setFilter(e.currentTarget.value)}
              leftSection={<IconSearch size={12} />}
            />
            {fields.length === 0 && (
              <Text size="xs" c="dimmed">No fields match "{filter}"</Text>
            )}
            <Stack gap={2}>
              {fields.map((f) => {
                const sqlType = jsToSqlType(f.type, engine);
                return (
                  <Group key={f.path} gap={4} wrap="nowrap" justify="space-between">
                    <Tooltip label={`Insert {{.${f.path}}}`} position="left" openDelay={400}>
                      <UnstyledButton
                        onClick={() => onInsert(`{{.${f.path}}}`)}
                        style={{ flex: 1, minWidth: 0, padding: '2px 4px', borderRadius: rem(4) }}
                      >
                        <Text size="xs" truncate style={{ fontFamily: 'monospace' }}>{f.path}</Text>
                        <Text size="xs" c="dimmed">{f.type} → {sqlType}</Text>
                      </UnstyledButton>
                    </Tooltip>
                    <Tooltip label={`Insert as CAST(.. AS ${sqlType})`}>
                      <ActionIcon
                        aria-label={`Insert ${f.path} as a CAST expression`}
                        size="sm"
                        variant="subtle"
                        color="orange"
                        onClick={() => onInsert(`CAST({{.${f.path}}} AS ${sqlType})`)}
                      >
                        <IconPlus size={12} />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                );
              })}
            </Stack>
          </>
        )}
      </Stack>
    </Stack>
  );
}
