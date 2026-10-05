import { Alert, Code, Group, Stack, Table, Text, Title } from '@mantine/core'
import { EXPRESSION_FUNCTIONS, exampleResult, groupByCategory } from '@/lib/functionCatalog'

const MONO_XS = { fontSize: 'var(--mantine-font-size-xs)', overflowWrap: 'anywhere' } as const

/**
 * The expression reference. The functions are the catalog the picker offers
 * (lib/expressionFunctions.json), not a list of their own: the one written out
 * here by hand had fallen six functions behind the engine.
 */
export default function HelpContent() {
  return (
    <Stack gap="md">
      <Alert color="blue" variant="light">
        <Stack gap={4}>
          <Text size="sm" fw={700}>Writing an expression</Text>
          <Text size="sm">1. A field of the incoming record is <Code>source.path</Code>, as in <Code>source.after.email</Code>.</Text>
          <Text size="sm">2. A function is <Code>name(arguments)</Code>, and can hold another: <Code>upper(trim(source.name))</Code>.</Text>
          <Text size="sm">3. Text goes in quotes: <Code>concat(source.first, " ", source.last)</Code>.</Text>
          <Text size="sm">4. Inside other text, write the call as a token: <Code>{'Hello {{upper(source.name)}}'}</Code>.</Text>
          <Text size="sm">
            You do not have to type these: the function button inside a value lists every function below
            and writes the call for you.
          </Text>
        </Stack>
      </Alert>

      {groupByCategory(EXPRESSION_FUNCTIONS).map(([category, functions]) => (
        <Stack key={category} gap={4}>
          <Title order={4} size="sm">{category}</Title>
          <Table verticalSpacing={6} horizontalSpacing="xs" withRowBorders layout="fixed">
            <Table.Tbody>
              {functions.map((fn) => {
                const result = exampleResult(fn)
                return (
                  <Table.Tr key={fn.name}>
                    <Table.Td w="34%" style={{ verticalAlign: 'top' }}>
                      <Text size="xs" fw={700} ff="monospace" style={{ overflowWrap: 'anywhere' }}>{fn.signature}</Text>
                    </Table.Td>
                    <Table.Td style={{ verticalAlign: 'top' }}>
                      <Text size="xs">{fn.summary}</Text>
                      {fn.example !== fn.signature && (
                        <Group gap={4} mt={4} wrap="wrap" align="baseline">
                          <Code style={MONO_XS}>{fn.example}</Code>
                          {result !== null && (
                            <>
                              <Text size="xs" c="dimmed" aria-hidden>→</Text>
                              <Code color="teal" style={MONO_XS}>{result}</Code>
                            </>
                          )}
                        </Group>
                      )}
                    </Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Stack>
      ))}
    </Stack>
  )
}
