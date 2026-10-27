import { useState } from 'react'
import { Alert, Button, Center, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { useParams } from '@tanstack/react-router'
import { IconAlertTriangle, IconCheck, IconLink } from '@tabler/icons-react'
import { getSessionRole } from '@/auth/session'
import { decodeOrigin, newPairingCode, PAIRING_MESSAGE, type PairingMessage } from '@/lib/environments'

/**
 * Opened by another install's link wizard: an administrator here allows that
 * install to promote workflows into this one. The code goes back to the
 * window that opened this one, and only to the address shown on screen.
 */
export function AcceptLinkPage() {
  const { from } = useParams({ strict: false }) as { from?: string }
  const origin = from ? decodeOrigin(from) : null
  const isAdmin = getSessionRole() === 'Administrator'
  const [sent, setSent] = useState(false)

  const allow = useMutation({
    mutationFn: () => newPairingCode(origin ?? ''),
    onSuccess: ({ code }) => {
      if (window.opener && origin) {
        const msg: PairingMessage = { type: PAIRING_MESSAGE, code }
        window.opener.postMessage(msg, origin)
        setSent(true)
        window.setTimeout(() => window.close(), 1500)
      }
    },
  })

  return (
    <Center mih="70vh" p="md">
      <Paper withBorder p="xl" radius="md" maw={520} w="100%">
        <Stack gap="md">
          <Group gap="xs"><IconLink size="1.4rem" aria-hidden /><Title order={3}>Link an environment</Title></Group>
          {!origin ? (
            <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="This link is broken">
              Start again from Settings › Environments in the instance you want to link.
            </Alert>
          ) : !isAdmin ? (
            <Alert color="yellow" title="An administrator has to approve this">
              You are signed in without administrator rights. Ask an administrator of this instance to approve the link,
              or to create a pairing code for you.
            </Alert>
          ) : allow.data ? (
            <Stack gap="xs">
              <Alert color="green" icon={<IconCheck size="1rem" />} title={sent ? 'Allowed' : 'Pairing code'}>
                {sent
                  ? 'The other instance received the code and is finishing the link. This window closes on its own.'
                  : 'Enter this code in the other instance. It works once, for 10 minutes.'}
              </Alert>
              <Text ff="monospace" fz={28} fw={700} ta="center" style={{ letterSpacing: 4 }}>{allow.data.code}</Text>
            </Stack>
          ) : (
            <>
              <Text>
                <b>{origin}</b> wants to promote workflows to this instance.
              </Text>
              <Text size="sm" c="dimmed">
                Each workflow it sends waits here until an administrator approves it, unless the rules in Settings ›
                Environments say otherwise. You can revoke the link at any time.
              </Text>
              <Alert color="yellow" variant="light">Only allow this if you opened it from {origin} yourself.</Alert>
              {allow.error && <Alert color="red">{(allow.error as Error).message}</Alert>}
              <Group justify="flex-end">
                <Button variant="default" onClick={() => window.close()}>Cancel</Button>
                <Button onClick={() => allow.mutate()} loading={allow.isPending}>Allow</Button>
              </Group>
            </>
          )}
        </Stack>
      </Paper>
    </Center>
  )
}
