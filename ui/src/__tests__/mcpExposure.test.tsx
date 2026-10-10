import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { isMcpExposed, withMcpExposure, MCP_EXPOSE_TAG } from '@/lib/mcpExposure'
import { McpExposureSwitch } from '@/pages/workflows/WorkflowEditor/components/McpExposureSwitch'

describe('MCP exposure tag', () => {
  it('matches the tag the way internal/mcpserver.IsExposed does', () => {
    expect(MCP_EXPOSE_TAG).toBe('mcp')
    expect(isMcpExposed(['mcp'])).toBe(true)
    expect(isMcpExposed(['billing', ' MCP '])).toBe(true)
    expect(isMcpExposed(['mcp-tools', 'billing'])).toBe(false)
    expect(isMcpExposed(undefined)).toBe(false)
  })

  it('adds the tag once and removes every spelling of it', () => {
    expect(withMcpExposure(['billing'], true)).toEqual(['billing', 'mcp'])
    expect(withMcpExposure(['Mcp', 'billing'], true)).toEqual(['Mcp', 'billing'])
    expect(withMcpExposure(['MCP', 'billing', ' mcp'], false)).toEqual(['billing'])
    expect(withMcpExposure(undefined, true)).toEqual(['mcp'])
  })
})

function Harness({ initial }: { initial: string[] }) {
  const [tags, setTags] = useState(initial)
  return (
    <>
      <McpExposureSwitch tags={tags} onChange={setTags} />
      <output data-testid="tags">{tags.join(',')}</output>
    </>
  )
}

describe('Expose to MCP clients switch', () => {
  it('turns the tag on and off and names the endpoint', async () => {
    const user = userEvent.setup()
    render(<MantineProvider><Harness initial={['billing']} /></MantineProvider>)

    const toggle = screen.getByRole('switch', { name: /expose to mcp clients/i })
    expect(toggle).not.toBeChecked()
    expect(screen.getByText('/api/mcp')).toBeInTheDocument()

    await user.click(toggle)
    expect(toggle).toBeChecked()
    expect(screen.getByTestId('tags')).toHaveTextContent('billing,mcp')

    await user.click(toggle)
    expect(toggle).not.toBeChecked()
    expect(screen.getByTestId('tags')).toHaveTextContent(/^billing$/)
  })

  it('reads an existing tag in any case as exposed', () => {
    render(<MantineProvider><Harness initial={['MCP']} /></MantineProvider>)
    expect(screen.getByRole('switch', { name: /expose to mcp clients/i })).toBeChecked()
  })
})
