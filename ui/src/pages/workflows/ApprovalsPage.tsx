import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { AgentRunDetails } from '@/components/workflow/AgentTranscript/AgentRunDetails'
import { 
  ActionIcon, Alert, Badge, Box, Button, Group, Modal, Paper, Pagination, Select, Stack, Text, Textarea, TextInput, Tooltip 
} from '@mantine/core'
import { IconChevronsDown, IconCircleCheck, IconCircleX, IconHierarchy, IconRefresh } from '@tabler/icons-react'
import { EmptyState, FilterBar, Row, RowList, StatePill } from '@/components/rows'

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
  form_definition?: Record<string, any>
  form_data?: Record<string, any>
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
      if (status && status !== 'all') params.set('status', status)
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
  const [formData, setFormData] = useState<Record<string, any>>({})

  useEffect(() => {
    setNotes('')
    setFormData(details?.form_data || {})
  }, [details?.id, details?.form_data])

  // From the row there are no notes and no form; from the review dialog there may be both.
  const approveMutation = useMutation({
    mutationFn: async (vars: { id: string; inline?: boolean }) => {
      const res = await apiFetch(`/api/approvals/${vars.id}/approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(vars.inline ? { notes: '', form_data: {} } : { notes, form_data: formData })
      })
      if (!res.ok) throw new Error('Failed to approve')
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['approvals'] })
      setDetails(null)
    }
  })

  const rejectMutation = useMutation({
    mutationFn: async (vars: { id: string; inline?: boolean }) => {
      const res = await apiFetch(`/api/approvals/${vars.id}/reject`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(vars.inline ? { notes: '', form_data: {} } : { notes, form_data: formData })
      })
      if (!res.ok) throw new Error('Failed to reject')
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['approvals'] })
      setDetails(null)
    }
  })

  const workflowOptions = useMemo(() => {
    const arr = (workflows?.data || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))
    return arr
  }, [workflows])
  const flowName = (id: string) => (workflows?.data || []).find((w: any) => w.id === id)?.name || `Flow ${id.slice(0, 8)}`
  const needsAnswers = (a: Approval) => Boolean(a.form_definition?.fields?.length)

  return (
    <Box>
      <Stack gap={0}>
        <FilterBar
          search={{ value: search, onChange: (v) => { setSearch(v); setPage(1) }, placeholder: 'Search by ID, message or step' }}
          filter={{
            value: status || 'all',
            onChange: (v) => { setStatus(v); setPage(1) },
            options: [
              { value: 'pending', label: 'Waiting' },
              { value: 'approved', label: 'Approved' },
              { value: 'rejected', label: 'Rejected' },
              { value: 'all', label: 'All' },
            ],
          }}
        >
          <Select aria-label="Filter by flow" placeholder="Any flow" data={workflowOptions} value={workflowId} onChange={(v)=>{ setWorkflowId(v); setPage(1) }} searchable clearable w={220} />
          <Tooltip label="Refresh">
            <ActionIcon aria-label="Refresh approvals" variant="default" size={36} onClick={() => refetch()} loading={isFetching}><IconRefresh size="1rem" /></ActionIcon>
          </Tooltip>
        </FilterBar>

        {approvals.length === 0 ? (
          <Paper withBorder radius={12}>
            <EmptyState
              title={status === 'pending' && !search && !workflowId ? 'Nothing is waiting for approval' : 'No approvals match'}
              detail="Messages paused at an Approval step, and tool calls an AI agent step wants to make, wait here for a decision."
            />
          </Paper>
        ) : (
          <RowList aria-label="Approvals">
            {approvals.map((a) => (
              <Row
                key={a.id}
                icon={IconHierarchy}
                title={flowName(a.workflow_id)}
                detail={`Step ${a.node_id} · ${a.id.slice(0, 8)} · ${new Date(a.created_at).toLocaleString()}`}
                state={a.status === 'approved'
                  ? <StatePill tone="ok">Approved</StatePill>
                  : a.status === 'rejected' ? <StatePill tone="err">Rejected</StatePill> : undefined}
                onOpen={() => setDetails(a)}
                action={a.status !== 'pending' ? undefined : needsAnswers(a) ? (
                  <Button size="xs" variant="default" onClick={() => setDetails(a)}>Review</Button>
                ) : (
                  <Group gap="xs" wrap="nowrap">
                    <Button size="xs" variant="default" onClick={() => rejectMutation.mutate({ id: a.id, inline: true })}>Reject</Button>
                    <Button size="xs" onClick={() => approveMutation.mutate({ id: a.id, inline: true })}>Approve</Button>
                  </Group>
                )}
              />
            ))}
          </RowList>
        )}
        {pages > 1 && (
          <Group justify="center" mt="md">
            <Pagination total={pages} value={page} onChange={setPage} />
          </Group>
        )}

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
              <AgentRunDetails data={details.data} />
              <Paper withBorder p="sm" radius="md">
                <Text size="xs" fw={800} c="dimmed" mb={4}>Metadata</Text>
                <pre style={{ margin: 0, maxHeight: 180, overflow: 'auto' }}>{JSON.stringify(details.metadata || {}, null, 2)}</pre>
              </Paper>
              <Paper withBorder p="sm" radius="md">
                <Text size="xs" fw={800} c="dimmed" mb={4}>Data</Text>
                <pre style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>{JSON.stringify(details.data || {}, null, 2)}</pre>
              </Paper>

              {details.form_definition?.fields && (
                <Paper withBorder p="md" radius="md" bg="var(--mantine-color-gray-0)">
                  <Text fw={700} size="sm" mb="md">Input Required</Text>
                  <Stack gap="sm">
                    {details.form_definition.fields.map((f: any) => (
                      <Box key={f.name}>
                        {f.type === 'text' && (
                          <TextInput 
                            label={f.label || f.name} 
                            placeholder={f.placeholder} 
                            required={f.required}
                            disabled={details.status !== 'pending'}
                            value={formData[f.name] || ''}
                            onChange={(e) => setFormData(prev => ({ ...prev, [f.name]: e.currentTarget.value }))}
                          />
                        )}
                        {f.type === 'textarea' && (
                          <Textarea 
                            label={f.label || f.name} 
                            placeholder={f.placeholder} 
                            required={f.required}
                            disabled={details.status !== 'pending'}
                            value={formData[f.name] || ''}
                            onChange={(e) => setFormData(prev => ({ ...prev, [f.name]: e.currentTarget.value }))}
                            autosize minRows={2}
                          />
                        )}
                        {f.type === 'select' && (
                          <Select 
                            label={f.label || f.name} 
                            data={f.options || []}
                            required={f.required}
                            disabled={details.status !== 'pending'}
                            value={formData[f.name] || ''}
                            onChange={(val) => setFormData(prev => ({ ...prev, [f.name]: val }))}
                          />
                        )}
                      </Box>
                    ))}
                  </Stack>
                </Paper>
              )}

              {details.status === 'pending' ? (
                <>
                  <Textarea label="Notes (optional)" placeholder="Reason or instructions..." value={notes} onChange={(e)=>setNotes(e.currentTarget.value)} autosize minRows={2} />
                  <Group justify="space-between">
                    <Button leftSection={<IconCircleX size="1rem" />} color="red" variant="light" onClick={()=> rejectMutation.mutate({ id: details.id })} loading={rejectMutation.isPending}>Reject</Button>
                    <Button leftSection={<IconCircleCheck size="1rem" />} color="green" onClick={()=> approveMutation.mutate({ id: details.id })} loading={approveMutation.isPending}>Approve</Button>
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
