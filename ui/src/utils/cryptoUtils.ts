export function generateStrongPassword(len = 20) {
  const bytes = new Uint8Array(len)
  if (typeof window !== 'undefined' && window.crypto && window.crypto.getRandomValues) {
    window.crypto.getRandomValues(bytes)
  } else {
    for (let i = 0; i < len; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  // base64url without padding
  const b64 = btoa(String.fromCharCode(...Array.from(bytes)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '')
  // Trim to desired length range 16-40
  const target = Math.max(16, Math.min(40, len))
  return b64.slice(0, target)
}

export function copyToClipboard(text: string) {
  if (typeof navigator !== 'undefined' && navigator.clipboard) {
    navigator.clipboard.writeText(text);
    return true;
  }
  return false;
}
