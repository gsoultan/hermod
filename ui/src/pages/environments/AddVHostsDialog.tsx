import { useState } from 'react'
import { Alert, Button, Checkbox, Group, Loader, Modal, Stack, Text, TextInput } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { addVHosts, environmentKeys, useVHostNames, vhostNameProblem, type Link } from '@/lib/environments'

interface AddVHostsDialogProps {
  opened: boolean
  onClose: () => void
  /** A connected vhost of the server; its token asks for the new ones. */
  via: Link | null
  /** The vhosts here that already promote somewhere. */
  taken: string[]
}

/**
 * Adds vhosts to a server this instance is already connected to. No pairing
 * code: the server either connects each at once, or holds it until an
 * administrator there allows it.
 */
export function AddVHostsDialog({ opened, onClose, via, taken }: AddVHostsDialogProps) {
  return (
    <Modal opened={opened && !!via} onClose={onClose} title={via ? `Add vhosts to ${via.name}` : ''} size="lg">
      {opened && via && <AddForm via={via} taken={taken} onClose={onClose} />}
    </Modal>
  )
}

function AddForm({ via, taken, onClose }: { via: Link; taken: string[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const { data: names, isLoading } = useVHostNames()
  const [picked, setPicked] = useState<Record<string, string>>({})
  const free = (names ?? []).filter((n) => !taken.includes(n))
  const routes = Object.entries(picked).map(([vhost, there]) => ({ vhost, target_vhost: there.trim() || vhost }))
  const problem = routes.map((r) => vhostNameProblem(r.target_vhost)).find(Boolean)

  const add = useMutation({
    mutationFn: () => addVHosts(via.id, routes),
    onSettled: () => queryClient.invalidateQueries({ queryKey: environmentKeys.links }),
  })

  if (add.data) {
    const waiting = add.data.filter((l) => l.pending)
    return (
      <Stack>
        <Alert color="green" title={`${add.data.length - waiting.length} connected`}>
          {add.data.filter((l) => !l.pending).map((l) => l.vhost).join(', ') || 'None yet.'}
        </Alert>
        {waiting.map((l) => (
          <Alert key={l.id} color="yellow">
            {l.vhost} waits for an administrator in {via.name} to allow it. It connects on its own once they do.
          </Alert>
        ))}
        <Group justify="flex-end"><Button onClick={onClose}>Done</Button></Group>
      </Stack>
    )
  }

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        No pairing code needed: {via.name} already trusts this instance. Pick the vhosts here whose workflows should go
        there. Each lands in a vhost of the same name unless you change it, and {via.name} creates it if it is missing.
      </Text>
      {isLoading && <Loader size="sm" />}
      {!isLoading && free.length === 0 && <Text size="sm">Every vhost here is already connected.</Text>}
      {free.map((n) => (
        <Group key={n} wrap="nowrap" align="center" gap="sm">
          <Checkbox
            label={n}
            checked={n in picked}
            onChange={(e) => {
              const on = e.currentTarget.checked
              setPicked((p) => {
                const next = { ...p }
                if (on) next[n] = n
                else delete next[n]
                return next
              })
            }}
            style={{ flex: 1 }}
          />
          <TextInput
            aria-label={`${n} lands in`}
            size="xs"
            w={200}
            disabled={!(n in picked)}
            value={picked[n] ?? n}
            maxLength={64}
            error={n in picked ? vhostNameProblem(picked[n].trim() || n) : null}
            onChange={(e) => {
              const v = e.currentTarget.value
              setPicked((p) => ({ ...p, [n]: v }))
            }}
          />
        </Group>
      ))}
      {add.error && <Alert color="red" title="Not added">{(add.error as Error).message}</Alert>}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>Cancel</Button>
        <Button disabled={routes.length === 0 || !!problem} loading={add.isPending} onClick={() => add.mutate()}>
          Add {routes.length} vhost{routes.length === 1 ? '' : 's'}
        </Button>
      </Group>
    </Stack>
  )
}
