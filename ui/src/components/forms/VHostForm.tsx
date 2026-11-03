import { useState, useEffect, useRef } from 'react';
import { Alert, Button, Group, TextInput, Stack, Textarea } from '@mantine/core';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useNavigate } from '@tanstack/react-router';

interface VHost {
  id?: string;
  name: string;
  description: string;
}

interface VHostFormProps {
  initialData?: VHost;
  isEditing?: boolean;
}

export function VHostForm({ initialData, isEditing = false }: VHostFormProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [vhost, setVHost] = useState<VHost>({ name: '', description: '' });

  // Hydrate once per record, keyed on its id rather than the object's identity.
  // React Query hands the edit page a fresh object whenever the record
  // refetches — after this form's own save, or when the list is invalidated —
  // and an effect keyed on the object overwrote whatever had been typed since.
  const hydratedFor = useRef<string | null>(null);
  useEffect(() => {
    if (initialData) {
      const key = initialData.id || 'new';
      if (hydratedFor.current === key) return;
      hydratedFor.current = key;
      setVHost(initialData);
    }
  }, [initialData]);

  const renaming = isEditing && !!initialData && vhost.name.trim() !== initialData.name;

  const submitMutation = useMutation({
    mutationFn: async (vhostData: VHost) => {
      const res = await apiFetch(`/api/vhosts${isEditing ? `/${initialData?.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(vhostData)
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `Failed to ${isEditing ? 'update' : 'create'} vhost`);
      }
      return res.json();
    },
    onSuccess: () => {
      try {
        // A rename moves every workflow, source, sink and user of the vhost to
        // the new name, so any list on the page may be holding the old one.
        if (renaming) {
          queryClient.invalidateQueries();
        } else {
          queryClient.invalidateQueries({ queryKey: ['vhosts'] });
        }
      } catch {
        // ignore
      }
      navigate({ to: '/vhosts' });
    }
  });

  return (
    <Stack gap="md">
      <TextInput
        label="Name"
        placeholder="e.g. production, staging"
        required
        value={vhost.name}
        onChange={(e) => setVHost({ ...vhost, name: e.currentTarget.value })}
        description={renaming
          ? `Its workflows, sources, sinks and users, and its secrets and ML data, move from "${initialData?.name}" to the new name. Running workflows restart under it.`
          : undefined}
      />
      <Textarea
        label="Description"
        placeholder="What is this virtual host for?"
        value={vhost.description}
        onChange={(e) => setVHost({ ...vhost, description: e.currentTarget.value })}
      />
      {submitMutation.error && (
        <Alert color="red" variant="light">{submitMutation.error.message}</Alert>
      )}
      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/vhosts' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(vhost)} loading={submitMutation.isPending}>
          {isEditing ? "Save changes" : "Create virtual host"}
        </Button>
      </Group>
    </Stack>
  );
}
