import { useId, useMemo, useState } from 'react'
import { Code, Group, ScrollArea, Stack, Text, TextInput, UnstyledButton } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import { exampleResult, groupByCategory, searchFunctions, type ExpressionFunction } from '@/lib/functionCatalog'
import { ESCAPE_STOPS_HERE } from './escapeStopsHere'

const EXAMPLE = { fontSize: 'var(--mantine-font-size-xs)', overflowWrap: 'anywhere' } as const

interface FunctionListProps {
  onPick: (fn: ExpressionFunction) => void
  /** Height of the scrolling list. */
  height?: number
  /**
   * Set when the list is in a popover over a modal or a drawer, so Escape
   * closes the popover and stops there. Mantine's modal listens on the window
   * and closes on any Escape whose target does not carry this attribute: the
   * picker closed, and the node's settings closed behind it.
   */
  inPopover?: boolean
}

/**
 * Every expression function, searchable and grouped by what it is for. Each
 * entry shows how the call is written, what it does, and an example with the
 * value the engine answers for it.
 *
 * The default export, so the catalog is only loaded by whatever opens a list.
 */
export default function FunctionList({ onPick, height = 300, inPopover }: FunctionListProps) {
  const id = useId()
  const escapeStopsHere = inPopover ? ESCAPE_STOPS_HERE : {}
  const [query, setQuery] = useState('')
  const matches = useMemo(() => searchFunctions(query), [query])
  const groups = useMemo(() => groupByCategory(matches), [matches])

  return (
    <Stack gap={6}>
      <TextInput
        size="xs"
        aria-label="Search functions"
        placeholder="Search — try “date”, “default” or “uppercase”"
        leftSection={<IconSearch size="0.8rem" />}
        value={query}
        data-autofocus
        {...escapeStopsHere}
        onChange={(e) => setQuery(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && matches.length > 0) {
            e.preventDefault()
            onPick(matches[0])
          }
        }}
      />
      <ScrollArea h={height} type="auto">
        <Stack gap="xs" pr={6}>
          {groups.map(([category, functions]) => (
            <Stack
              key={category}
              component="fieldset"
              gap={4}
              style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}
            >
              <Text component="legend" size="xs" fw={700} c="dimmed" tt="uppercase" mb={2}>
                {category}
              </Text>
              {functions.map((fn) => {
                const result = exampleResult(fn)
                return (
                  <UnstyledButton
                    key={fn.name}
                    aria-label={`Insert ${fn.signature}`}
                    aria-describedby={`${id}-${fn.name}`}
                    {...escapeStopsHere}
                    p={6}
                    style={{
                      borderRadius: 6,
                      border: '1px solid var(--mantine-color-default-border)',
                      display: 'block',
                      width: '100%',
                    }}
                    onClick={() => onPick(fn)}
                  >
                    <Text size="xs" fw={700} ff="monospace">
                      {fn.signature}
                    </Text>
                    <Text id={`${id}-${fn.name}`} size="xs" c="dimmed">
                      {fn.summary}
                    </Text>
                    {/* A call that takes nothing is its own example. */}
                    {fn.example !== fn.signature && (
                      <Group gap={4} mt={4} wrap="wrap" align="baseline">
                        <Code style={EXAMPLE}>{fn.example}</Code>
                        {result !== null && (
                          <>
                            <Text size="xs" c="dimmed" aria-hidden>
                              →
                            </Text>
                            <Code color="teal" style={EXAMPLE}>
                              {result}
                            </Code>
                          </>
                        )}
                      </Group>
                    )}
                  </UnstyledButton>
                )
              })}
            </Stack>
          ))}
          {matches.length === 0 && (
            <Text size="xs" c="dimmed" ta="center" py="sm">
              No function matches “{query}”.
            </Text>
          )}
        </Stack>
      </ScrollArea>
    </Stack>
  )
}
