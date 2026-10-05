import { Suspense, lazy, useState } from 'react'
import { ActionIcon, Box, Popover, Text, Tooltip } from '@mantine/core'
import { IconFunction } from '@tabler/icons-react'
import type { ExpressionFunction } from '@/lib/functionCatalog'
import { ESCAPE_STOPS_HERE } from './escapeStopsHere'

// Module scope: a lazy() inside the component is a new type on every render.
const FunctionList = lazy(() => import('./FunctionList'))

interface FunctionPickerProps {
  onPick: (fn: ExpressionFunction) => void
}

/**
 * The "Insert function" button of an input that takes an expression. It only
 * says which function was picked; where the call goes in the text is the
 * input's to decide (lib/expressionInsert).
 *
 * Offer it only where the engine evaluates a call: a value read by
 * EvaluateAdvancedExpression or EvaluateField, or a {{ }} token a template
 * resolver reads. A field read as a plain path -- Mask, Deduplicate -- would
 * take the call for a field's name and find nothing.
 */
export function FunctionPicker({ onPick }: FunctionPickerProps) {
  const [opened, setOpened] = useState(false)

  return (
    <Popover opened={opened} onChange={setOpened} withArrow position="bottom-end" trapFocus>
      <Popover.Target>
        {/* Not while the list is open: the tooltip sat on top of it. */}
        <Tooltip label="Insert function" disabled={opened}>
          <ActionIcon
            aria-label="Insert function"
            variant="subtle"
            onClick={(e) => {
              e.preventDefault()
              setOpened((v) => !v)
            }}
          >
            <IconFunction size="1rem" />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      {/* Focusable, so a click on the list's own text leaves the focus in the
          list rather than on the page behind it. */}
      <Popover.Dropdown tabIndex={-1} {...ESCAPE_STOPS_HERE}>
        <Box w={340} maw="80vw">
          <Suspense fallback={<Text size="xs" c="dimmed">Loading functions…</Text>}>
            <FunctionList
              inPopover
              onPick={(fn) => {
                onPick(fn)
                setOpened(false)
              }}
            />
          </Suspense>
        </Box>
      </Popover.Dropdown>
    </Popover>
  )
}
