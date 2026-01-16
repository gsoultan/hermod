import { useState } from 'react'
import { Title, Table, Button, Group, ActionIcon, Box, Paper, Text, Stack, TextInput, Pagination } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { IconTrash, IconUserPlus, IconUsers, IconEdit, IconSearch } from '@tabler/icons-react'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'

export type Role = 'Administrator' | 'Editor' | 'Viewer'

interface User {
  id: string
  username: string
  full_name: string
  email: string
  role: Role
  vhosts: string[]
  password?: string
}

export function UsersPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [activePage, setPage] = useState(1)
  const itemsPerPage = 30

  const { data: usersResponse } = useSuspenseQuery<any>({
    queryKey: ['users', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`/api/users?page=${activePage}&limit=${itemsPerPage}&search=${search}`)
      if (!res.ok) throw new Error('Failed to fetch users')
      return res.json()
    }
  })

  const users = usersResponse?.data || []
  const totalItems = usersResponse?.total || 0

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/users/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete user')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
    }
  })

  const totalPages = Math.ceil(totalItems / itemsPerPage)

  const rows = users.map((user: User) => (
    <Table.Tr key={user.id}>
      <Table.Td>{user.username}</Table.Td>
      <Table.Td>{user.full_name}</Table.Td>
      <Table.Td>{user.email}</Table.Td>
      <Table.Td>{user.role}</Table.Td>
      <Table.Td>{user.vhosts?.join(', ') || '-'}</Table.Td>
      <Table.Td>
        <Group justify="flex-end">
          <ActionIcon variant="light" color="blue" onClick={() => navigate({ to: `/users/${user.id}/edit` })} radius="md">
            <IconEdit size="1.2rem" stroke={1.5} />
          </ActionIcon>
          <ActionIcon color="red" variant="light" onClick={() => {
            if (confirm('Are you sure you want to delete this user?')) {
              deleteMutation.mutate(user.id);
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
          <Stack gap="md">
            <Group gap="sm">
              <IconUsers size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>User Management</Title>
                <Text size="sm" c="dimmed">
                  Manage system users, their roles, and access to virtual hosts. 
                  Administrators can create and edit users to control platform access.
                </Text>
              </Box>
              <Button leftSection={<IconUserPlus size="1.2rem" />} onClick={() => navigate({ to: '/users/new' })}>
                Add User
              </Button>
            </Group>
            <TextInput
              placeholder="Search users by username, name, email or role..."
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
                <Table.Th>Username</Table.Th>
                <Table.Th>Full Name</Table.Th>
                <Table.Th>Email</Table.Th>
                <Table.Th>Role</Table.Th>
                <Table.Th>VHosts</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows}
              {users.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={6} py="xl">
                    <Text c="dimmed" ta="center">{search ? 'No users match your search' : 'No users found'}</Text>
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
