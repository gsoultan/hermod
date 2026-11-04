import { Switch } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { environmentKeys, setPeerDecide, type Peer } from '@/lib/environments'

/** Lets a linked instance's administrators approve, reject and undo what it sends, from there. */
export function PeerDecideSwitch({ peer }: { peer: Peer }) {
  const queryClient = useQueryClient()
  const change = useMutation({
    mutationFn: (decide: boolean) => setPeerDecide(peer.id, decide),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: environmentKeys.peers }),
    onError: (err: Error) => notifications.show({ color: 'red', title: 'Not changed', message: err.message }),
  })
  return (
    <Switch
      size="xs"
      label="Its admins approve from there"
      aria-label={`Let ${peer.name}'s administrators approve, reject and undo${peer.vhost ? ` in ${peer.vhost}` : ''} from there`}
      checked={change.isPending ? change.variables : !!peer.decide}
      disabled={change.isPending}
      onChange={(e) => change.mutate(e.currentTarget.checked)}
    />
  )
}
