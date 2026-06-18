import { IconAlertTriangle, IconEdit, IconInfoCircle, IconPlayerPlay, IconPlus, IconSearch, IconServer, IconTerminal2, IconTrash } from '@tabler/icons-react';
import { useState } from 'react'
import { Title, Table, Button, Group, Paper, Text, Box, Stack, Badge, TextInput, Pagination, ActionIcon, RingProgress, Tooltip, Center, Alert, Modal, Code, CopyButton } from '@mantine/core'
import { useSuspenseQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { useNavigate } from '@tanstack/react-router'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import type { Worker } from '@/types'
export function WorkersPage() {
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
          <Stack gap={0}>
            <Badge variant="dot" color={online ? 'green' : 'red'}>
              {online ? 'Online' : 'Offline'}
            </Badge>
            {!online && (
              <Text size="xs" c="dimmed" mt={4}>
                Last seen: {formatLastSeen(worker.last_seen)}
              </Text>
            )}
          </Stack>
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
            {!online && (
              <Tooltip label="Start this worker">
                <ActionIcon variant="light" color="green" onClick={() => startMutation.mutate(worker.id)} loading={startMutation.isPending && startMutation.variables === worker.id} radius="md" aria-label="Start worker">
                  <IconPlayerPlay size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            )}
            {!online && (
              <Tooltip label="How to start this worker manually">
                <ActionIcon variant="light" color="orange" onClick={() => { setSelectedWorker(worker); open(); }} radius="md" aria-label="Worker start instructions">
                  <IconTerminal2 size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            )}
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
        {workers.length > 0 && workers.every(w => !isOnline(w.last_seen)) && (
          <Alert icon={<IconAlertTriangle size="1rem" />} title="All Workers Offline" color="red" radius="md" variant="filled">
            Hermod cannot process any workflows because all registered workers are currently offline. Please start at least one worker to resume processing.
          </Alert>
        )}
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
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
            <Table.Thead>
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
            <Group justify="center" p="md" bg="var(--mantine-color-body)" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
              <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
            </Group>
          )}
        </Paper>
      </Stack>
      
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


