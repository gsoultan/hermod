import { IconEdit, IconPlus, IconSearch, IconServer, IconTrash } from '@tabler/icons-react';
import { useState } from 'react'
import { Title, Table, Button, Group, Paper, Text, Box, Stack, Badge, TextInput, Pagination, ActionIcon, RingProgress, Tooltip, Center } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'
import type { Worker } from '../types'
export function WorkersPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [activePage, setPage] = useState(1)
  const itemsPerPage = 30

  const { data: workersResponse } = useSuspenseQuery<any>({
    queryKey: ['workers', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`/api/workers?page=${activePage}&limit=${itemsPerPage}&search=${search}`)
      if (!res.ok) throw new Error('Failed to fetch workers')
      return res.json()
    }
  })

  const workers = (workersResponse as any)?.data as Worker[] || []
  const totalItems = (workersResponse as any)?.total as number || 0

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workers/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete worker')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    }
  })

  const totalPages = Math.ceil(totalItems / itemsPerPage)

  const isOnline = (lastSeen?: string) => {
    if (!lastSeen) return false;
    const date = new Date(lastSeen);
    const now = new Date();
    // Consider online if seen in last 2 minutes
    return (now.getTime() - date.getTime()) < 120000;
  };

  const rows = workers.map((worker: Worker) => {
    const online = isOnline(worker.last_seen);
    return (
      <Table.Tr key={worker.id}>
        <Table.Td>
          <Stack gap={0}>
            <Text fw={500}>{worker.name}</Text>
            <Text size="xs" c="dimmed">{worker.id}</Text>
          </Stack>
        </Table.Td>
        <Table.Td>
          <Badge variant="dot" color={online ? 'green' : 'red'}>
            {online ? 'Online' : 'Offline'}
          </Badge>
        </Table.Td>
        <Table.Td>
          {online ? (
            <Group gap="xs">
              <Tooltip label={`CPU Usage: ${Math.round((worker.cpu_usage || 0) * 100)}%`}>
                <RingProgress
                  size={40}
                  thickness={4}
                  roundCaps
                  sections={[{ value: (worker.cpu_usage || 0) * 100, color: (worker.cpu_usage || 0) > 0.8 ? 'red' : 'blue' }]}
                  label={
                    <Center>
                      <Text size="xs" fw={700} style={{ fontSize: '8px' }}>
                        {Math.round((worker.cpu_usage || 0) * 100)}%
                      </Text>
                    </Center>
                  }
                />
              </Tooltip>
              <Text size="xs" c="dimmed">CPU</Text>
            </Group>
          ) : '-'}
        </Table.Td>
        <Table.Td>
          {online ? (
            <Group gap="xs">
              <Tooltip label={`Memory Usage: ${Math.round((worker.memory_usage || 0) * 100)}%`}>
                <RingProgress
                  size={40}
                  thickness={4}
                  roundCaps
                  sections={[{ value: (worker.memory_usage || 0) * 100, color: (worker.memory_usage || 0) > 0.8 ? 'orange' : 'teal' }]}
                  label={
                    <Center>
                      <Text size="xs" fw={700} style={{ fontSize: '8px' }}>
                        {Math.round((worker.memory_usage || 0) * 100)}%
                      </Text>
                    </Center>
                  }
                />
              </Tooltip>
              <Text size="xs" c="dimmed">Mem</Text>
            </Group>
          ) : '-'}
        </Table.Td>
        <Table.Td>
          {worker.host ? (
            <Badge variant="light">
              {worker.host}:{worker.port}
            </Badge>
          ) : '-'}
        </Table.Td>
        <Table.Td>{worker.description || '-'}</Table.Td>
        <Table.Td>
          <Group justify="flex-end">
            <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/workers/${worker.id}/edit` })} radius="md" aria-label="Edit worker">
              <IconEdit size="1.2rem" stroke={1.5} />
            </ActionIcon>
            <ActionIcon color="red" variant="light" onClick={() => {
              if (confirm('Are you sure you want to unregister this worker?')) {
                deleteMutation.mutate(worker.id);
              }
            }} radius="md" aria-label="Unregister worker">
              <IconTrash size="1.2rem" />
            </ActionIcon>
          </Group>
        </Table.Td>
      </Table.Tr>
    );
  })

  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <style>
        {`
          @keyframes fadeIn {
            from { opacity: 0; transform: translateY(10px); }
            to { opacity: 1; transform: translateY(0); }
          }
        `}
      </style>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Stack gap="md">
            <Group gap="sm">
              <IconServer size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>Workers</Title>
                <Text size="sm" c="dimmed">
                  Manage your Hermod workers. Register workers running on different servers to explicitly assign tasks to them.
                </Text>
              </Box>
              <Button leftSection={<IconPlus size="1.2rem" />} onClick={() => navigate({ to: '/workers/new' })}>
                Register Worker
              </Button>
            </Group>
            <TextInput
              placeholder="Search workers by name, ID, host or description..."
              leftSection={<IconSearch size="1rem" stroke={1.5} />}
              value={search}
              onChange={(event) => {
                setSearch(event.currentTarget.value)
                setPage(1)
              }}
              radius="md"
            />
          </Stack>
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th>Name / ID</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>CPU</Table.Th>
                <Table.Th>Memory</Table.Th>
                <Table.Th>Address</Table.Th>
                <Table.Th>Description</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows}
              {workers.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={5} py="xl">
                    <Text c="dimmed" ta="center">{search ? 'No workers match your search' : 'No workers registered yet'}</Text>
                  </Table.Td>
                </Table.Tr>
              )}
            </Table.Tbody>
          </Table>
          {totalPages > 1 && (
            <Group justify="center" p="md" bg="gray.0" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
              <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
            </Group>
          )}
        </Paper>
      </Stack>
    </Box>
  )
}


