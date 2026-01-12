import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Divider, MultiSelect, Paper, Text } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { TransformationManager } from './TransformationManager';
import { IconArrowsDiff } from '@tabler/icons-react';

const API_BASE = '/api';

interface ConnectionFormProps {
  initialData?: any;
  isEditing?: boolean;
}

export function ConnectionForm({ initialData, isEditing = false }: ConnectionFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [conn, setConn] = useState<any>({ 
    name: '', 
    vhost: '', 
    source_id: '', 
    sink_ids: [],
    worker_id: '',
    transformation_ids: [],
    transformations: []
  });

  useEffect(() => {
    if (initialData) {
      setConn({
        ...initialData,
        transformation_ids: initialData.transformation_ids || [],
        transformations: initialData.transformations || [],
      });
    }
  }, [initialData]);

  const { data: vhosts } = useSuspenseQuery<any[]>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const { data: workers } = useSuspenseQuery<any[]>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const { data: sources } = useSuspenseQuery({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    }
  });

  const { data: sinks } = useSuspenseQuery({
    queryKey: ['sinks'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    }
  });

  const { data: transformations } = useSuspenseQuery<any[]>({
    queryKey: ['transformations'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations`);
      return res.json();
    }
  });

  const availableVHostsList = role === 'Administrator' 
    ? (vhosts || []).map(v => v.name)
    : availableVHosts;

  const availableSources = sources?.filter((s: any) => 
    !s.vhost || s.vhost === conn.vhost || conn.vhost === ''
  );

  const availableSinks = sinks?.filter((s: any) => 
    !s.vhost || s.vhost === conn.vhost || conn.vhost === ''
  );

  const submitMutation = useMutation({
    mutationFn: async (c: any) => {
      const res = await apiFetch(`${API_BASE}/connections${isEditing ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(c),
      });
      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || `Failed to ${isEditing ? 'update' : 'create'} connection`);
      }
      return res.json();
    },
    onSuccess: () => {
      navigate({ to: '/connections' });
    }
  });


  return (
    <Stack>
      <TextInput 
        label="Name" 
        placeholder="Production Connection" 
        value={conn.name}
        onChange={(e) => setConn({ ...conn, name: e.target.value })}
        required
      />
      <Select 
        label="VHost" 
        placeholder="Select a virtual host" 
        data={availableVHostsList}
        value={conn.vhost}
        onChange={(val) => setConn({ ...conn, vhost: val || '' })}
        required
      />
      <Select 
        label="Worker (Optional)" 
        placeholder="Assign to a specific worker" 
        data={(workers || []).map(w => ({ value: w.id, label: w.name || w.id }))}
        value={conn.worker_id}
        onChange={(val) => setConn({ ...conn, worker_id: val || '' })}
        clearable
      />
      <Select 
        label="Source" 
        placeholder="Pick a source"
        data={availableSources?.map((s: any) => ({ value: s.id, label: s.name }))}
        value={conn.source_id}
        onChange={(val) => setConn({ ...conn, source_id: val || '', name: conn.name || `Conn ${sources?.find((s:any)=>s.id===val)?.name}` })}
        required
      />
      <MultiSelect 
        label="Sinks" 
        placeholder="Pick one or more sinks"
        data={availableSinks?.map((s: any) => ({ value: s.id, label: s.name }))}
        value={conn.sink_ids}
        onChange={(val) => setConn({ ...conn, sink_ids: val })}
        required
      />

      <Paper withBorder p="md" radius="md" mt="md">
        <Stack gap="sm">
          <Group gap="xs" mb="xs">
            <IconArrowsDiff size="1.2rem" />
            <Text fw={600}>Message Transformations</Text>
          </Group>

          <MultiSelect 
            label="Predefined Transformations" 
            placeholder="Select reusable transformations"
            data={(transformations || []).map((t: any) => ({ value: t.id, label: t.name }))}
            value={conn.transformation_ids}
            onChange={(val) => setConn({ ...conn, transformation_ids: val })}
            clearable
          />

          <Divider label="Custom Inline Pipeline" labelPosition="center" my="sm" />
          
          <TransformationManager 
            title="Inline Steps" 
            transformations={conn.transformations} 
            onChange={(next) => setConn({ ...conn, transformations: next })}
          />
        </Stack>
      </Paper>

      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/connections' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(conn)} loading={submitMutation.isPending} disabled={!conn.name || !conn.vhost || !conn.source_id || !conn.sink_ids || conn.sink_ids.length === 0}>
          {isEditing ? 'Save Changes' : 'Create Connection'}
        </Button>
      </Group>
    </Stack>
  );
}
