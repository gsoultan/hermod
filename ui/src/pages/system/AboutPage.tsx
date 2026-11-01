import { Box, Code, Divider, Group, Paper, Stack, Text, ThemeIcon, Title } from '@mantine/core';
import { IconBolt, IconCopyright } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { PageHeader } from '@/components/rows';

/** The copyright line, word for word as the LICENSE file at the repository root states it. */
export const COPYRIGHT_NOTICE = '© 2025 Gembit Soultan Shirazi. All rights reserved.';

/**
 * Who owns Hermod and which build is running. The notice repeats the LICENSE
 * file; the version is whatever the server reports at /api/version, cut to its
 * release tag the same way the account menu does.
 */
export function AboutPage() {
  const { data } = useQuery<{ version?: string }>({
    queryKey: ['version'],
    queryFn: async () => (await apiFetch('/api/version')).json(),
    staleTime: Infinity,
  });
  const build = data?.version || '';
  const release = build.match(/^v?\d+\.\d+\.\d+/)?.[0] || '';

  return (
    <>
      <PageHeader title="About Hermod" lede="Who makes Hermod, and which build you are running." back={{ to: '/settings', label: 'Settings' }} />
      <Stack gap="lg" maw={720}>
        <Paper withBorder radius="md" p="lg">
          <Group gap="md" wrap="nowrap" align="center">
            <Box
              aria-hidden
              w={48}
              h={48}
              style={{
                display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: 12,
                background: 'var(--mantine-color-indigo-6)', color: 'white', flex: 'none',
              }}
            >
              <IconBolt size="1.6rem" stroke={2} />
            </Box>
            <Stack gap={2} style={{ minWidth: 0 }}>
              <Title order={2} size="h3">Hermod</Title>
              <Text c="dimmed" size="sm">Real-time change data capture with a visual pipeline editor.</Text>
            </Stack>
          </Group>
          <Divider my="md" />
          <Stack gap={6}>
            <Group gap="xs" wrap="wrap">
              <Text size="sm" fw={500} w={72}>Version</Text>
              <Text size="sm">{release || (build ? build : 'Unknown')}</Text>
            </Group>
            {build && build !== release && (
              <Group gap="xs" wrap="nowrap" align="flex-start">
                <Text size="sm" fw={500} w={72} style={{ flexShrink: 0 }}>Build</Text>
                <Code style={{ wordBreak: 'break-all' }}>{build}</Code>
              </Group>
            )}
          </Stack>
        </Paper>

        <Paper withBorder radius="md" p="lg" component="section" aria-labelledby="about-copyright">
          <Group gap="sm" mb="sm" wrap="nowrap">
            <ThemeIcon variant="light" radius="xl" size="lg" aria-hidden>
              <IconCopyright size="1.2rem" stroke={1.5} />
            </ThemeIcon>
            <Title order={2} size="h4" id="about-copyright">Copyright</Title>
          </Group>
          <Text fw={600}>{COPYRIGHT_NOTICE}</Text>
          <Text size="sm" mt="sm">
            Hermod, its source code, documentation and associated files are the proprietary and confidential
            property of Gembit Soultan Shirazi. No right or license to use, copy, modify, distribute or sell
            the software is granted except by a separate written agreement signed by the copyright holder.
          </Text>
          <Group gap="xs" mt="md">
            <Text size="sm" c="dimmed">License</Text>
            <Text size="sm" fw={500}>Hermod Proprietary License</Text>
          </Group>
        </Paper>
      </Stack>
    </>
  );
}
