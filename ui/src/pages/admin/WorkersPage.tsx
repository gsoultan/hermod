import { IconAlertTriangle, IconDots, IconEdit, IconInfoCircle, IconPlayerPlay, IconPlayerStop, IconPlus, IconServerBolt, IconTerminal2, IconTrash } from '@tabler/icons-react';
import { useState } from 'react'
import { ActionIcon, Alert, Box, Button, Code, CopyButton, Group, Menu, Modal, Pagination, Paper, Stack, Text } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { Link, useNavigate } from '@tanstack/react-router'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import type { Worker } from '@/types'
import { useConfirm } from '@/components/common/ConfirmProvider';
import { ResourceGauge } from '@/components/common/ResourceGauge';
import { NO_READING, formatCapacity, usageFraction } from '@/utils/metricFormat';
import { EmptyState, FilterBar, Row, RowList, StatePill } from '@/components/rows';
import { AdminListHeader } from './AdminListHeader';

export function WorkersPage({ embedded = false }: { embedded?: boolean }) {
  const confirm = useConfirm();
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [activePage, setPage] = useState(1)
  const itemsPerPage = 30
  const [opened, { open, close }] = useDisclosure(false)
  const [selectedWorker, setSelectedWorker] = useState<Worker | null>(null)

  const { data: workersResponse } = useSuspenseQuery<any>({
    queryKey: ['workers', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`/api/workers?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(search)}`)
      if (!res.ok) throw new Error('Failed to fetch workers')
      return res.json()
    },
    refetchInterval: 5000, // Refresh every 5 seconds for realtime status
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

  const startMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workers/${id}/start`, { method: 'POST' })
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error || 'Failed to start worker')
      }
      return res.json()
    },
    onSuccess: () => {
      notifications.show({
        title: 'Worker starting',
        message: 'The worker process has been launched. It may take a moment to come online.',
        color: 'green',
      })
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    },
    onError: (err: Error) => {
      notifications.show({
        title: 'Failed to start worker',
        message: err.message,
        color: 'red',
      })
    }
  })

  const shutdownMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workers/${id}/shutdown`, { method: 'POST' })
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error || 'Failed to shut down worker')
      }
      return res.json()
    },
    onSuccess: () => {
      notifications.show({
        title: 'Graceful shutdown requested',
        message: 'The worker is draining. Its running workflows will move to other available workers, then it will stop.',
        color: 'blue',
      })
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    },
    onError: (err: Error) => {
      notifications.show({
        title: 'Failed to shut down worker',
        message: err.message,
        color: 'red',
      })
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

  const formatLastSeen = (lastSeen?: string) => {
    if (!lastSeen) return 'Never';
    const date = new Date(lastSeen);
    const now = new Date();
    const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return date.toLocaleDateString();
  };

  const shutDown = async (worker: Worker) => {
    if (await confirm({ title: 'Shut down worker', message: `Gracefully shut down ${worker.name || worker.id}?`, consequence: 'Its running workflows are handed off to other available workers.', confirmLabel: 'Shut down' })) {
      shutdownMutation.mutate(worker.id);
    }
  }
  const unregister = async (worker: Worker) => {
    if (await confirm({ title: 'Unregister worker', message: `Unregister ${worker.name || worker.id}?`, consequence: 'Workflows pinned to this worker will stop until it is re-registered.', confirmLabel: 'Unregister', danger: true })) {
      deleteMutation.mutate(worker.id);
    }
  }

  return (
    <Box className="page-enter">
      {workers.length > 0 && workers.every(w => !isOnline(w.last_seen)) && (
        <Alert icon={<IconAlertTriangle size="1rem" />} title="All workers are offline" color="red" radius="md" variant="filled" mb="md">
          Hermod cannot run any flows while every registered worker is offline. Start at least one worker to resume.
        </Alert>
      )}
      <AdminListHeader
        embedded={embedded}
        title="Workers"
        lede="The machines that run your flows. Register one per server to assign flows to it."
        back={{ to: '/settings', label: 'Back to settings', search: { tab: 'workers' } }}
        actions={<Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: '/workers/new' })}>Register worker</Button>}
      />
      <FilterBar search={{ value: search, onChange: (v) => { setSearch(v); setPage(1) }, placeholder: 'Search workers by name, ID, host or description' }} />

      {workers.length === 0 ? (
        <Paper withBorder radius={12}>
          <EmptyState title={search ? 'No workers match' : 'No workers registered yet'} detail={search ? 'Try a name, an ID, a host or a word from the description.' : 'Register a worker, then start Hermod on that machine with the command it gives you.'} />
        </Paper>
      ) : (
        <RowList aria-label="Workers">
          {workers.map((worker) => {
            const online = isOnline(worker.last_seen);
            const name = worker.name || worker.id;
            return (
              <Row
                key={worker.id}
                icon={IconServerBolt}
                iconTone={online ? 'default' : 'err'}
                title={name}
                detail={[worker.host ? `${worker.host}:${worker.port}` : '', worker.description, worker.id].filter(Boolean).join(' · ')}
                to="/workers/$workerId/edit"
                params={{ workerId: worker.id }}
                state={
                  <Group gap="md" wrap="wrap">
                    <StatePill tone={worker.draining ? 'warn' : online ? 'ok' : 'err'}>
                      {worker.draining ? 'Draining' : online ? 'Online' : 'Offline'}
                    </StatePill>
                    {!online && <Text size="sm" c="dimmed">Last seen {formatLastSeen(worker.last_seen).toLowerCase()}</Text>}
                    {online && (
                      <>
                        <ResourceGauge
                          fraction={worker.cpu_usage ?? null}
                          caption={worker.cpu_cores ? `${worker.cpu_cores} core${worker.cpu_cores === 1 ? '' : 's'}` : NO_READING}
                          tooltip="CPU"
                        />
                        <ResourceGauge
                          fraction={usageFraction(worker.memory_used_bytes ?? 0, worker.memory_total_bytes ?? 0) ?? worker.memory_usage ?? null}
                          caption={formatCapacity(worker.memory_used_bytes ?? 0, worker.memory_total_bytes ?? 0)}
                          tooltip="Memory"
                        />
                        <ResourceGauge
                          fraction={usageFraction(worker.storage_used_bytes ?? 0, worker.storage_total_bytes ?? 0)}
                          caption={formatCapacity(worker.storage_used_bytes ?? 0, worker.storage_total_bytes ?? 0)}
                          tooltip="Storage (data directory)"
                        />
                      </>
                    )}
                  </Group>
                }
                action={
                  <Group gap="xs" wrap="nowrap">
                    {!online && (
                      <Button size="xs" variant="default" leftSection={<IconPlayerPlay size="0.9rem" />} aria-label={`Start ${name}`}
                        onClick={() => startMutation.mutate(worker.id)} loading={startMutation.isPending && startMutation.variables === worker.id}>
                        Start
                      </Button>
                    )}
                    <Menu position="bottom-end" withArrow shadow="md">
                      <Menu.Target>
                        <ActionIcon aria-label={`More for ${name}`} variant="subtle" color="gray"><IconDots size="1rem" /></ActionIcon>
                      </Menu.Target>
                      <Menu.Dropdown>
                        <Menu.Item component={Link} to="/workers/$workerId/edit" params={{ workerId: worker.id } as any} leftSection={<IconEdit size="1rem" />}>Edit</Menu.Item>
                        {online && <Menu.Item leftSection={<IconPlayerStop size="1rem" />} onClick={() => shutDown(worker)}>Shut down</Menu.Item>}
                        {!online && <Menu.Item leftSection={<IconTerminal2 size="1rem" />} onClick={() => { setSelectedWorker(worker); open(); }}>How to start it</Menu.Item>}
                        <Menu.Item color="red" leftSection={<IconTrash size="1rem" />} onClick={() => unregister(worker)}>Unregister</Menu.Item>
                      </Menu.Dropdown>
                    </Menu>
                  </Group>
                }
              />
            );
          })}
        </RowList>
      )}
      {totalPages > 1 && (
        <Group justify="center" mt="md">
          <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
        </Group>
      )}

      <Modal opened={opened} onClose={close} title="How to Start Worker" size="lg" radius="md">
        {selectedWorker && (
          <Stack gap="md">
            <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
              To start this worker, you need to run the Hermod binary on the target machine with the following parameters.
            </Alert>
            
            <Text fw={700} size="sm">Option 1: Command Line</Text>
            <Box style={{ position: 'relative' }}>
              <Code block p="md">
                {`hermod --mode worker --worker-guid "${selectedWorker.id}" --platform-url "${window.location.origin}" --worker-token "<YOUR_TOKEN>"`}
              </Code>
              <CopyButton value={`hermod --mode worker --worker-guid "${selectedWorker.id}" --platform-url "${window.location.origin}" --worker-token "<YOUR_TOKEN>"`}>
                {({ copied, copy }) => (
                  <Button 
                    size="compact-xs" 
                    variant="light" 
                    color={copied ? 'teal' : 'blue'} 
                    onClick={copy}
                    style={{ position: 'absolute', top: 10, right: 10 }}
                  >
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                )}
              </CopyButton>
            </Box>
            
            <Text fw={700} size="sm">Option 2: Environment Variables</Text>
            <Code block p="md">
              {`HERMOD_MODE=worker\nHERMOD_WORKER_GUID=${selectedWorker.id}\nHERMOD_PLATFORM_URL=${window.location.origin}\nHERMOD_WORKER_TOKEN=<YOUR_TOKEN>`}
            </Code>

            <Text size="sm" c="dimmed">
              Note: Replace <Code>{`<YOUR_TOKEN>`}</Code> with the token generated when you registered the worker. If you lost the token, you may need to re-register the worker or check the <Code>worker.yaml</Code> file on the worker machine.
            </Text>

            <Group justify="flex-end" mt="md">
              <Button onClick={close}>Close</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Box>
  )
}


