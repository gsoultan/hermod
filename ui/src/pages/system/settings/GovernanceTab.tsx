import { ActionIcon, Badge, Button, Group, Paper, Stack, Table, Text, ThemeIcon, Title, Tooltip } from '@mantine/core';
import { IconFolder, IconPencil, IconPlus, IconTrash } from '@tabler/icons-react';
import type { Workspace } from '@/types';
import { formatDateTime } from '@/utils/dateUtils';

import type { SettingsController } from './useSettingsController';

/**
 * A quota of 0 means unlimited everywhere in the API, so rendering the raw
 * number would show four zeroes and read as "nothing allowed".
 */
function Quota({ value, unit }: { value: number | undefined; unit?: string }) {
  if (!value) return <Text size="sm" c="dimmed">∞</Text>;
  return <Text size="sm">{value}{unit ? ` ${unit}` : ''}</Text>;
}

/** The "governance" tab of Settings. State lives in useSettingsController. */
export function GovernanceTab({ ctx }: { ctx: SettingsController }) {
  const {
    requestDeleteWorkspace,
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
                  <Text size="sm" c="dimmed">
                    Group workflows, sources and sinks, and cap what they may consume.
                    Assign one from the Workflows list (Batch Actions &rarr; Move to Workspace).
                  </Text>
                </Stack>
                <Button size="xs" leftSection={<IconPlus size="1rem" />} onClick={() => openWSModal()}>Create Workspace</Button>
              </Group>

              <Table.ScrollContainer minWidth={900}>
                <Table verticalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Description</Table.Th>
                    {/* The quotas were set blind in the create modal and then
                        never shown again, so nobody could tell what a workspace
                        was actually limited to. */}
                    <Table.Th>Workflows</Table.Th>
                    <Table.Th>CPU</Table.Th>
                    <Table.Th>Memory</Table.Th>
                    <Table.Th>Throughput</Table.Th>
                    <Table.Th>Created</Table.Th>
                    <Table.Th style={{ width: 110 }}>Actions</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {!Array.isArray(workspaces) || workspaces.length === 0 ? (
                    <Table.Tr>
                      <Table.Td colSpan={8}>
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
                        <Table.Td><Quota value={ws.max_workflows} /></Table.Td>
                        <Table.Td><Quota value={ws.max_cpu} unit="cores" /></Table.Td>
                        <Table.Td><Quota value={ws.max_memory} unit="MB" /></Table.Td>
                        <Table.Td><Quota value={ws.max_throughput} unit="msg/s" /></Table.Td>
                        <Table.Td>
                          <Text size="sm">{formatDateTime(ws.created_at)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            <Tooltip label="Edit workspace">
                              <ActionIcon aria-label="Edit workspace" variant="subtle" onClick={() => openWSModal(ws)}>
                                <IconPencil size="1rem" />
                              </ActionIcon>
                            </Tooltip>
                            <Tooltip label="Delete workspace">
                              <ActionIcon aria-label="Delete workspace" color="red" variant="subtle" onClick={() => requestDeleteWorkspace(ws)}>
                                <IconTrash size="1rem" />
                              </ActionIcon>
                            </Tooltip>
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    ))
                  )}
                </Table.Tbody>
              </Table>
              </Table.ScrollContainer>
              <Text size="xs" c="dimmed" mt="sm">
                <Badge size="xs" variant="light" color="gray" mr={6}>∞</Badge>
                means no limit. Deleting a workspace moves its members out rather than deleting them.
              </Text>
            </Paper>
          </Stack>
    </>
  );
}
