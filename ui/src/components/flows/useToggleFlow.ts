import { useMutation, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';

/**
 * The switch on a flow row. Start and stop are the same call; the server
 * flips whatever the flow is now. Every workflows query is refreshed after,
 * so the list, Home and the sidebar badge agree.
 */
export function useToggleFlow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ id }: { id: string; name: string; active: boolean }) => {
      await apiFetch(`/api/workflows/${id}/toggle`, { method: 'POST' });
    },
    onSuccess: (_data, { name, active }) => {
      notifications.show({
        id: `flow-toggle-${name}`,
        message: active ? `Stopping ${name}` : `Starting ${name}`,
        color: active ? 'gray' : 'green',
      });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] });
    },
  });
}
