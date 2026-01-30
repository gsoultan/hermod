import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Stack, PasswordInput, Select, MultiSelect } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useNavigate } from '@tanstack/react-router';

export type Role = 'Administrator' | 'Editor' | 'Viewer';

interface User {
  id?: string;
  username: string;
  full_name: string;
  email: string;
  role: Role;
  vhosts: string[];
  password?: string;
}

interface UserFormProps {
  initialData?: User;
  isEditing?: boolean;
}

export function UserForm({ initialData, isEditing = false }: UserFormProps) {
  const navigate = useNavigate();
  const [user, setUser] = useState<User>({ 
    username: '', 
    password: '', 
    full_name: '', 
    email: '', 
    role: 'Viewer', 
    vhosts: [] 
  });

  useEffect(() => {
    if (initialData) {
      setUser({
        ...initialData,
        username: initialData.username || '',
        full_name: initialData.full_name || '',
        email: initialData.email || '',
        role: initialData.role || 'Viewer',
        vhosts: initialData.vhosts || [],
        password: '', // Don't populate password field
      });
    }
  }, [initialData]);

  const { data: vhostsResponse } = useSuspenseQuery<any>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch('/api/vhosts');
      if (!res.ok) throw new Error('Failed to fetch vhosts');
      return res.json();
    }
  });

  const vhosts = Array.isArray(vhostsResponse?.data) ? vhostsResponse.data : [];

  const submitMutation = useMutation({
    mutationFn: async (userData: User) => {
      const res = await apiFetch(`/api/users${isEditing ? `/${initialData?.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(userData)
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `Failed to ${isEditing ? 'update' : 'create'} user`);
      }
      return res.json();
    },
    onSuccess: () => {
      navigate({ to: '/users' });
    }
  });

  return (
    <Stack gap="md">
      <TextInput
        label="Username"
        required
        value={user.username}
        onChange={(e) => setUser({ ...user, username: e.currentTarget.value })}
      />
      {!isEditing && (
        <PasswordInput
          label="Password"
          required
          value={user.password}
          onChange={(e) => setUser({ ...user, password: e.currentTarget.value })}
        />
      )}
      <TextInput
        label="Full Name"
        value={user.full_name}
        onChange={(e) => setUser({ ...user, full_name: e.currentTarget.value })}
      />
      <TextInput
        label="Email"
        value={user.email}
        onChange={(e) => setUser({ ...user, email: e.currentTarget.value })}
      />
      <Select
        label="Role"
        data={['Administrator', 'Editor', 'Viewer']}
        value={user.role}
        onChange={(value) => setUser({ ...user, role: value as Role })}
      />
      <MultiSelect
        label="Assigned VHosts"
        placeholder="Pick vhosts"
        data={vhosts?.map((v: { name: string }) => v.name) || []}
        value={user.vhosts}
        maxValues={user.role === 'Administrator' ? undefined : 1}
        onChange={(value) => setUser({ ...user, vhosts: value })}
      />
      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/users' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(user)} loading={submitMutation.isPending}>
          {isEditing ? "Save Changes" : "Create User"}
        </Button>
      </Group>
    </Stack>
  );
}
