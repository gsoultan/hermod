import { Title, Table, Button, Group, ActionIcon, Paper, Text, Box, Stack, Badge } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { IconTrash, IconPlus, IconServer, IconEdit } from '@tabler/icons-react'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'

interface Worker {
  id: string
  name: string
  host: string
  port: number
  description: string
}

export function WorkersPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const { data: workers } = useSuspenseQuery<Worker[]>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch('/api/workers')
      if (!res.ok) throw new Error('Failed to fetch workers')
      return res.json()
    }
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workers/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete worker')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    }
  })

  const rows = workers?.map((worker) => (
    <Table.Tr key={worker.id}>
      <Table.Td>
        <Stack gap={0}>
          <Text fw={500}>{worker.name}</Text>
          <Text size="xs" c="dimmed">{worker.id}</Text>
        </Stack>
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
          <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/workers/${worker.id}/edit` })} radius="md">
            <IconEdit size="1.2rem" stroke={1.5} />
          </ActionIcon>
          <ActionIcon color="red" variant="light" onClick={() => {
            if (confirm('Are you sure you want to unregister this worker?')) {
              deleteMutation.mutate(worker.id);
            }
          }} radius="md">
            <IconTrash size="1.2rem" />
          </ActionIcon>
        </Group>
      </Table.Td>
    </Table.Tr>
  ))

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
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th>Name / ID</Table.Th>
                <Table.Th>Address</Table.Th>
                <Table.Th>Description</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows}
              {workers?.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={4} py="xl">
                    <Text c="dimmed" ta="center">No workers registered yet</Text>
                  </Table.Td>
                </Table.Tr>
              )}
            </Table.Tbody>
          </Table>
        </Paper>
      </Stack>
    </Box>
  )
}
