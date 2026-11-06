import { lazy } from 'react'
import { Anchor, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Link as RouterLink } from '@tanstack/react-router'
import { IconInfoCircle } from '@tabler/icons-react'
import { getSessionRole } from '@/auth/session'
import { HubPage } from '@/components/layout/HubPage'
import { approvalsTabs } from '@/components/layout/nav'
import { EmptyState } from '@/components/rows'
import { useWaitingApprovals } from '@/lib/approvals'
import { groupByRelease, releaseStatus, useLinks, usePromotions, useSent, useVHostRequests } from '@/lib/environments'
import { ReleaseCard } from '@/pages/environments/EnvironmentsPage'
import { WaitingRow } from '@/pages/environments/SentReleases'
import { VHostRequests } from '@/pages/environments/EnvironmentsTab'
import { useHubTab } from '@/pages/hubs/useHubTab'

const ApprovalsPage = lazy(async () => ({ default: (await import('@/pages/workflows/ApprovalsPage')).ApprovalsPage }))

/**
 * Everything that waits for a decision, in one place. The two kinds share
 * only the word "approval", so each has its own tab and opens with one line
 * on what approving it does.
 */
export function ApprovalsHubPage() {
  const waiting = useWaitingApprovals()
  const tabs = approvalsTabs(waiting)
  const [tab, setTab] = useHubTab(tabs)
  return (
    <HubPage
      title="Approvals"
      lede="Everything waiting for a decision. Nothing here moves until someone decides."
      tabs={tabs}
      value={tab}
      onChange={setTab}
    >
      {tab === 'held' ? <HeldMessages /> : <Releases />}
    </HubPage>
  )
}

function Explainer({ children }: { children: React.ReactNode }) {
  return (
    <Group gap="xs" wrap="nowrap" align="flex-start">
      <IconInfoCircle size="1.1rem" color="var(--mantine-color-dimmed)" style={{ flexShrink: 0, marginTop: 2 }} aria-hidden />
      <Text size="sm" c="dimmed">{children}</Text>
    </Group>
  )
}

const border = (i: number) => (i ? { borderTop: '1px solid var(--mantine-color-default-border)' } : undefined)

function Releases() {
  const isAdmin = getSessionRole() === 'Administrator'
  const { data: received } = usePromotions(isAdmin)
  const { data: sent } = useSent(isAdmin)
  const { data: links } = useLinks(isAdmin)
  const { data: requests } = useVHostRequests(isAdmin)

  // Kept on screen while it goes live, so approving does not make it vanish mid-deploy.
  const here = groupByRelease(received ?? []).filter((ps) => ['pending', 'deploying'].includes(releaseStatus(ps)))
  const deciding = new Set((links ?? []).filter((l) => l.decide).map((l) => l.id))
  const there = groupByRelease(sent ?? []).filter((ps) => releaseStatus(ps) === 'pending' && deciding.has(ps[0].peer_id))
  const nothing = here.length === 0 && there.length === 0 && (requests ?? []).length === 0

  return (
    <Stack gap="lg">
      <Explainer>
        A release is a set of flow versions sent from another environment, such as staging to production. Approving it
        makes all of them live there at once.
      </Explainer>
      {!isAdmin ? (
        <Paper withBorder radius="lg">
          <EmptyState title="Administrators approve releases. You can approve held messages." />
        </Paper>
      ) : (
        <>
          <VHostRequests />
          {here.length > 0 && (
            <Stack gap="md">
              <Title order={4}>Sent here, waiting to go live</Title>
              {here.map((ps) => <ReleaseCard key={ps[0].id} ps={ps} />)}
            </Stack>
          )}
          {there.length > 0 && (
            <Stack gap="xs">
              <Title order={4}>Sent from here, waiting there</Title>
              <Text size="sm" c="dimmed">Approve here and it goes live there. Nobody has to sign in to the other instance.</Text>
              <Paper withBorder radius="lg">
                {there.map((ps, i) => <WaitingRow key={ps[0].id} ps={ps} style={border(i)} />)}
              </Paper>
            </Stack>
          )}
          {nothing && (
            <Paper withBorder radius="lg">
              <EmptyState
                title="No release is waiting for you"
                detail="A release sent to this instance, or one this instance sent where you may decide, shows up here."
              />
            </Paper>
          )}
        </>
      )}
      <Text size="sm" c="dimmed">
        Promote flows, and see every release sent and received, on{' '}
        <Anchor component={RouterLink} to="/environments" size="sm">Releases</Anchor>.
      </Text>
    </Stack>
  )
}

function HeldMessages() {
  return (
    <Stack gap="lg">
      <Explainer>
        An Approval step in a running flow holds each message that reaches it until someone approves or rejects it. An AI
        agent step holds the writes it wants to make the same way. Approving lets that one message continue and changes
        nothing else.
      </Explainer>
      <ApprovalsPage />
    </Stack>
  )
}
