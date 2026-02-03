import { useState, useEffect } from 'react';
import { Title, Paper, Stack, Group, Box, Text, TextInput, Button, Divider, Alert, Badge, Card, Avatar, Tabs, PasswordInput, Modal, Image, Code } from '@mantine/core';
import { IconUser, IconMail, IconShieldLock, IconDeviceFloppy, IconLock, IconCheck, IconAlertCircle, IconWorld, IconRefresh, IconFingerprint, IconTrash } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch, apiJson } from '../api';
import { notifications } from '@mantine/notifications';

interface User {
  id: string;
  username: string;
  full_name: string;
  email: string;
  role: string;
  vhosts: string[];
  two_factor_enabled: boolean;
}

export function ProfilePage() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<string | null>('info');
  const [twoFactorSecret, setTwoFactorSecret] = useState<{ secret: string; url: string } | null>(null);
  const [twoFactorCode, setTwoFactorCode] = useState('');

  const { data: user, isLoading, error } = useQuery<User>({
    queryKey: ['me'],
    queryFn: async () => {
      const res = await apiFetch('/api/me');
      return res.json();
    }
  });

  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');

  useEffect(() => {
    if (user) {
      setFullName(user.full_name || '');
      setEmail(user.email || '');
    }
  }, [user]);

  const updateProfileMutation = useMutation({
    mutationFn: async (data: { full_name: string; email: string }) => {
      await apiJson('/api/me', {
        method: 'PUT',
        body: JSON.stringify(data),
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['me'] });
      notifications.show({
        title: 'Success',
        message: 'Profile updated successfully',
        color: 'green',
        icon: <IconCheck size="1.1rem" />,
      });
    }
  });

  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [passwordError, setPasswordError] = useState('');

  const changePasswordMutation = useMutation({
    mutationFn: async (newPassword: string) => {
      if (!user) return;
      await apiJson(`/api/users/${user.id}/password`, {
        method: 'PUT',
        body: JSON.stringify({ password: newPassword }),
      });
    },
    onSuccess: () => {
      setPassword('');
      setConfirmPassword('');
      notifications.show({
        title: 'Success',
        message: 'Password changed successfully',
        color: 'green',
        icon: <IconCheck size="1.1rem" />,
      });
    }
  });

  const setup2FAMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/auth/2fa/setup', { method: 'POST' });
      return res.json();
    },
    onSuccess: (data) => {
      setTwoFactorSecret(data);
    }
  });

  const verify2FAMutation = useMutation({
    mutationFn: async (data: { secret: string; code: string }) => {
      await apiJson('/api/auth/2fa/verify', {
        method: 'POST',
        body: JSON.stringify(data),
      });
    },
    onSuccess: () => {
      setTwoFactorSecret(null);
      setTwoFactorCode('');
      queryClient.invalidateQueries({ queryKey: ['me'] });
      notifications.show({
        title: 'Success',
        message: 'Two-factor authentication enabled',
        color: 'green',
        icon: <IconCheck size="1.1rem" />,
      });
    }
  });

  const disable2FAMutation = useMutation({
    mutationFn: async () => {
      await apiFetch('/api/auth/2fa/disable', { method: 'POST' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['me'] });
      notifications.show({
        title: 'Success',
        message: 'Two-factor authentication disabled',
        color: 'blue',
        icon: <IconCheck size="1.1rem" />,
      });
    }
  });

  const generatePasswordMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/auth/generate-password', { method: 'POST' });
      return res.json();
    },
    onSuccess: (data) => {
      setPassword(data.password);
      setConfirmPassword(data.password);
      notifications.show({
        title: 'Generated',
        message: 'A strong password has been generated for you',
        color: 'blue',
      });
    }
  });

  if (isLoading) return <Text p="xl">Loading profile...</Text>;
  if (error) return <Alert m="xl" color="red" icon={<IconAlertCircle />}>Failed to load profile</Alert>;
  if (!user) return null;

  const handleUpdateProfile = (e: React.FormEvent) => {
    e.preventDefault();
    updateProfileMutation.mutate({ full_name: fullName, email });
  };

  const handleChangePassword = (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirmPassword) {
      setPasswordError('Passwords do not match');
      return;
    }
    if (password.length < 8) {
      setPasswordError('Password must be at least 8 characters long');
      return;
    }
    setPasswordError('');
    changePasswordMutation.mutate(password);
  };

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <Avatar size="lg" radius="xl" color="blue" variant="filled">
              {user.full_name?.charAt(0) || user.username.charAt(0)}
            </Avatar>
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>{user.full_name || user.username}</Title>
              <Text size="sm" c="dimmed">@{user.username} • {user.role}</Text>
            </Box>
          </Group>
        </Paper>

        <Tabs value={activeTab} onChange={setActiveTab} variant="outline" radius="md">
          <Tabs.List>
            <Tabs.Tab value="info" leftSection={<IconUser size="1rem" />}>Profile Info</Tabs.Tab>
            <Tabs.Tab value="security" leftSection={<IconShieldLock size="1rem" />}>Security</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="info" pt="xl">
            <Group align="flex-start" gap="xl">
              <Paper withBorder p="xl" radius="md" style={{ flex: 1 }}>
                <form onSubmit={handleUpdateProfile}>
                  <Stack gap="md">
                    <Title order={4}>General Information</Title>
                    <TextInput
                      label="Username"
                      value={user.username}
                      disabled
                      description="Username cannot be changed"
                    />
                    <TextInput
                      label="Full Name"
                      placeholder="Enter your full name"
                      value={fullName}
                      onChange={(e) => setFullName(e.currentTarget.value)}
                      leftSection={<IconUser size="1rem" />}
                    />
                    <TextInput
                      label="Email Address"
                      placeholder="your@email.com"
                      value={email}
                      onChange={(e) => setEmail(e.currentTarget.value)}
                      leftSection={<IconMail size="1rem" />}
                    />
                    <Button 
                      type="submit" 
                      loading={updateProfileMutation.isPending}
                      leftSection={<IconDeviceFloppy size="1.1rem" />}
                    >
                      Save Changes
                    </Button>
                  </Stack>
                </form>
              </Paper>

              <Stack gap="md" style={{ width: 300 }}>
                <Card withBorder radius="md" p="md">
                  <Stack gap="xs">
                    <Text fw={700} size="sm">Access & Roles</Text>
                    <Divider />
                    <Group justify="space-between">
                      <Text size="xs" c="dimmed">System Role</Text>
                      <Badge variant="light" color="blue">{user.role}</Badge>
                    </Group>
                    <Stack gap={4}>
                      <Group gap={4}>
                        <IconWorld size="0.8rem" color="gray" />
                        <Text size="xs" c="dimmed">Virtual Hosts</Text>
                      </Group>
                      <Group gap={4} wrap="wrap">
                        {user.vhosts?.length > 0 ? (
                          user.vhosts.map(v => (
                            <Badge key={v} variant="dot" size="sm">{v}</Badge>
                          ))
                        ) : (
                          <Text size="xs">None assigned</Text>
                        )}
                      </Group>
                    </Stack>
                  </Stack>
                </Card>
              </Stack>
            </Group>
          </Tabs.Panel>

          <Tabs.Panel value="security" pt="xl">
            <Paper withBorder p="xl" radius="md" maw={500}>
              <form onSubmit={handleChangePassword}>
                <Stack gap="md">
                  <Title order={4}>Change Password</Title>
                  <Text size="sm" c="dimmed">
                    Ensure your account is using a long, random password to stay secure.
                  </Text>
                  
                  <Group justify="space-between" align="flex-end">
                    <PasswordInput
                      label="New Password"
                      placeholder="Enter new password"
                      value={password}
                      onChange={(e) => setPassword(e.currentTarget.value)}
                      leftSection={<IconLock size="1rem" />}
                      error={passwordError}
                      style={{ flex: 1 }}
                    />
                    <Button 
                      variant="light" 
                      onClick={() => generatePasswordMutation.mutate()}
                      loading={generatePasswordMutation.isPending}
                      leftSection={<IconRefresh size="1rem" />}
                    >
                      Generate
                    </Button>
                  </Group>
                  
                  <PasswordInput
                    label="Confirm New Password"
                    placeholder="Confirm new password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.currentTarget.value)}
                    leftSection={<IconLock size="1rem" />}
                  />
                  
                  <Button 
                    type="submit" 
                    color="orange"
                    loading={changePasswordMutation.isPending}
                    leftSection={<IconShieldLock size="1.1rem" />}
                  >
                    Update Password
                  </Button>
                </Stack>
              </form>
            </Paper>

            <Paper withBorder p="xl" radius="md" maw={500} mt="lg">
              <Stack gap="md">
                <Group justify="space-between">
                  <Stack gap={0}>
                    <Title order={4}>Two-Factor Authentication (2FA)</Title>
                    <Text size="sm" c="dimmed">
                      Add an extra layer of security to your account using TOTP.
                    </Text>
                  </Stack>
                  <Badge color={user.two_factor_enabled ? 'green' : 'gray'} variant="light">
                    {user.two_factor_enabled ? 'Enabled' : 'Disabled'}
                  </Badge>
                </Group>

                <Divider />

                {user.two_factor_enabled ? (
                  <Stack gap="md">
                    <Alert color="blue" icon={<IconShieldLock size="1.2rem" />}>
                      Two-factor authentication is currently enabled for your account.
                    </Alert>
                    <Button 
                      color="red" 
                      variant="light" 
                      leftSection={<IconTrash size="1.1rem" />}
                      onClick={() => {
                        if (window.confirm('Are you sure you want to disable 2FA?')) {
                          disable2FAMutation.mutate();
                        }
                      }}
                      loading={disable2FAMutation.isPending}
                    >
                      Disable 2FA
                    </Button>
                  </Stack>
                ) : (
                  <Stack gap="md">
                    <Text size="sm">
                      Protect your account with an additional security layer. Once configured, you'll need to enter a code from your authenticator app to log in.
                    </Text>
                    <Button 
                      variant="outline" 
                      leftSection={<IconFingerprint size="1.1rem" />}
                      onClick={() => setup2FAMutation.mutate()}
                      loading={setup2FAMutation.isPending}
                    >
                      Enable 2FA
                    </Button>
                  </Stack>
                )}
              </Stack>
            </Paper>

            <Modal
              opened={twoFactorSecret !== null}
              onClose={() => setTwoFactorSecret(null)}
              title="Setup Two-Factor Authentication"
              size="md"
            >
              {twoFactorSecret && (
                <Stack gap="md">
                  <Text size="sm">
                    1. Scan this QR code with your authenticator app (like Google Authenticator, Authy, or Microsoft Authenticator):
                  </Text>
                  
                  <Box style={{ display: 'flex', justifyContent: 'center' }} p="md" bg="gray.1">
                    <Image 
                      src={`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(twoFactorSecret.url)}`}
                      width={200}
                      height={200}
                      alt="QR Code"
                    />
                  </Box>

                  <Text size="sm">
                    2. If you can't scan the QR code, enter this secret manually:
                  </Text>
                  <Code block p="md" style={{ fontSize: '1.2rem', textAlign: 'center' }}>
                    {twoFactorSecret.secret}
                  </Code>

                  <Text size="sm">
                    3. Enter the 6-digit code from your app to verify:
                  </Text>
                  <TextInput
                    placeholder="000000"
                    value={twoFactorCode}
                    onChange={(e) => setTwoFactorCode(e.currentTarget.value)}
                    maxLength={6}
                    size="lg"
                    style={{ textAlign: 'center' }}
                  />

                  <Button 
                    fullWidth 
                    onClick={() => verify2FAMutation.mutate({ 
                      secret: twoFactorSecret.secret, 
                      code: twoFactorCode 
                    })}
                    loading={verify2FAMutation.isPending}
                  >
                    Verify & Enable
                  </Button>
                </Stack>
              )}
            </Modal>
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Box>
  );
}
