import { ActionIcon, Button, Group, Paper, Stack, Table, Text, ThemeIcon, Title } from '@mantine/core';
import { IconFolder, IconPlus, IconTrash } from '@tabler/icons-react';
import type { Workspace } from '@/types';
import { formatDateTime } from '@/utils/dateUtils';

import type { SettingsController } from './useSettingsController';

/** The "governance" tab of Settings. State lives in useSettingsController. */
export function GovernanceTab({ ctx }: { ctx: SettingsController }) {
  const {
    deleteWSMutation,
    openWSModal,
    workspaces,
  } = ctx;

  return (
    <>
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group justify="space-between" mb="md">
                <Stack gap={0}>
                  <Group gap="xs">
                    <IconFolder size="1.2rem" color="blue" />
                    <Title order={4}>Workspaces</Title>
                  </Group>
                  <Text size="sm" c="dimmed">Organize workflows and secrets into logical containers</Text>
                </Stack>
                <Button size="xs" leftSection={<IconPlus size="1rem" />} onClick={openWSModal}>Create Workspace</Button>
              </Group>

              <Table.ScrollContainer minWidth={700}>
                <Table verticalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Description</Table.Th>
                    <Table.Th>Created</Table.Th>
                    <Table.Th style={{ width: 80 }}>Actions</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {!Array.isArray(workspaces) || workspaces.length === 0 ? (
                    <Table.Tr>
                      <Table.Td colSpan={4}>
                        <Text ta="center" py="xl" c="dimmed">No workspaces created yet</Text>
                      </Table.Td>
                    </Table.Tr>
                  ) : (
                    workspaces.map((ws: Workspace) => (
                      <Table.Tr key={ws.id}>
                        <Table.Td>
                          <Group gap="xs">
                            <ThemeIcon variant="light" color="blue" size="sm">
                              <IconFolder size="0.8rem" />
                            </ThemeIcon>
                            <Text fw={500}>{ws.name}</Text>
                          </Group>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{ws.description || '-'}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{formatDateTime(ws.created_at)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <ActionIcon aria-label="Delete workspace" color="red" variant="subtle" onClick={() => deleteWSMutation.mutate(ws.id)}>
                            <IconTrash size="1rem" />
                          </ActionIcon>
                        </Table.Td>
                      </Table.Tr>
                    ))
                  )}
                </Table.Tbody>
              </Table>
              </Table.ScrollContainer>
            </Paper>
          </Stack>
    </>
  );
}
