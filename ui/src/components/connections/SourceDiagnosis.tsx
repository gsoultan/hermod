import { useQuery } from '@tanstack/react-query'
import { Alert, Box, Button, Group, Loader, Paper, SimpleGrid, Stack, Table, Text, ThemeIcon, Title } from '@mantine/core'
import { IconAlertTriangle, IconArrowRight, IconBolt, IconCheck, IconDatabase, IconLayoutColumns, IconRefresh, IconX } from '@tabler/icons-react'
import { diagnoseSource, humanDuration, type DiagnosisCheck, type DiagnosisReport, type DiagnosisSession, type DiagnosisStatus } from '@/lib/dbDiagnosis'

const statusIcon: Record<DiagnosisStatus, { color: string; icon: React.ReactNode; label: string }> = {
  ok: { color: 'green', icon: <IconCheck size="1rem" />, label: 'Fine' },
  warn: { color: 'orange', icon: <IconAlertTriangle size="1rem" />, label: 'Worth a look' },
  err: { color: 'red', icon: <IconAlertTriangle size="1rem" />, label: 'Problem' },
  unknown: { color: 'gray', icon: <IconX size="1rem" />, label: 'Could not check' },
}

const wrap = { overflowWrap: 'anywhere' } as const

function SectionLabel({ children }: { children: React.ReactNode }) {
  return <Text size="xs" fw={600} c="dimmed" tt="uppercase" style={{ letterSpacing: '.06em' }} mb={8}>{children}</Text>
}

function Hop({ icon, label }: { icon: React.ReactNode; label: string }) {
  return (
    <Paper withBorder radius="sm" px="sm" py={6}>
      <Group gap={6} wrap="nowrap">{icon}<Text size="sm">{label}</Text></Group>
    </Paper>
  )
}

function Route({ report }: { report: DiagnosisReport }) {
  const { route } = report
  const arrow = <Box visibleFrom="xs" c="dimmed"><IconArrowRight size="0.9rem" /></Box>
  return (
    <Box>
      <SectionLabel>How Hermod reaches it</SectionLabel>
      <Group gap={8}>
        <Hop icon={<IconBolt size="0.95rem" />} label="Hermod" />
        {route.through_pooler && <>{arrow}<Hop icon={<IconLayoutColumns size="0.95rem" />} label="PgBouncer" /></>}
        {arrow}
        <Hop icon={<IconDatabase size="0.95rem" />} label={`PostgreSQL ${route.server_version}`} />
      </Group>
      {route.through_pooler && (
        <Text size="sm" c="dimmed" mt={8}>
          Found by itself: the server listens on {route.server_port}, Hermod dials {route.dialed_port}.
        </Text>
      )}
    </Box>
  )
}

function doing(s: DiagnosisSession) {
  const wait = s.state === 'active' && s.wait_type ? ` · waiting on ${s.wait_type}` : ''
  return `${s.command || s.state || 'connected'}${wait}`
}

function ageColor(sec: number) {
  if (sec >= 3600) return 'red'
  if (sec >= 300) return 'orange'
  return undefined
}

