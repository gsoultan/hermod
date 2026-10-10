import { useMemo, useState } from 'react'
import {
  Alert, Badge, Box, Group, Loader, Paper, Select, Stack, Switch, Text, Title,
} from '@mantine/core'
import { IconAlertCircle, IconCoin, IconPlugConnectedX } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVHost } from '@/context/VHostContext'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import { getSessionRole } from '@/auth/session'
import { isModelVHost } from '@/lib/mlModels'
import {
  aiBudgetKey, getAIBudget, saveAIBudget, setAIKillSwitch, type AIBudget, type AIBudgetReport,
} from '@/lib/aiBudget'
import { BudgetForm } from './BudgetForm'
import { UsagePanel } from './UsagePanel'

/**
 * What a vhost may spend on AI: this month's usage against its limits, the
 * budget and per-workflow caps, and the kill switch that stops every AI node
 * and agent in the vhost. Every signed-in role can read it; Editors and Admins
 * change it.
 */
export function AIBudgetPage() {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const readOnly = getSessionRole() === 'Viewer'
  const { selectedVHost, availableVHosts } = useVHost()
  const [chosen, setChosen] = useState<string | null>(null)
  const vhost = isModelVHost(selectedVHost) ? selectedVHost : chosen
  const vhostOptions = useMemo(
    () => Array.from(new Set(['default', ...availableVHosts.filter((v) => isModelVHost(v))])),
    [availableVHosts],
  )

  const { data: report, isLoading, error } = useQuery({
    queryKey: aiBudgetKey(vhost ?? ''),
    queryFn: ({ signal }) => getAIBudget(vhost as string, signal),
    enabled: !!vhost,
    retry: false,
  })

  const settle = (r: AIBudgetReport) => queryClient.setQueryData(aiBudgetKey(vhost as string), r)
  const save = useMutation({ mutationFn: (b: AIBudget) => saveAIBudget(vhost as string, b), onSuccess: settle })
  const kill = useMutation({ mutationFn: (off: boolean) => setAIKillSwitch(vhost as string, off), onSuccess: settle })

  const toggle = async (off: boolean) => {
    if (off) {
      const ok = await confirm({
        title: 'Switch AI calls off',
        message: `Stop every AI call in ${vhost}?`,
        consequence: 'AI nodes and agents in this vhost fail at once and their messages take the error branch, until the switch is turned back on. Budgets and usage are kept.',
        confirmLabel: 'Switch AI off',
        danger: true,
      })
      if (!ok) return
    }
    kill.mutate(off)
  }

  const disabled = report?.budget.disabled ?? false

  return (
    <Box p="md" className="page-enter">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Stack gap="md">
            <Group gap="sm" align="flex-start">
              <IconCoin size="2rem" color="var(--mantine-color-yellow-filled)" />
              <Box style={{ flex: 1 }}>
                <Group gap="sm" align="center">
                  <Title order={2} fw={800}>AI budget</Title>
                  {vhost && <Badge variant="light" size="lg" radius="sm" tt="none" aria-label="VHost shown">{vhost}</Badge>}
                </Group>
                <Text size="sm" c="dimmed">
                  Monthly token and cost limits for the AI nodes and agents of a vhost, and of single workflows in it.
                  An alert goes out at 80%; at 100% calls are refused and the node takes its error branch.
                </Text>
              </Box>
              {report && (
                <Switch size="md" color="red" label="Switch AI calls off" checked={disabled}
                  disabled={readOnly || kill.isPending} onChange={(e) => toggle(e.currentTarget.checked)} />
              )}
            </Group>
            {!isModelVHost(selectedVHost) && (
              <Select label="VHost" description="A budget belongs to one vhost." placeholder="Select one"
                data={vhostOptions} value={chosen} onChange={setChosen} allowDeselect={false} maw={360} />
            )}
          </Stack>
        </Paper>

        {disabled && (
          <Alert color="red" icon={<IconPlugConnectedX size="1rem" />} title="AI calls are switched off">
            Every AI node and agent in {vhost} fails without calling a model until the switch is turned back on.
          </Alert>
        )}
        {kill.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The kill switch did not change">
            {(kill.error as Error).message}
          </Alert>
        )}

        {!vhost ? (
          <Paper radius="md" withBorder>
            <EmptyState compact icon={<IconCoin size="1.3rem" />} title="Choose a vhost"
              description="Budgets are kept per vhost. Pick one above to see and change its budget." />
          </Paper>
        ) : isLoading ? (
          <Group justify="center" p="xl"><Loader size="sm" /><Text size="sm" c="dimmed">Loading the budget…</Text></Group>
        ) : error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title={`The AI budget of ${vhost} could not be loaded`}>
            {(error as Error).message}
          </Alert>
        ) : report ? (
          <>
            <UsagePanel report={report} />
            <BudgetForm key={`${vhost}/${report.budget.updated_at ?? ''}`} budget={report.budget} readOnly={readOnly}
              saving={save.isPending} error={save.error as Error | null} onSave={(b) => save.mutate(b)} />
          </>
        ) : null}
      </Stack>
    </Box>
  )
}
