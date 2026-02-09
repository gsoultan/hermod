import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Stack, Textarea } from '@mantine/core';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
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

  useEffect(() => {
    if (initialData) {
      setVHost(initialData);
    }
  }, [initialData]);

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
        queryClient.invalidateQueries({ queryKey: ['vhosts'] });
      } catch (_) {
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
      />
      <Textarea
        label="Description"
        placeholder="What is this vhost for?"
        value={vhost.description}
        onChange={(e) => setVHost({ ...vhost, description: e.currentTarget.value })}
      />
      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/vhosts' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(vhost)} loading={submitMutation.isPending}>
          {isEditing ? "Save Changes" : "Create VHost"}
        </Button>
      </Group>
    </Stack>
  );
}