function HermodSessions({ sessions }: { sessions: DiagnosisSession[] }) {
  const active = sessions.filter((s) => s.state !== 'idle')
  return (
    <Box>
      <Title order={4} mb="xs">Hermod's queries on this database now</Title>
      {active.length === 0 ? (
        <Text size="sm" c="dimmed">None running. {sessions.length} idle connection{sessions.length === 1 ? '' : 's'} kept open for reuse.</Text>
      ) : (
        <Table.ScrollContainer minWidth={280}>
          <Table verticalSpacing="sm" horizontalSpacing="xs" fz="sm">
            <Table.Thead><Table.Tr><Table.Th>Connection</Table.Th><Table.Th>Doing</Table.Th><Table.Th ta="right">For</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>
              {active.map((s) => (
                <Table.Tr key={s.pid} data-session={s.pid}>
                  <Table.Td style={wrap}>{s.app}</Table.Td>
                  <Table.Td c="dimmed" style={wrap}>{doing(s)}</Table.Td>
                  <Table.Td ta="right" c={ageColor(s.query_seconds)} style={{ whiteSpace: 'nowrap' }}>{humanDuration(s.query_seconds)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </Box>
  )
}

function CheckRow({ check }: { check: DiagnosisCheck }) {
  const s = statusIcon[check.status] ?? statusIcon.unknown
  return (
    <Group align="flex-start" wrap="nowrap" gap="sm" p="sm" data-check={check.id} data-status={check.status}>
      <ThemeIcon variant="transparent" color={s.color} size="sm" aria-label={s.label}>{s.icon}</ThemeIcon>
      <Box style={{ flex: 1, minWidth: 0 }}>
        <Group justify="space-between" gap="xs" wrap="nowrap" align="flex-start">
          <Text fw={500}>{check.title}</Text>
          <Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{check.value}</Text>
        </Group>
        <Text size="sm" c="dimmed" style={wrap}>{check.detail}</Text>
      </Box>
    </Group>
  )
}

function Checks({ checks }: { checks: DiagnosisCheck[] }) {
  return (
    <Box>
      <SectionLabel>What Hermod checked</SectionLabel>
      <Paper withBorder radius="md">
        <Stack gap={0}>
          {checks.map((c, i) => (
            <Box key={c.id} style={i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined}>
              <CheckRow check={c} />
            </Box>
          ))}
        </Stack>
      </Paper>
    </Box>
  )
}

function Others({ sessions }: { sessions: DiagnosisSession[] }) {
  const busy = sessions.filter((s) => s.state !== 'idle')
  if (busy.length === 0) return null
  return (
    <Box>
      <Title order={4} mb="xs">Other clients on this database</Title>
      <Paper withBorder radius="md">
        {busy.slice(0, 8).map((s, i) => (
          <Group key={s.pid} align="flex-start" wrap="nowrap" gap="sm" p="sm" data-other={s.pid}
            style={i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined}>
            <ThemeIcon variant="transparent" color="gray" size="sm"><IconDatabase size="1rem" /></ThemeIcon>
            <Box style={{ flex: 1, minWidth: 0 }}>
              <Group justify="space-between" gap="xs" wrap="nowrap">
                <Text fw={500} style={wrap}>{s.user}{s.app ? ` · ${s.app}` : ''}</Text>
                <Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{humanDuration(s.query_seconds || s.state_seconds)}</Text>
              </Group>
              <Text size="sm" c="dimmed" style={wrap}>
                {doing(s)}{s.blocked_by?.length ? ` · blocked by ${s.blocked_by.length} session${s.blocked_by.length > 1 ? 's' : ''}` : ''}
              </Text>
            </Box>
          </Group>
        ))}
      </Paper>
    </Box>
  )
}

interface SourceDiagnosisProps {
  sourceId: string
  /** A diagnosis connects to the database, so it takes an editor. */
  canCheck: boolean
}

/**
 * What is happening on a source's database: how Hermod reaches it, what
 * Hermod and others are running, and what Hermod checked -- locks, long
 * queries, waits, open transactions, limits and PgBouncer. It only reads.
 */
export function SourceDiagnosis({ sourceId, canCheck }: SourceDiagnosisProps) {
  const q = useQuery({
    queryKey: ['sources', sourceId, 'diagnosis'],
    queryFn: () => diagnoseSource(sourceId),
    enabled: canCheck,
    staleTime: 0,
    retry: false,
  })
  if (!canCheck) {
    return <Text c="dimmed">Diagnosing the database connects to it, so it takes an Editor.</Text>
  }
  if (q.isPending) {
    return <Group gap="sm"><Loader size="sm" /><Text c="dimmed">Reading the database's activity…</Text></Group>
  }
  if (q.isError) {
    return (
      <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="Hermod could not diagnose this database">
        <Stack gap="xs" align="flex-start">
          <Text size="sm" style={wrap}>{(q.error as Error).message}</Text>
          <Button size="xs" variant="light" leftSection={<IconRefresh size="0.9rem" />} onClick={() => q.refetch()}>Try again</Button>
        </Stack>
      </Alert>
    )
  }
  const r = q.data
  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Text size="sm" c="dimmed">Checked {new Date(r.checked_at).toLocaleTimeString()}. Hermod only reads; nothing on the database is changed.</Text>
        <Button size="xs" variant="default" leftSection={<IconRefresh size="0.9rem" />} loading={q.isFetching} onClick={() => q.refetch()}>Check again</Button>
      </Group>
      {r.limited && <Alert color="gray" title="Some sessions were hidden from Hermod">{r.limited}</Alert>}
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="xl">
        <Stack gap="lg">
          <Route report={r} />
          <HermodSessions sessions={r.hermod ?? []} />
        </Stack>
        <Stack gap="lg">
          <Checks checks={r.checks} />
          <Others sessions={r.others ?? []} />
        </Stack>
      </SimpleGrid>
    </Stack>
  )
}
