import '@testing-library/jest-dom'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

export const server = setupServer(
  http.get('/api/config/status', () =>
    HttpResponse.json({ configured: true, user_setup: true })
  ),
)

// Node exposes an experimental, disabled `localStorage` global that shadows
// jsdom's implementation, leaving `localStorage.setItem` undefined in tests.
// Install a small in-memory polyfill so persisted auth/session logic works.
class MemoryStorage implements Storage {
  private store = new Map<string, string>()
  get length() { return this.store.size }
  clear() { this.store.clear() }
  getItem(key: string) { return this.store.has(key) ? this.store.get(key)! : null }
  key(index: number) { return Array.from(this.store.keys())[index] ?? null }
  removeItem(key: string) { this.store.delete(key) }
  setItem(key: string, value: string) { this.store.set(key, String(value)) }
}

function installStorage(name: 'localStorage' | 'sessionStorage') {
  const current = (globalThis as any)[name]
  if (current && typeof current.setItem === 'function') return
  const storage = new MemoryStorage()
  Object.defineProperty(globalThis, name, { configurable: true, value: storage })
  if (typeof window !== 'undefined') {
    Object.defineProperty(window, name, { configurable: true, value: storage })
  }
}

installStorage('localStorage')
installStorage('sessionStorage')

// jsdom does not implement matchMedia; Mantine uses it to detect color scheme
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {}, // deprecated
    removeListener: () => {}, // deprecated
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})

// Stub ResizeObserver used by Mantine ScrollArea and other components
class NoopResizeObserver {
  constructor(_callback: ResizeObserverCallback) {}
  observe(_target: Element) {}
  unobserve(_target: Element) {}
  disconnect() {}
}
;(globalThis as any).ResizeObserver = NoopResizeObserver as any

// Stub WebSocket to avoid real connections during tests
class NoopWebSocket {
  url: string
  readyState = 1
  onopen: ((ev: any) => any) | null = null
  onmessage: ((ev: any) => any) | null = null
  onerror: ((ev: any) => any) | null = null
  onclose: ((ev: any) => any) | null = null
  constructor(url: string) {
    this.url = url
    setTimeout(() => this.onopen && this.onopen({} as any), 0)
  }
  send(_data: any) {}
  close() { this.readyState = 3 }
  addEventListener() {}
  removeEventListener() {}
}
;(globalThis as any).WebSocket = NoopWebSocket as any

beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
