import '@testing-library/jest-dom'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

export const server = setupServer(
  http.get('/api/config/status', () =>
    HttpResponse.json({ configured: true, user_setup: true })
  ),
)

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
