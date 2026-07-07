import { useMemo, useRef, useState } from 'react'
import { ActionIcon, Group, Popover, Stack, Text, TextInput, Textarea, Tooltip as MantineTooltip, ScrollArea, Badge } from '@mantine/core'
import { IconSearch, IconVariable } from '@tabler/icons-react'

type CommonProps = {
  label?: string
  placeholder?: string
  description?: string
  required?: boolean
  disabled?: boolean
  error?: string
}

export interface TemplateFieldProps extends CommonProps {
  value: string
  onChange: (value: string) => void
  availableFields?: any[] | { path: string; type?: string }[]
  /**
   * Called to build the insertion text from a selected field.
   * Default uses Go template style: {{.field.path}}
   */
  buildToken?: (fieldPath: string) => string
  /**
   * When true, renders a textarea instead of a text input.
   */
  multiline?: boolean
}

function defaultBuildToken(fieldPath: string) {
  // Convert a.b[0].c -> .a.b[0].c for Go template style
  const dotPrefixed = fieldPath.startsWith('.') ? fieldPath : `.${fieldPath}`
  return `{{${dotPrefixed}}}`
}

export function TemplateField({
  label,
  placeholder,
  description,
  required,
  disabled,
  error,
  value,
  onChange,
  availableFields = [],
  buildToken = defaultBuildToken,
  multiline,
}: TemplateFieldProps) {
  const [opened, setOpened] = useState(false)
  const [q, setQ] = useState('')
  const inputRef = useRef<HTMLInputElement & HTMLTextAreaElement>(null as any)

  const filtered = useMemo(() => {
    const query = q.trim().toLowerCase()
    return (availableFields || []).filter((f) => {
      const path = typeof f === 'string' ? f : f.path
      return !query || path.toLowerCase().includes(query)
    })
  }, [q, availableFields])

  const insertAtCursor = (text: string) => {
    const el: any = inputRef.current
    if (!el) {
      onChange((value || '') + text)
      return
    }
    const start = el.selectionStart ?? (value?.length ?? 0)
    const end = el.selectionEnd ?? (value?.length ?? 0)
    const before = (value || '').slice(0, start)
    const after = (value || '').slice(end)
    const next = `${before}${text}${after}`
    onChange(next)
    // restore cursor after inserted text
    requestAnimationFrame(() => {
      try {
        el.focus()
        const caret = start + text.length
        el.setSelectionRange?.(caret, caret)
      } catch {}
    })
  }

  const FieldList = (
    <Stack gap={6} style={{ width: 260 }}>
      <TextInput
        size="xs"
        placeholder="Search fields..."
        value={q}
        leftSection={<IconSearch size="0.8rem" />}
        onChange={(e) => setQ(e.currentTarget.value)}
      />
      <ScrollArea h={220} type="auto">
        <Stack gap={4} pr={4}>
          {filtered.map((f) => {
            const path = typeof f === 'string' ? f : f.path;
            const type = typeof f === 'string' ? undefined : f.type;
            
            return (
              <Group
                key={path}
                justify="space-between"
                wrap="nowrap"
                p={6}
                style={{
                  borderRadius: 6,
                  border: '1px solid var(--mantine-color-gray-3)',
                  cursor: 'pointer',
                }}
                onClick={() => {
                  insertAtCursor(buildToken(path))
                  setOpened(false)
                }}
              >
                <Stack gap={0} style={{ overflow: 'hidden' }}>
                  <Text size="xs" fw={500} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {path}
                  </Text>
                  {type && (
                    <Text size="10px" c="dimmed">
                      {type}
                    </Text>
                  )}
                </Stack>
                <Badge variant="light" size="xs">Insert</Badge>
              </Group>
            );
          })}
          {filtered.length === 0 && (
            <Text size="xs" c="dimmed" px={4}>
              No fields match "{q}"
            </Text>
          )}
        </Stack>
      </ScrollArea>
      <Text size="10px" c="dimmed">
        Tip: Click a field to insert a template token.
      </Text>
    </Stack>
  )

  const commonProps = {
    label,
    placeholder,
    description,
    required,
    disabled,
    error,
    value,
    onChange: (e: any) => onChange(e?.target ? e.target.value : e),
    rightSection: (
      <Popover opened={opened} onChange={setOpened} withArrow position="bottom-end">
        <Popover.Target>
          <MantineTooltip label="Insert variable">
            <ActionIcon
              aria-label="Insert variable"
              variant="subtle"
              onClick={(e) => {
                e.preventDefault()
                setOpened((v) => !v)
              }}
            >
              <IconVariable size="1rem" />
            </ActionIcon>
          </MantineTooltip>
        </Popover.Target>
        <Popover.Dropdown>{FieldList}</Popover.Dropdown>
      </Popover>
    ),
    ref: inputRef as any,
  }

  return multiline ? (
    <Textarea autosize minRows={2} {...commonProps} />
  ) : (
    <TextInput {...commonProps} />
  )
}

export default TemplateField
