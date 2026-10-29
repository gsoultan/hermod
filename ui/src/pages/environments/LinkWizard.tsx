import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Divider, Group, Modal, Select, Stack, Stepper, Text, TextInput } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { IconExternalLink, IconLink } from '@tabler/icons-react'
import {
  acceptUrl, createLink, environmentKeys, formatPairingCode, isPairingMessage, normalizeUrl, urlProblem, useVHostNames,
  vhostNameProblem, type Link,
} from '@/lib/environments'
import { ConnectionMatching } from './ConnectionMatching'

interface LinkWizardProps {
  opened: boolean
  onClose: () => void
}

/**
 * Connects one vhost here to the install it promotes to, in three steps:
 * where it is and which vhost there the workflows land in, an administrator
 * there approves (by signing in, or by reading out a code), and which
 * connections there stand in for the ones here. The other install creates
 * the vhost when it does not have it yet.
 */
export function LinkWizard({ opened, onClose }: LinkWizardProps) {
  const queryClient = useQueryClient()
  const [step, setStep] = useState(0)
  const [url, setUrl] = useState('')
  const [touched, setTouched] = useState(false)
  const [code, setCode] = useState('')
  const [from, setFrom] = useState<string | null>(null)
  const [landIn, setLandIn] = useState<string | null>(null)
  const [link, setLink] = useState<Link | null>(null)
  const popup = useRef<Window | null>(null)

  const problem = urlProblem(url)
  const target = problem ? '' : normalizeUrl(url)
  const host = target ? new URL(target).host : ''
  const { data: vhosts } = useVHostNames(opened)
  // Lands in the vhost of the same name unless someone says otherwise.
  const there = (landIn ?? from ?? '').trim()
  const thereProblem = from ? vhostNameProblem(there) : null

  const link_ = useMutation({
    mutationFn: (c: string) => createLink(target, c, from ?? ''),
    onSuccess: async (l) => {
      setLink(l)
      setStep(2)
      await queryClient.invalidateQueries({ queryKey: environmentKeys.links })
    },
  })

  // The accept page on the other install posts the code back to this window.
  useEffect(() => {
    if (!opened || step !== 1 || !target) return
    const expected = new URL(target).origin
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== expected || !isPairingMessage(e.data)) return
      const c = formatPairingCode(e.data.code)
      setCode(c)
      popup.current?.close()
      link_.mutate(c)
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
    // link_.mutate is stable; re-subscribing on every render would drop messages.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened, step, target])

  const close = () => {
    popup.current?.close()
    setStep(0)
    setUrl('')
    setTouched(false)
    setCode('')
    setFrom(null)
    setLandIn(null)
    setLink(null)
    link_.reset()
    onClose()
  }

  const openSignIn = () => {
    popup.current = window.open(acceptUrl(target, window.location.origin, there), 'hermod-link', 'popup,width=520,height=700')
  }

  return (
    <Modal opened={opened} onClose={close} title="Connect an environment" size="lg">
      <Stepper active={step} size="sm" allowNextStepsSelect={false}>
        <Stepper.Step label="Where" description="Address and virtual host">
          <Stack mt="md">
            <Text size="sm">
              Enter the address of the Hermod that workflows from here should be promoted to, usually your production
              install, and the vhost whose workflows go there. Each vhost is connected on its own.
            </Text>
            <TextInput
              label="Address"
              placeholder="https://hermod.example.com"
              value={url}
              onChange={(e) => setUrl(e.currentTarget.value)}
              onBlur={() => setTouched(true)}
              error={touched && url ? problem : null}
              data-autofocus
            />
            <Group grow align="flex-start">
              <Select
                label="Workflows from"
                description="The virtual host here"
                placeholder="Pick a virtual host"
                data={vhosts ?? []}
                value={from}
                searchable
                allowDeselect={false}
                onChange={setFrom}
              />
              <TextInput
                label="Land in"
                description={host ? `The vhost in ${host}` : 'The vhost there'}
                placeholder="Same name"
                value={there}
                maxLength={64}
                disabled={!from}
                error={thereProblem}
                onChange={(e) => setLandIn(e.currentTarget.value)}
              />
            </Group>
            <Text size="xs" c="dimmed">If it has no virtual host of that name yet, it creates one when its administrator allows the link.</Text>
            <Group justify="flex-end">
              <Button variant="default" onClick={close}>Cancel</Button>
              <Button disabled={!!problem || !from || !!thereProblem} onClick={() => setStep(1)}>Next</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Approve" description={`An admin of ${host || 'it'}`}>
          <Stack mt="md">
            <Text size="sm">
              An administrator of <b>{host}</b> has to approve the link. If that is you, sign in there and click
              Allow; this window fills in the rest.
            </Text>
            <Button leftSection={<IconExternalLink size="1rem" />} onClick={openSignIn} loading={link_.isPending}>
              Sign in to {host} and approve
            </Button>
            <Divider label="or use a pairing code" labelPosition="center" />
            <Text size="sm" c="dimmed">
              Someone else can create a code in {host} under Settings › Environments › Create pairing code, and read it
              to you. A code works once, for 10 minutes, and its workflows land in the vhost it was made for.
            </Text>
            <Group align="flex-end">
              <TextInput
                label="Pairing code"
                placeholder="ABCD-EFGH"
                value={code}
                onChange={(e) => setCode(formatPairingCode(e.currentTarget.value))}
                styles={{ input: { fontFamily: 'monospace', letterSpacing: 2 } }}
                style={{ flex: 1 }}
              />
              <Button
                leftSection={<IconLink size="1rem" />}
                disabled={code.replace('-', '').length !== 8}
                loading={link_.isPending}
                onClick={() => link_.mutate(code)}
              >
                Link
              </Button>
            </Group>
            {link_.error && (
              <Alert color="red" title="Not linked">{(link_.error as Error).message}</Alert>
            )}
            <Group justify="flex-start">
              <Button variant="subtle" onClick={() => setStep(0)}>Back</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Connections" description="Match them up">
          {link && (
            <Stack mt="md">
              <Alert color="green" title={`Connected to ${link.name}`}>
                Workflows in {link.vhost} can now be promoted to {link.name}, into {link.target_vhost || link.vhost}
                {link.vhost_created ? `, which it created for them` : ''}. Last, check which connection there stands in
                for each one here.
              </Alert>
              <ConnectionMatching link={link} onDone={close} />
            </Stack>
          )}
        </Stepper.Step>
      </Stepper>
    </Modal>
  )
}
