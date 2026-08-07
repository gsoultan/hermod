import { Button, Card, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core';
import { IconBraces, IconCode } from '@tabler/icons-react';

import type { SettingsController } from './useSettingsController';

/** The "developer" tab of Settings. State lives in useSettingsController. */
export function DeveloperTab({ ctx }: { ctx: SettingsController }) {
  const {
    handleGenerateSDK,
  } = ctx;

  return (
    <>
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconCode size="1.2rem" color="blue" />
                <Title order={4}>Client SDK Generation</Title>
              </Group>
              <Text size="sm" c="dimmed" mb="lg">
                Generate lightweight client libraries to easily publish messages to Hermod from your applications.
              </Text>
              
              <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                <Card withBorder padding="lg" radius="md">
                  <Stack align="center" gap="sm">
                    <ThemeIcon size="xl" radius="md" color="blue" variant="light">
                      <IconCode size="1.5rem" />
                    </ThemeIcon>
                    <Text fw={700}>Go Client</Text>
                    <Text size="xs" c="dimmed" ta="center">Native Go library using standard net/http.</Text>
                    <Button variant="light" size="sm" onClick={() => handleGenerateSDK('go')}>Download .go</Button>
                  </Stack>
                </Card>

                <Card withBorder padding="lg" radius="md">
                  <Stack align="center" gap="sm">
                    <ThemeIcon size="xl" radius="md" color="blue" variant="light">
                      <IconBraces size="1.5rem" />
                    </ThemeIcon>
                    <Text fw={700}>TypeScript Client</Text>
                    <Text size="xs" c="dimmed" ta="center">Modern TS client using fetch API.</Text>
                    <Button variant="light" size="sm" onClick={() => handleGenerateSDK('typescript')}>Download .ts</Button>
                  </Stack>
                </Card>
              </SimpleGrid>
            </Paper>
          </Stack>
    </>
  );
}
