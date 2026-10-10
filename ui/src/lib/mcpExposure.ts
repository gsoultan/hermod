/**
 * Whether a workflow is offered to MCP clients at /api/mcp.
 *
 * The backend rule is internal/mcpserver/exposure.go: a workflow is exposed
 * when one of its tags equals ExposeTag ("mcp"), ignoring case and
 * surrounding space. It is a tag, not a setting, so it is saved with the
 * workflow like any other tag.
 */
export const MCP_EXPOSE_TAG = 'mcp'

export const MCP_ENDPOINT = '/api/mcp'

const isExposeTag = (tag: string) => tag.trim().toLowerCase() === MCP_EXPOSE_TAG

export function isMcpExposed(tags: readonly string[] | null | undefined): boolean {
  return (tags ?? []).some(isExposeTag)
}

/**
 * The tags with exposure turned on or off. Turning it off removes every
 * spelling the server would match, or the workflow would stay exposed.
 */
export function withMcpExposure(tags: readonly string[] | null | undefined, exposed: boolean): string[] {
  const current = [...(tags ?? [])]
  if (exposed) return isMcpExposed(current) ? current : [...current, MCP_EXPOSE_TAG]
  return current.filter((t) => !isExposeTag(t))
}
