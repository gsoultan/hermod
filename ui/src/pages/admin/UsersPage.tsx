import { IconDots, IconEdit, IconPlus, IconTrash } from '@tabler/icons-react';
import { useDebouncedValue } from '@mantine/hooks';
import { useState } from 'react'
import { ActionIcon, Box, Button, Group, Menu, Pagination, Paper, Text } from '@mantine/core'
import { keepPreviousData, useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@/api'
import { Link, useNavigate } from '@tanstack/react-router'
import type { User } from '@/types'
import { useConfirm } from '@/components/common/ConfirmProvider';
import { EmptyState, FilterBar, Row, RowList } from '@/components/rows';
import { AdminListHeader } from './AdminListHeader';

/** Role, where they can work, and who they are: the line under the name. */
export function personDetail(user: User): string {
  const reach = user.role === 'Administrator'
    ? 'every virtual host'
    : user.vhosts?.length ? user.vhosts.join(', ') : 'no virtual hosts'
  return [user.role, reach, user.full_name, user.email].filter(Boolean).join(' · ')
}

export function UsersPage() {
  const confirm = useConfirm();
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  // Debounced so a burst of keystrokes costs one request, and used as the query
  // key so the key changes once per search rather than once per character.
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [activePage, setPage] = useState(1)
  const itemsPerPage = 30

  // `useQuery`, not `useSuspenseQuery`: a suspense query re-suspends on every
  // key change, which threw paging and searching back to the full-page spinner.
  const { data: usersResponse, isPending } = useQuery<any>({
    queryKey: ['users', activePage, debouncedSearch],
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const res = await apiFetch(`/api/users?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(debouncedSearch)}`)
      if (!res.ok) throw new Error('Failed to fetch users')
      return res.json()
    }
  })

  const users = (usersResponse as any)?.data as User[] || []
  const totalItems = (usersResponse as any)?.total as number || 0
  const totalPages = Math.ceil(totalItems / itemsPerPage)

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/users/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete user')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
    }
  })

  const remove = async (user: User) => {
    if (await confirm({ title: 'Delete user', message: `Permanently delete ${user.username || 'this user'}?`, consequence: 'They will lose access immediately. This cannot be undone.', confirmLabel: 'Delete user', danger: true })) {
      deleteMutation.mutate(user.id);
    }
  }

  return (
    <Box className="page-enter">
      <AdminListHeader
        title="People"
        lede="Who can sign in and what they can do"
        back={{ to: '/settings', label: 'Back to settings' }}
        actions={<Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/users/new' })}>Add person</Button>}
      />
      <FilterBar search={{ value: search, onChange: (v) => { setSearch(v); setPage(1) }, placeholder: 'Search people by name, email or role' }} />

      {users.length === 0 ? (
        <Paper withBorder radius={12}>
          {isPending
            ? <Text c="dimmed" ta="center" py="xl">Loading people…</Text>
            : <EmptyState title={search ? 'Nobody matches' : 'Nobody here yet'} detail={search ? 'Try a username, a name, an email or a role.' : 'Add a person to let them sign in.'} />}
        </Paper>
      ) : (
        <RowList aria-label="People">
          {users.map((user) => (
            <Row
              key={user.id}
              avatar={user.username || '?'}
              title={user.username}
              detail={personDetail(user)}
              state={<Text size="sm" c="dimmed">{user.two_factor_enabled ? 'Two-factor on' : 'Two-factor off'}</Text>}
              to="/users/$userId/edit"
              params={{ userId: user.id }}
              action={
                <Menu position="bottom-end" withArrow shadow="md">
                  <Menu.Target>
                    <ActionIcon aria-label={`More for ${user.username}`} variant="subtle" color="gray"><IconDots size="1rem" /></ActionIcon>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item component={Link} to="/users/$userId/edit" params={{ userId: user.id } as any} leftSection={<IconEdit size="1rem" />}>Edit</Menu.Item>
                    <Menu.Item color="red" leftSection={<IconTrash size="1rem" />} onClick={() => remove(user)}>Delete</Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              }
            />
          ))}
        </RowList>
      )}
      {totalPages > 1 && (
        <Group justify="center" mt="md">
          <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
        </Group>
      )}
    </Box>
  )
}
