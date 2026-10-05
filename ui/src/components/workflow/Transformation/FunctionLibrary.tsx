import { Suspense, lazy } from 'react'
import { Card, Group, Text } from '@mantine/core'
import { IconFunction } from '@tabler/icons-react'

const FunctionList = lazy(() => import('@/components/shared/FunctionList'))

/**
 * Every expression function, beside the rows that use them.
 *
 * It was a card of 13 hand-picked functions, shown for the Formulas node only.
 * Set Fields evaluates the same values, so it is shown there too, and the list
 * is the catalog the engine's test checks (lib/expressionFunctions.json).
 *
 * Clicking one adds its example as a new row. To use a function in a value
 * that is already being written, the value's own "Insert function" button puts
 * it there.
 */
export function FunctionLibrary({ onInsert }: { onInsert: (example: string) => void }) {
  return (
    <Card withBorder padding="md" radius="md" component="section" aria-label="Function library">
      <Group gap="xs" mb={4}>
        <IconFunction size="1rem" color="var(--mantine-color-orange-6)" />
        <Text size="xs" fw={700}>FUNCTION LIBRARY</Text>
      </Group>
      <Text size="xs" c="dimmed" mb="xs">
        Click a function to add its example as a new field. To use one in a value you are writing,
        press the function button inside that value.
      </Text>
      <Suspense fallback={<Text size="xs" c="dimmed">Loading functions…</Text>}>
        <FunctionList height={260} onPick={(fn) => onInsert(fn.example)} />
      </Suspense>
    </Card>
  )
}
