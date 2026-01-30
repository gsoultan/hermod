import { Title, Paper, Stack, Group, Box, Text, Center, Loader, Button, Modal, PasswordInput } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { useState } from 'react';
import { IconUsers, IconLock } from '@tabler/icons-react';
import { UserForm } from '../components/UserForm';
import { useSuspenseQuery, useMutation } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';

export function EditUserPage() {
  const { userId } = useParams({ from: '/users/$userId/edit' });
  const [opened, { open, close }] = useDisclosure(false);
  const [newPassword, setNewPassword] = useState('');

  const { data: user, isLoading } = useSuspenseQuery({
    queryKey: ['users', userId],
    queryFn: async () => {
      const res = await apiFetch(`/api/users/${userId}`);
      if (!res.ok) throw new Error('Failed to fetch user');
      return res.json();
    }
  });

  const changePasswordMutation = useMutation({
    mutationFn: async (password: string) => {
      const res = await apiFetch(`/api/users/${userId}/password`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password })
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to change password');
      }
      return res.json();
    },
    onSuccess: () => {
      close();
      setNewPassword('');
    }
  });

  if (isLoading) {
    return (
      <Center h="100vh">
        <Loader size="xl" />
      </Center>
    );
  }

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group justify="space-between">
            <Group gap="sm">
              <IconUsers size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box>
                <Title order={2} fw={800}>Edit User: {user?.username}</Title>
                <Text size="sm" c="dimmed">Update user profile, role, and permissions.</Text>
              </Box>
            </Group>
            <Button leftSection={<IconLock size={16} />} variant="light" color="orange" onClick={open}>
              Change Password
            </Button>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <UserForm initialData={user} isEditing />
        </Paper>
      </Stack>

      <Modal opened={opened} onClose={close} title="Change Password" centered>
        <Stack gap="md">
          <PasswordInput
            label="New Password"
            placeholder="Enter new password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.currentTarget.value)}
            required
          />
          <Group justify="flex-end" mt="md">
            <Button variant="outline" onClick={close}>Cancel</Button>
            <Button 
              color="orange" 
              onClick={() => changePasswordMutation.mutate(newPassword)}
              loading={changePasswordMutation.isPending}
              disabled={!newPassword}
            >
              Update Password
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Box>
  );
}
