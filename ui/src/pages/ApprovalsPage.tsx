import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../api'
import { 
  Alert, Badge, Box, Button, Group, Modal, Paper, Pagination, Select, Stack, Table, Text, Textarea, TextInput, Title 
} from '@mantine/core'
import { IconChevronsDown, IconCircleCheck, IconCircleX, IconRefresh, IconSearch } from '@tabler/icons-react'

type Approval = {
  id: string
  workflow_id: string
  node_id: string
  message_id: string
  status: 'pending' | 'approved' | 'rejected'
  created_at: string
  processed_at?: string
  processed_by?: string
  notes?: string
  metadata?: Record<string, string>
  data?: Record<string, any>
}

export function ApprovalsPage() {
  const qc = useQueryClient()
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState<string | null>('pending')
  const [workflowId, setWorkflowId] = useState<string | null>(null)
  const [page, setPage] = useState(1)
  const limit = 30

  const { data: workflows } = useQuery<any>({
    queryKey: ['workflows','all'],
    queryFn: async () => {
      const res = await apiFetch('/api/workflows?limit=1000')
      if (!res.ok) throw new Error('Failed to fetch workflows')
      return res.json()
    }
  })

  const { data: approvalsResp, isFetching, refetch } = useQuery<any>({
    queryKey: ['approvals', page, status, workflowId, search],
    queryFn: async () => {
      const params = new URLSearchParams()
      params.set('page', String(page))
      params.set('limit', String(limit))
      if (status) params.set('status', status)
      if (workflowId) params.set('workflow_id', workflowId)
      if (search) params.set('search', search)
      const res = await apiFetch(`/api/approvals?${params.toString()}`)
      if (!res.ok) throw new Error('Failed to fetch approvals')
      return res.json()
    }
  })

  const approvals: Approval[] = approvalsResp?.data || []
  const total = approvalsResp?.total || 0
  const pages = Math.ceil(total / limit) || 1

  const [details, setDetails] = useState<Approval | null>(null)
  const [notes, setNotes] = useState('')

  useEffect(() => {
    setNotes('')
  }, [details?.id])

  const approveMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/approvals/${id}/approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ notes })
      })
      if (!res.ok) throw new Error('Failed to approve')
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['approvals'] })
      setDetails(null)
    }
  })

  const rejectMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/approvals/${id}/reject`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ notes })
      })
      if (!res.ok) throw new Error('Failed to reject')
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['approvals'] })
      setDetails(null)
    }
  })

  const rows = approvals.map((a) => (
    <Table.Tr key={a.id} onClick={() => setDetails(a)} style={{ cursor: 'pointer' }}>
      <Table.Td><Text size="sm" c="dimmed">{a.id.slice(0,8)}</Text></Table.Td>
      <Table.Td>{a.workflow_id}</Table.Td>
      <Table.Td>{a.node_id}</Table.Td>
      <Table.Td>{a.message_id}</Table.Td>
      <Table.Td>
        {a.status === 'pending' && <Badge color="yellow" variant="light">Pending</Badge>}
        {a.status === 'approved' && <Badge color="green" variant="light" leftSection={<IconCircleCheck size="0.8rem" />}>Approved</Badge>}
        {a.status === 'rejected' && <Badge color="red" variant="light" leftSection={<IconCircleX size="0.8rem" />}>Rejected</Badge>}
      </Table.Td>
      <Table.Td>
        <Text size="sm" c="dimmed">{new Date(a.created_at).toLocaleString()}</Text>
      </Table.Td>
    </Table.Tr>
  ))

  const workflowOptions = useMemo(() => {
    const arr = (workflows?.data || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))
    return arr
  }, [workflows])

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper withBorder p="md" radius="md" bg="gray.0">
          <Group justify="space-between" align="flex-end">
            <Stack gap={2} style={{ flex: 1 }}>
              <Title order={2} fw={800}>Approvals</Title>
              <Text size="sm" c="dimmed">Review and decide on messages paused at Approval nodes.</Text>
            </Stack>
            <Group wrap="nowrap">
              <Button variant="light" leftSection={<IconRefresh size="1rem" />} onClick={() => refetch()} loading={isFetching}>Refresh</Button>
            </Group>
          </Group>
          <Group mt="md" wrap="wrap" gap="sm">
            <TextInput placeholder="Search by ID/Message/Node" leftSection={<IconSearch size="1rem" />} value={search} onChange={(e)=>{ setSearch(e.currentTarget.value); setPage(1) }} w={280} />
            <Select label={undefined} placeholder="Filter by status" data={[
              { value: 'pending', label: 'Pending' },
              { value: 'approved', label: 'Approved' },
              { value: 'rejected', label: 'Rejected' },
            ]} value={status} onChange={(v)=>{ setStatus(v); setPage(1) }} w={180} clearable />
            <Select label={undefined} placeholder="Filter by workflow" data={workflowOptions} value={workflowId} onChange={(v)=>{ setWorkflowId(v); setPage(1) }} searchable clearable w={260} />
          </Group>
        </Paper>

        <Paper radius="md" style={{ border: '1px solid var(--mantine-color-gray-1)', overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="xl">
            <Table.Thead bg="gray.0">
              <Table.Tr>
                <Table.Th>ID</Table.Th>
                <Table.Th>Workflow</Table.Th>
                <Table.Th>Node</Table.Th>
                <Table.Th>Message</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Created</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows}
              {approvals.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={6} py="xl">
                    <Text c="dimmed" ta="center">No approvals found</Text>
                  </Table.Td>
                </Table.Tr>
              )}
            </Table.Tbody>
          </Table>
          {pages > 1 && (
            <Group justify="center" p="md" bg="gray.0" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
              <Pagination total={pages} value={page} onChange={setPage} radius="md" />
            </Group>
          )}
        </Paper>

        <Modal opened={!!details} onClose={()=>setDetails(null)} title={<Group gap={6}><IconChevronsDown size="1rem" /><Text fw={700}>Approval Details</Text></Group>} size="lg">
          {details && (
            <Stack>
              <Group gap="sm">
                <Badge variant="light" color={details.status === 'pending' ? 'yellow' : details.status === 'approved' ? 'green' : 'red'}>
                  {details.status}
                </Badge>
                <Text size="sm" c="dimmed">Workflow: {details.workflow_id}</Text>
                <Text size="sm" c="dimmed">Node: {details.node_id}</Text>
              </Group>
              <Paper withBorder p="sm" radius="md">
                <Text size="xs" fw={800} c="dimmed" mb={4}>Metadata</Text>
                <pre style={{ margin: 0, maxHeight: 180, overflow: 'auto' }}>{JSON.stringify(details.metadata || {}, null, 2)}</pre>
              </Paper>
              <Paper withBorder p="sm" radius="md">
                <Text size="xs" fw={800} c="dimmed" mb={4}>Data</Text>
                <pre style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>{JSON.stringify(details.data || {}, null, 2)}</pre>
              </Paper>
              {details.status === 'pending' ? (
                <>
                  <Textarea label="Notes (optional)" placeholder="Reason or instructions..." value={notes} onChange={(e)=>setNotes(e.currentTarget.value)} autosize minRows={2} />
                  <Group justify="space-between">
                    <Button leftSection={<IconCircleX size="1rem" />} color="red" variant="light" onClick={()=> rejectMutation.mutate(details.id)} loading={rejectMutation.isPending}>Reject</Button>
                    <Button leftSection={<IconCircleCheck size="1rem" />} color="green" onClick={()=> approveMutation.mutate(details.id)} loading={approveMutation.isPending}>Approve</Button>
                  </Group>
                </>
              ) : (
                <Alert color={details.status === 'approved' ? 'green' : 'red'} variant="light">
                  Decision by {details.processed_by || 'N/A'} at {details.processed_at ? new Date(details.processed_at).toLocaleString() : '—'}
                  {details.notes && (<><br/>Notes: {details.notes}</>)}
                </Alert>
              )}
            </Stack>
          )}
        </Modal>
      </Stack>
    </Box>
  )
}

export default ApprovalsPage
