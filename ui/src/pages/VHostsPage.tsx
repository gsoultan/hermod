import { Title, Table, Button, Group, ActionIcon, Paper, Text, Box, Stack } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { IconTrash, IconPlus, IconServer, IconEdit } from '@tabler/icons-react'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'

interface VHost {
  id: string
  name: string
  description: string
}

export function VHostsPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const { data: vhosts } = useSuspenseQuery<VHost[]>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch('/api/vhosts')
      if (!res.ok) throw new Error('Failed to fetch vhosts')
      return res.json()
    }
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/vhosts/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete vhost')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['vhosts'] })
    }
  })

  const rows = vhosts?.map((vhost) => (
    <Table.Tr key={vhost.id}>
      <Table.Td fw={500}>{vhost.name}</Table.Td>
      <Table.Td>{vhost.description || '-'}</Table.Td>
      <Table.Td>
        <Group justify="flex-end">
          <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/vhosts/${vhost.id}/edit` })} radius="md">
            <IconEdit size="1.2rem" stroke={1.5} />
          </ActionIcon>
          <ActionIcon color="red" variant="light" onClick={() => {
            if (confirm('Are you sure you want to delete this vhost?')) {
              deleteMutation.mutate(vhost.id);
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
              <Title order={2} fw={800}>Virtual Hosts</Title>
              <Text size="sm" c="dimmed">
                Virtual Hosts (VHosts) provide logical isolation for your data flows. 
                Use them to separate different environments, projects, or teams within a single Hermod instance.
              </Text>
            </Box>
            <Button leftSection={<IconPlus size="1.2rem" />} onClick={() => navigate({ to: '/vhosts/new' })}>
              Add VHost
            </Button>
          </Group>
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th>Name</Table.Th>
                <Table.Th>Description</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows}
              {vhosts?.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={3} py="xl">
                    <Text c="dimmed" ta="center">No virtual hosts created yet</Text>
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
