import { IconDots, IconEdit, IconHierarchy, IconPlus, IconTrash } from '@tabler/icons-react';
import { useState } from 'react'
import { ActionIcon, Box, Button, Group, Menu, Pagination, Paper } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { Link, useNavigate } from '@tanstack/react-router'
import type { VHost } from '@/types'
import { useConfirm } from '@/components/common/ConfirmProvider';
import { EmptyState, FilterBar, Row, RowList } from '@/components/rows';
import { AdminListHeader } from './AdminListHeader';

export function VHostsPage({ embedded = false }: { embedded?: boolean }) {
  const confirm = useConfirm();
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [activePage, setPage] = useState(1)
  const itemsPerPage = 30

  const { data: vhostsResponse } = useSuspenseQuery<any>({
    queryKey: ['vhosts', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`/api/vhosts?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(search)}`)
      if (!res.ok) throw new Error('Failed to fetch vhosts')
      return res.json()
    }
  })

  const vhosts = (vhostsResponse as any)?.data as VHost[] || []
  const totalItems = (vhostsResponse as any)?.total as number || 0
  const totalPages = Math.ceil(totalItems / itemsPerPage)

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/vhosts/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete vhost')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['vhosts'] })
    }
  })

  const remove = async (vhost: VHost) => {
    if (await confirm({ title: 'Delete virtual host', message: `Permanently delete ${vhost.name}?`, consequence: 'Sources, sinks and flows scoped to it may stop working.', confirmLabel: 'Delete virtual host', danger: true })) {
      deleteMutation.mutate(vhost.id || vhost.name);
    }
  }

  return (
    <Box className="page-enter">
      <AdminListHeader
        embedded={embedded}
        title="Virtual hosts"
        lede="Keep environments, projects or teams apart inside one Hermod"
        back={{ to: '/settings', label: 'Back to settings', search: { tab: 'vhosts' } }}
        actions={<Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/vhosts/new' })}>Add virtual host</Button>}
      />
      <FilterBar search={{ value: search, onChange: (v) => { setSearch(v); setPage(1) }, placeholder: 'Search virtual hosts' }} />

      {vhosts.length === 0 ? (
        <Paper withBorder radius={12}>
          <EmptyState title={search ? 'No virtual hosts match' : 'No virtual hosts yet'} detail={search ? 'Try a name or a word from the description.' : 'Add one to keep a group of flows and connections apart.'} />
        </Paper>
      ) : (
        <RowList aria-label="Virtual hosts">
          {vhosts.map((vhost) => {
            const id = vhost.id || vhost.name
            return (
              <Row
                key={id}
                icon={IconHierarchy}
                title={vhost.name}
                detail={vhost.description || 'No description'}
                to="/vhosts/$vhostId/edit"
                params={{ vhostId: id }}
                action={
                  <Menu position="bottom-end" withArrow shadow="md">
                    <Menu.Target>
                      <ActionIcon aria-label={`More for ${vhost.name}`} variant="subtle" color="gray"><IconDots size="1rem" /></ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      <Menu.Item component={Link} to="/vhosts/$vhostId/edit" params={{ vhostId: id } as any} leftSection={<IconEdit size="1rem" />}>Edit</Menu.Item>
                      <Menu.Item color="red" leftSection={<IconTrash size="1rem" />} onClick={() => remove(vhost)}>Delete</Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                }
              />
            )
          })}
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
