import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { describeMissingRefs, downloadWorkflowExport } from '../utils/workflowExport'

// The export endpoint answers with a bundle that now names the dependencies it
// could not resolve. Downloading it without reading that field is how an
// operator carries a bundle to another machine and only finds out there that
// the workflow cannot start.
describe('describeMissingRefs', () => {
  it('says nothing when the bundle is complete', () => {
    expect(describeMissingRefs(undefined)).toBeNull()
    expect(describeMissingRefs([])).toBeNull()
  })

  it('names each unresolved dependency', () => {
    const msg = describeMissingRefs([
      { kind: 'source', id: 'src-deleted', node_id: 'n1' },
      { kind: 'sink', id: 'snk-gone' },
    ])
    expect(msg).toContain('src-deleted')
    expect(msg).toContain('snk-gone')
    expect(msg).toContain('source')
    expect(msg).toContain('sink')
  })
})

describe('downloadWorkflowExport', () => {
  let clicked: HTMLAnchorElement | null = null
  const originalCreate = document.createElement.bind(document)

  beforeEach(() => {
    clicked = null
    URL.createObjectURL = vi.fn(() => 'blob:stub')
    URL.revokeObjectURL = vi.fn()
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = originalCreate(tag)
      if (tag === 'a') {
        ;(el as HTMLAnchorElement).click = () => {
          clicked = el as HTMLAnchorElement
        }
      }
      return el
    }) as typeof document.createElement)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  const bundle = (extra: Record<string, unknown> = {}) =>
    new Response(JSON.stringify({ workflow: { id: 'wf-1', name: 'Orders' }, ...extra }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })

  it('downloads the bundle and reports no warning when it is complete', async () => {
    const result = await downloadWorkflowExport(bundle(), 'Orders')
    expect(result.warning).toBeNull()
    expect(clicked).not.toBeNull()
    expect(clicked!.download).toBe('workflow-Orders.json')
  })

  it('still downloads, but warns, when the bundle has unresolved references', async () => {
    const result = await downloadWorkflowExport(
      bundle({ missing_refs: [{ kind: 'source', id: 'src-deleted', node_id: 'n1' }] }),
      'Orders',
    )
    expect(clicked).not.toBeNull()
    expect(result.warning).toContain('src-deleted')
  })

  it('does not propose a path or break the filename when the workflow name has slashes or quotes', async () => {
    await downloadWorkflowExport(bundle(), 'a/b"c')
    expect(clicked).not.toBeNull()
    expect(clicked!.download).not.toContain('/')
    expect(clicked!.download).not.toContain('"')
  })
})
