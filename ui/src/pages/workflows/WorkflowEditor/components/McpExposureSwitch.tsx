import { Code, Stack, Switch, Text } from '@mantine/core'
import { isMcpExposed, MCP_ENDPOINT, MCP_EXPOSE_TAG, withMcpExposure } from '@/lib/mcpExposure'

interface McpExposureSwitchProps {
  tags: string[]
  onChange: (tags: string[]) => void
}

/**
 * Opts the workflow in to MCP by adding or removing its `mcp` tag. Like every
 * other workflow setting it takes effect when the workflow is saved.
 */
export function McpExposureSwitch({ tags, onChange }: McpExposureSwitchProps) {
  const exposed = isMcpExposed(tags)
  return (
    <Stack gap={4}>
      <Switch
        label="Expose to MCP clients"
        checked={exposed}
        onChange={(e) => onChange(withMcpExposure(tags, e.currentTarget.checked))}
        size="sm"
      />
      <Text size="xs" c="dimmed">
        MCP clients (Claude, ChatGPT, IDE agents) connected to <Code>{MCP_ENDPOINT}</Code> with a Hermod login can list
        this workflow and read its status; Editors and Administrators can also run it, through its webhook source.
        Only users with access to this workflow&apos;s vhost see it. This adds the <Code>{MCP_EXPOSE_TAG}</Code> tag;
        save the workflow to apply it.
      </Text>
    </Stack>
  )
}
