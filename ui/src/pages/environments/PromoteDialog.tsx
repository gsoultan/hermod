import { useState } from 'react'
import {
  Alert, Badge, Button, Group, List, Loader, Modal, Select, Stack, Switch, Text, Textarea, ThemeIcon,
} from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link as RouterLink } from '@tanstack/react-router'
import { IconAlertTriangle, IconCheck, IconRocket, IconX } from '@tabler/icons-react'
import { formatDateTime } from '@/utils/dateUtils'
import { getSessionRole } from '@/auth/session'
import {
  environmentKeys, previewPromotion, promote, useLinks, type Check, type Promotion,
} from '@/lib/environments'

interface Version {
  version: number
  created_at?: string
  message?: string
}

interface PromoteDialogProps {
  workflowId: string
  workflowName: string
  opened: boolean
  onClose: () => void
  versions?: Version[]
}

const changeColor = { added: 'green', removed: 'red', changed: 'blue' } as const

/**
 * Sends a version of this workflow to a linked install. The checks run
 * first, against the other install, and anything blocking keeps the button
 * off; the other install shows the same changes to whoever approves it.
 */
export function PromoteDialog({ workflowId, workflowName, opened, onClose, versions = [] }: PromoteDialogProps) {
  const queryClient = useQueryClient()
  const { data: links, isLoading: linksLoading } = useLinks(opened)
  const [chosenLink, setLinkId] = useState<string | null>(null)
  const linkId = chosenLink ?? links?.[0]?.id ?? null
  const [version, setVersion] = useState('0')
  const [note, setNote] = useState('')
  const [start, setStart] = useState(true)
  const [autoRollback, setAutoRollback] = useState<boolean | null>(null)
  const [sent, setSent] = useState<Promotion | null>(null)

  const preview = useQuery({
    queryKey: ['environments', 'preview', linkId, workflowId, version],
    queryFn: () => previewPromotion(linkId as string, workflowId, Number(version)),
    enabled: opened && !!linkId && !sent,
    staleTime: 0,
  })
  const rules = preview.data?.rules
  const rollback = autoRollback ?? rules?.auto_rollback ?? true

  const send = useMutation({
    mutationFn: () => promote(linkId as string, workflowId, { version: Number(version), note: note.trim(), start, auto_rollback: rollback }),
    onSuccess: async (p) => {
      setSent(p)
      await queryClient.invalidateQueries({ queryKey: environmentKeys.sent })
    },
  })

  const close = () => {
    setSent(null)
    setNote('')
    setAutoRollback(null)
    send.reset()
    onClose()
  }

  const link = links?.find((l) => l.id === linkId)
  const direct = rules?.approval === 'none'

  return (
    <Modal opened={opened} onClose={close} title={`Promote ${workflowName}`} size="lg">
      {linksLoading ? (
        <Group justify="center" p="lg"><Loader size="sm" /></Group>
      ) : (links ?? []).length === 0 ? (
        <Stack>
          <Alert color="blue" title="Nowhere to promote to yet">
            Link this instance to your production instance first, in Settings › Environments.
          </Alert>
          {getSessionRole() === 'Administrator' && (
            <Group justify="flex-end">
              <Button component={RouterLink} to="/settings" search={{ tab: 'environments' } as any} onClick={close}>
                Open Settings
              </Button>
            </Group>
          )}
        </Stack>
      ) : sent ? (
        <Stack>
          <Alert color="green" icon={<IconCheck size="1rem" />} title={sent.status === 'deployed' ? `Deployed to ${link?.name}` : `Sent to ${link?.name}`}>
            {sent.status === 'deployed'
              ? `${workflowName} is live in ${link?.name}.`
              : `An administrator of ${link?.name} has to approve it. They see it under Promotions there.`}
          </Alert>
          <Group justify="flex-end"><Button onClick={close}>Close</Button></Group>
        </Stack>
      ) : (
        <Stack gap="md">
          <Group grow align="flex-start">
            <Select
              label="To"
              data={(links ?? []).map((l) => ({ value: l.id, label: l.name }))}
              value={linkId}
              allowDeselect={false}
              onChange={setLinkId}
            />
            <Select
              label="Version"
              data={[
                { value: '0', label: 'Latest' },
                ...versions.map((v) => ({
                  value: String(v.version),
                  label: `v${v.version}${v.created_at ? ` · ${formatDateTime(v.created_at)}` : ''}`,
                })),
              ]}
              value={version}
              allowDeselect={false}
              onChange={(v) => setVersion(v ?? '0')}
            />
          </Group>

          {preview.isLoading && <Group gap="xs"><Loader size="xs" /><Text size="sm">Checking {link?.name}…</Text></Group>}
          {preview.error && <Alert color="red" title="Could not run the checks">{(preview.error as Error).message}</Alert>}
          {preview.data && (
            <>
              <Stack gap={6}>
                <Text fw={600} size="sm">Checks</Text>
                <List spacing={6} size="sm" center>
                  {preview.data.checks.map((c) => <CheckItem key={c.name} check={c} />)}
                </List>
              </Stack>
              <Stack gap={6}>
                <Text fw={600} size="sm">What changes in {link?.name}</Text>
                {(preview.data.changes ?? []).length === 0 ? (
                  <Text size="sm" c="dimmed">Nothing: {link?.name} already runs this version.</Text>
                ) : (
                  <Stack gap={4}>
                    {(preview.data.changes ?? []).map((c, i) => (
                      <Group key={i} gap="xs" wrap="nowrap">
                        <Badge size="sm" variant="light" color={changeColor[c.kind]}>{c.kind}</Badge>
                        <Text size="sm">{c.what}</Text>
                      </Group>
                    ))}
                  </Stack>
                )}
              </Stack>
            </>
          )}

          <Textarea
            label="Note for the approver"
            placeholder="What changed and why"
            autosize
            minRows={2}
            maxLength={1000}
            value={note}
            onChange={(e) => setNote(e.currentTarget.value)}
          />
          <Switch label={`Start it in ${link?.name ?? 'the other instance'} once deployed`} checked={start} onChange={(e) => setStart(e.currentTarget.checked)} />
          <Switch
            label="Roll back automatically if it starts failing"
            checked={rollback}
            onChange={(e) => setAutoRollback(e.currentTarget.checked)}
          />
          {send.error && <Alert color="red" title="Not sent">{(send.error as Error).message}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={close}>Cancel</Button>
            <Button
              leftSection={<IconRocket size="1rem" />}
              disabled={!preview.data?.ready}
              loading={send.isPending}
              onClick={() => send.mutate()}
            >
              {direct ? `Deploy to ${link?.name}` : 'Send for approval'}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

function CheckItem({ check }: { check: Check }) {
  const color = check.ok ? 'green' : check.blocking ? 'red' : 'yellow'
  const Icon = check.ok ? IconCheck : check.blocking ? IconX : IconAlertTriangle
  return (
    <List.Item icon={<ThemeIcon size={20} radius="xl" color={color} variant="light"><Icon size="0.8rem" /></ThemeIcon>}>
      <Text size="sm" span>{check.name}</Text>
      {check.detail && <Text size="xs" c="dimmed">{check.detail}</Text>}
    </List.Item>
  )
}
