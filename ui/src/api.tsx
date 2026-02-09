import { notifications } from '@mantine/notifications';
import { getToken, removeToken } from './auth/storage';
import { IconAlertCircle, IconExternalLink } from '@tabler/icons-react';
import { Text, Group, Anchor, Stack } from '@mantine/core';

export function getClaimsFromToken(): any {
  const token = getToken();
  if (!token) return null;
  try {
    const base64Url = token.split('.')[1];
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
    const jsonPayload = decodeURIComponent(atob(base64).split('').map(function(c) {
      return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
    }).join(''));
    return JSON.parse(jsonPayload);
  } catch (e) {
    return null;
  }
}

export function getRoleFromToken(): string | null {
  return getClaimsFromToken()?.role || null;
}

export async function apiFetch(url: string, options: RequestInit = {}) {
  const token = getToken();
  
  const headers = new Headers(options.headers);
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(url, {
    ...options,
    headers,
    credentials: 'include',
    // Allow callers to pass AbortSignal for cancellation
    signal: options.signal,
  });

  if (response.status === 401 && !url.includes('/api/login')) {
    removeToken();
    const currentPath = window.location.pathname;
    if (currentPath !== '/login' && currentPath !== '/setup' && currentPath !== '/forgot-password') {
      window.location.href = `/login?redirect=${encodeURIComponent(currentPath)}`;
    } else {
      window.location.href = '/login';
    }
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    const errorMessage = data.error || response.statusText || 'An unexpected error occurred';
    
    // Only show notification if it's not a background check
    if (!url.includes('/api/config/status') && !url.includes('/api/vhosts')) {
      const isToggle = url.includes('/toggle');
      let workflowID = '';
      if (isToggle) {
        const parts = url.split('/');
        // URL is something like /api/workflows/ID/toggle
        const wfIndex = parts.indexOf('workflows');
        if (wfIndex !== -1 && parts[wfIndex + 1]) {
          workflowID = parts[wfIndex + 1];
        }
      }

      notifications.show({
        title: <Text fw={700}>System Interruption</Text>,
        message: (
          <Stack gap={4}>
            <Text size="sm">{errorMessage}</Text>
            {workflowID && (
              <Group gap={4} mt={4}>
                <IconExternalLink size="0.9rem" />
                <Anchor 
                  href={`/logs?workflow_id=${workflowID}`} 
                  underline="always"
                  size="xs"
                  fw={600}
                >
                  View Workflow Logs
                </Anchor>
              </Group>
            )}
          </Stack>
        ),
        color: 'red',
        icon: <IconAlertCircle size="1.1rem" />,
        autoClose: workflowID ? 10000 : 5000,
      });
    }
    
    // We still throw to allow the caller to handle it
    const error = new Error(errorMessage);
    (error as any).status = response.status;
    (error as any).data = data;
    throw error;
  }

  return response;
}

export async function apiJson<T>(url: string, options: RequestInit = {}) {
  const res = await apiFetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });
  return res.json() as Promise<T>;
}
