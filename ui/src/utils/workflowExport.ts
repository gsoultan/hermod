/**
 * Downloading a workflow export.
 *
 * Both the workflows list and the workflow detail page offered "Export JSON",
 * each with its own copy of the same anchor-and-blob dance, and both reported
 * "Workflow exported successfully" whatever came back. The server now names the
 * dependencies it could not resolve in `missing_refs`; a bundle with one is
 * still worth downloading, but saying nothing about it is how an operator finds
 * out only on the other machine that the workflow cannot start.
 */

export type MissingRef = {
  kind: string
  id: string
  node_id?: string
}

/** Turns the bundle's unresolved references into one line for a notification. */
export function describeMissingRefs(refs: MissingRef[] | undefined | null): string | null {
  if (!refs || refs.length === 0) return null
  const named = refs.map((r) => `${r.kind} ${r.id}${r.node_id ? ` (node ${r.node_id})` : ''}`)
  return `This workflow points at ${named.length === 1 ? 'a dependency that no longer exists' : 'dependencies that no longer exist'}, so the bundle does not contain ${named.length === 1 ? 'it' : 'them'}: ${named.join(', ')}. Importing it elsewhere produces a workflow that cannot start.`
}

/**
 * Keeps a workflow name usable as a filename: anything outside a conservative
 * set becomes an underscore, so a name with a slash cannot propose a path and a
 * name with a quote cannot break out of one.
 */
export function exportFilename(name: string): string {
  const safe = (name || '').replace(/[^A-Za-z0-9._-]+/g, '_').replace(/^[._-]+|[._-]+$/g, '')
  return `workflow-${(safe || 'workflow').slice(0, 100)}.json`
}

export type ExportResult = { warning: string | null }

/**
 * Saves the bundle in `response` to disk and reports anything the operator
 * should know about it. The response is read as text rather than a blob so the
 * bundle can be inspected before it is handed to the browser.
 */
export async function downloadWorkflowExport(response: Response, workflowName: string): Promise<ExportResult> {
  const text = await response.text()

  let warning: string | null = null
  try {
    warning = describeMissingRefs(JSON.parse(text)?.missing_refs)
  } catch {
    // An unparseable body is still saved: the operator keeps whatever the
    // server sent rather than losing it to a parse error here.
    warning = null
  }

  const url = window.URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = exportFilename(workflowName)
  document.body.appendChild(a)
  a.click()
  window.URL.revokeObjectURL(url)
  document.body.removeChild(a)

  return { warning }
}
