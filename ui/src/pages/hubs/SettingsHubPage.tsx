import { Box, Group, Stack, Text, Title, UnstyledButton } from '@mantine/core';
import { IconChevronRight } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import { getSessionRole } from '@/auth/session';
import { settingsRows } from '@/components/layout/nav';

/**
 * Settings as one list. Each row is a place that used to be its own sidebar
 * link or a tab of the platform settings; the row says what is inside so
 * nobody has to open it to find out.
 */
export function SettingsHubPage() {
  const rows = settingsRows(getSessionRole());
  return (
    <Stack gap="md">
      <Box>
        <Title order={1} size="h2">Settings</Title>
        <Text c="dimmed" size="sm">Your workspace, the people in it, and how Hermod behaves.</Text>
      </Box>
      <Box
        component="ul"
        aria-label="Settings"
        m={0}
        p={0}
        style={{ listStyle: 'none', border: '1px solid var(--mantine-color-default-border)', borderRadius: 12, overflow: 'hidden' }}
      >
        {rows.map((row, i) => (
          <Box
            component="li"
            key={row.key}
            style={{ borderTop: i === 0 ? undefined : '1px solid var(--mantine-color-default-border)' }}
          >
          <UnstyledButton
            component={Link}
            to={row.to}
            search={row.search as any}
            px="md"
            py="sm"
            style={{ display: 'block', width: '100%' }}
          >
            <Group justify="space-between" wrap="nowrap">
              <Box style={{ minWidth: 0 }}>
                <Text fw={600}>{row.label}</Text>
                <Text size="sm" c="dimmed" truncate="end">{row.detail}</Text>
              </Box>
              <IconChevronRight size="1rem" style={{ color: 'var(--mantine-color-dimmed)', flex: 'none' }} aria-hidden />
            </Group>
          </UnstyledButton>
          </Box>
        ))}
      </Box>
    </Stack>
  );
}
