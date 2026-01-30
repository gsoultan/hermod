// Simple auth storage abstraction to prepare for HttpOnly cookie migration
// Current implementation uses localStorage for backward compatibility.

const TOKEN_KEY = 'hermod_token';

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    // ignore write errors (e.g., private mode)
  }
}

export function removeToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    // ignore
  }
}
