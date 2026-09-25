import { beforeAll, afterAll } from 'vitest'
import { render } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { FlowCanvas } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'

// React Flow measures a node before it draws an edge to it, and jsdom lays
// nothing out. These are the stand-ins React Flow's testing guide gives, plus
// the contentRect the pan-zoom extent reads, which the guide's version omits.

class MeasuringResizeObserver {
  private readonly callback: ResizeObserverCallback
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback
  }
  observe(target: Element) {
    const { offsetWidth: width, offsetHeight: height } = target as HTMLElement
    const entry = { target, contentRect: { width, height } } as unknown as ResizeObserverEntry
    this.callback([entry], this as unknown as ResizeObserver)
  }
  unobserve() {}
  disconnect() {}
}

class ScaleOnlyDOMMatrixReadOnly {
  m22: number
  constructor(transform?: string) {
    const scale = transform?.match(/scale\(([1-9.])\)/)?.[1]
    this.m22 = scale !== undefined ? +scale : 1
  }
}

/**
 * Installs the layout stand-ins for the calling test file only, and restores
 * jsdom's own afterwards. Call it once, at the top level of the file.
 */
export function installReactFlowLayoutShims(): void {
  const saved: Record<string, PropertyDescriptor | undefined> = {}

  beforeAll(() => {
    saved.ResizeObserver = Object.getOwnPropertyDescriptor(globalThis, 'ResizeObserver')
    saved.DOMMatrixReadOnly = Object.getOwnPropertyDescriptor(globalThis, 'DOMMatrixReadOnly')
    saved.offsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
    saved.offsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
    saved.getBBox = Object.getOwnPropertyDescriptor(SVGElement.prototype, 'getBBox')

    Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, writable: true, value: MeasuringResizeObserver })
    Object.defineProperty(globalThis, 'DOMMatrixReadOnly', { configurable: true, writable: true, value: ScaleOnlyDOMMatrixReadOnly })
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get(this: HTMLElement) { return parseFloat(this.style.height) || 1 },
    })
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      get(this: HTMLElement) { return parseFloat(this.style.width) || 1 },
    })
    Object.defineProperty(SVGElement.prototype, 'getBBox', {
      configurable: true,
      writable: true,
      value: () => ({ x: 0, y: 0, width: 0, height: 0 }),
    })
  })

  afterAll(() => {
    const restore = (target: object, key: string, descriptor: PropertyDescriptor | undefined) => {
      if (descriptor) Object.defineProperty(target, key, descriptor)
      else delete (target as Record<string, unknown>)[key]
    }
    restore(globalThis, 'ResizeObserver', saved.ResizeObserver)
    restore(globalThis, 'DOMMatrixReadOnly', saved.DOMMatrixReadOnly)
    restore(HTMLElement.prototype, 'offsetHeight', saved.offsetHeight)
    restore(HTMLElement.prototype, 'offsetWidth', saved.offsetWidth)
    restore(SVGElement.prototype, 'getBBox', saved.getBBox)
  })
}

/** The editor's canvas, drawn from whatever the workflow store holds. */
export function renderCanvas() {
  return render(
    <MantineProvider>
      <ReactFlowProvider>
        <div style={{ width: 1400, height: 700 }}>
          <FlowCanvas onNodeClick={() => {}} onEdgeClick={() => {}} onDrop={() => {}} onDragOver={() => {}} />
        </div>
      </ReactFlowProvider>
    </MantineProvider>
  )
}
