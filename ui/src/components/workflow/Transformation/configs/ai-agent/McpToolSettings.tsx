import { useState } from 'react'
import { ActionIcon, Alert, Button, Checkbox, Group, Radio, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import {
  isPlaintextCredential,
  isReservedHeader,
  SECRET_HEADER_HINT,
  type AgentIssue,
  type AgentTool,
  type McpServer,
} from './agentTools'

interface McpToolSettingsProps {
  tool: AgentTool
  issues: AgentIssue[]
  onChange: (tool: AgentTool) => void
}

type HeaderRow = { name: string; value: string }

const rowsOf = (headers: unknown): HeaderRow[] =>
  headers && typeof headers === 'object' && !Array.isArray(headers)
    ? Object.entries(headers as Record<string, unknown>).map(([name, value]) => ({ name, value: String(value ?? '') }))
    : []

/** Rows with a name, as the headers object; a later row of the same name wins. */
const headersOf = (rows: HeaderRow[]): Record<string, string> =>
  Object.fromEntries(rows.filter((r) => r.name.trim() !== '').map((r) => [r.name, r.value]))

const sameHeaders = (a: Record<string, string>, b: Record<string, string>) => JSON.stringify(a) === JSON.stringify(b)

const URL_HINT =
  'The server\'s Streamable HTTP endpoint, http or https. May be a template such as {{secret("MCP_URL")}}.'

/** The write flag as the three-way choice: absent, false or true (config.go parseMCP). */
const WRITE_CHOICES = { unset: 'unset', readOnly: 'false', writes: 'true' } as const
const writeChoice = (w: unknown) => (w === false ? WRITE_CHOICES.readOnly : w === true ? WRITE_CHOICES.writes : WRITE_CHOICES.unset)

/**
 * The settings of an mcp tool: the server (url and headers, both templates
 * resolved with secrets only), the one remote tool it calls, whether the
 * workflow says it writes, and whether the model sees the server's input
 * schema or the arguments declared here (config.go parseMCP / useRemote).
 */
export function McpToolSettings({ tool, issues, onChange }: McpToolSettingsProps) {
  const server: McpServer = tool.server && typeof tool.server === 'object' ? tool.server : {}
  const errorFor = (field: string) =>
    issues.filter((i) => i.severity === 'error' && i.field === field).map((i) => i.message).join(' ') || undefined
  const headerWarnings = issues.filter((i) => i.severity === 'warning' && i.field === 'server.headers')

  // Rows are kept locally so a header can be named after it is added, and are
  // reset whenever the saved headers change from outside this editor.
  const saved = headersOf(rowsOf(server.headers))
  const [rows, setRows] = useState<HeaderRow[]>(() => rowsOf(server.headers))
  if (!sameHeaders(headersOf(rows), saved)) setRows(rowsOf(server.headers))

  const setServer = (patch: Partial<McpServer>) => {
    const next: McpServer = { ...server, ...patch }
    if (next.headers && Object.keys(next.headers).length === 0) delete next.headers
    onChange({ ...tool, server: next })
  }
  const saveRows = (next: HeaderRow[]) => {
    setRows(next)
    setServer({ headers: headersOf(next) })
  }
  const updateRow = (i: number, patch: Partial<HeaderRow>) => saveRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  const chooseWrite = (choice: string) => {
    const { write: _write, ...rest } = tool
    if (choice === WRITE_CHOICES.unset) onChange(rest)
    else onChange({ ...rest, write: choice === WRITE_CHOICES.writes })
  }
  const usesServerSchema = tool.parameters === undefined
  const chooseSchema = (useServer: boolean) => {
    const { parameters: _parameters, ...rest } = tool
    onChange(useServer ? rest : { ...rest, parameters: [] })
  }

  return (
    <Stack gap="sm">
      <Group grow align="flex-start" gap="sm">
        <TextInput
          label="Server URL"
          placeholder="https://mcp.example.com/mcp"
          value={server.url ?? ''}
          onChange={(e) => setServer({ url: e.currentTarget.value })}
          required
          error={errorFor('server.url')}
          description={URL_HINT}
        />
        <TextInput
          label="Remote tool"
          placeholder="e.g. search_docs"
          value={tool.tool ?? ''}
          onChange={(e) => onChange({ ...tool, tool: e.currentTarget.value })}
          required
          error={errorFor('tool')}
          description="The name of the one tool on the server this tool calls."
        />
      </Group>

      <Stack gap="xs">
        <Text size="sm" fw={500}>
          Headers
        </Text>
        <Text size="xs" c="dimmed">
          Sent with every request to the server. Put credentials in a vhost secret and reference it, for example Bearer{' '}
          {SECRET_HEADER_HINT}.
        </Text>
        {rows.map((r, i) => (
          <Group key={i} gap="xs" align="flex-start" wrap="nowrap">
            <TextInput
              label={`Header ${i + 1} name`}
              placeholder="e.g. Authorization"
              value={r.name}
              onChange={(e) => updateRow(i, { name: e.currentTarget.value })}
              error={isReservedHeader(r.name) ? 'Set by the MCP transport; it cannot be configured.' : undefined}
              size="xs"
              flex={1}
            />
            <TextInput
              label={`Header ${i + 1} value`}
              placeholder={`e.g. Bearer ${SECRET_HEADER_HINT}`}
              value={r.value}
              onChange={(e) => updateRow(i, { value: e.currentTarget.value })}
              error={isPlaintextCredential(r.name, r.value) ? 'A credential typed in here is saved with the workflow.' : undefined}
              size="xs"
              flex={2}
            />
            <ActionIcon
              aria-label={`Remove header ${i + 1}`}
              color="red"
              variant="subtle"
              mt={22}
              onClick={() => saveRows(rows.filter((_, j) => j !== i))}
            >
              <IconTrash size="0.9rem" />
            </ActionIcon>
          </Group>
        ))}
        {errorFor('server.headers') && (
          <Text size="xs" c="var(--mantine-color-error)">
            {errorFor('server.headers')}
          </Text>
        )}
        {headerWarnings.map((w) => (
          <Text key={w.message} size="xs" c="orange">
            {w.message}
          </Text>
        ))}
        <Button
          variant="subtle"
          size="xs"
          leftSection={<IconPlus size="0.9rem" />}
          onClick={() => setRows([...rows, { name: '', value: '' }])}
          w="fit-content"
        >
          Add header
        </Button>
      </Stack>

      <Radio.Group
        label="Does this tool change anything?"
        value={writeChoice(tool.write)}
        onChange={chooseWrite}
        description="What you say here is checked against what the server says about the tool."
      >
        <Stack gap={6} mt={6}>
          <Radio value={WRITE_CHOICES.unset} label="Not set (treated as a write)" />
          <Radio value={WRITE_CHOICES.readOnly} label="Read-only (write: false)" />
          <Radio value={WRITE_CHOICES.writes} label="Writes (write: true)" />
        </Stack>
      </Radio.Group>
      {tool.write === false && (
        <Text size="xs" c="dimmed">
          This still needs the server's read-only annotation on the tool. If the server does not mark it read-only, it
          is treated as a write.
        </Text>
      )}
      <Alert color="yellow" variant="light" icon={<IconInfoCircle size="1rem" />}>
        <Text size="sm">
          An MCP tool runs without approval only when you mark it read-only here and the server marks it read-only.
          Otherwise every call waits for approval, unless you turn approval off below.
        </Text>
      </Alert>

      <Checkbox
        label="Use the server's input schema"
        description="The model fills in the arguments the server describes. Turn off to declare them here; an empty list means the tool takes none."
        checked={usesServerSchema}
        onChange={(e) => chooseSchema(e.currentTarget.checked)}
      />
    </Stack>
  )
}
