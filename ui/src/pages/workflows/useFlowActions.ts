import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { downloadWorkflowExport } from '@/utils/workflowExport';
import { useConfirm } from '@/components/common/ConfirmProvider';

/**
 * Export JSON, Clone and Delete for one flow. The list, the read-only view
 * and the editor all offer the same three, so they share the one
 * implementation and the same words.
 */
export function useFlowActions(id: string | undefined, name: string | undefined) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const label = name || 'The flow';

  const exportJson = async () => {
    if (!id) return;
    try {
      const res = await apiFetch(`/api/workflows/${id}/export`);
      const { warning } = await downloadWorkflowExport(res, name || id);
      if (warning) {
        notifications.show({ title: 'Exported with missing dependencies', message: warning, color: 'yellow', autoClose: false });
      } else {
        notifications.show({ title: 'Exported', message: `${label} was saved as JSON.`, color: 'green' });
      }
    } catch (err: any) {
      notifications.show({ title: 'Export failed', message: err.message, color: 'red' });
    }
  };

  const cloneMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`/api/workflows/${id}`);
      if (!res.ok) throw new Error(await res.text());
      // The identity and runtime fields are not copied; the three names exist only to be left out.
      const { id: _id, status: _status, active: _active, ...copy } = await res.json();
      copy.name = `${copy.name} (Copy)`;
      const created = await apiFetch('/api/workflows', { method: 'POST', body: JSON.stringify(copy) });
      if (!created.ok) throw new Error(await created.text());
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      notifications.show({ title: 'Cloned', message: `${label} (Copy) is in your flows.`, color: 'green' });
      navigate({ to: '/workflows' });
    },
    onError: (err: Error) => notifications.show({ title: 'Clone failed', message: err.message, color: 'red' }),
  });

  const deleteMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`/api/workflows/${id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(await res.text());
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      notifications.show({ title: 'Deleted', message: `${label} was deleted.`, color: 'green' });
      navigate({ to: '/workflows' });
    },
    onError: (err: Error) => notifications.show({ title: 'Delete failed', message: err.message, color: 'red' }),
  });

  const remove = async () => {
    if (!id) return;
    const ok = await confirm({
      title: `Delete ${label}`,
      message: 'The flow stops and its definition is removed. Runs, traces and logs already recorded stay where they are.',
      confirmLabel: 'Delete flow',
      danger: true,
    });
    if (!ok) return;
    deleteMutation.mutate();
  };

  return {
    exportJson,
    clone: () => { if (id) cloneMutation.mutate(); },
    remove,
    cloning: cloneMutation.isPending,
    deleting: deleteMutation.isPending,
  };
}
