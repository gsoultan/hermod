import { notifications } from '@mantine/notifications';

export function getRoleFromToken(): string | null {
  const token = localStorage.getItem('hermod_token');
  if (!token) return null;
  try {
    const base64Url = token.split('.')[1];
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
    const jsonPayload = decodeURIComponent(atob(base64).split('').map(function(c) {
      return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
    }).join(''));
    return JSON.parse(jsonPayload).role;
  } catch (e) {
    return null;
  }
}

export async function apiFetch(url: string, options: RequestInit = {}) {
  const token = localStorage.getItem('hermod_token');
  
  const headers = new Headers(options.headers);
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(url, {
    ...options,
    headers,
  });

  if (response.status === 401 && !url.includes('/api/login')) {
    localStorage.removeItem('hermod_token');
    const currentPath = window.location.pathname;
    if (currentPath !== '/login' && currentPath !== '/setup') {
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
      notifications.show({
        title: 'Error',
        message: errorMessage,
        color: 'red',
        autoClose: 5000,
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
