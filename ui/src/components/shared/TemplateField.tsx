import { Suspense, createContext, lazy, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { ActionIcon, Box, Collapse, Group, Popover, Stack, Text, TextInput, Textarea, Tooltip as MantineTooltip, ScrollArea, Badge, UnstyledButton } from '@mantine/core'
import { IconBinaryTree2, IconBraces, IconEye, IconEyeOff, IconKey, IconSearch, IconVariable } from '@tabler/icons-react'
import { applyFunction, isInsideToken } from '@/lib/expressionInsert'
import type { ExpressionFunction } from '@/lib/functionCatalog'
import { FunctionPicker } from './FunctionPicker'
import { ESCAPE_STOPS_HERE } from './escapeStopsHere'

// Module scope, and only loaded when opened: it carries the function catalog.
const ValueBuilder = lazy(() => import('./ValueBuilder'))

/**
 * The names of the secrets the workflow being edited can read: its vhost's.
 * Provided once, above the node's form, so every field that can insert a
 * variable can insert a secret too. Names only -- the browser never holds a
 * value.
 */
export const SecretNamesContext = createContext<string[]>([])

type CommonProps = {
  label?: string
  placeholder?: string
  description?: ReactNode
  required?: boolean
  disabled?: boolean
  error?: string
  /**
   * Names the input when the visible heading is rendered separately -- a row in
   * a repeated list has no label of its own, which leaves every one of them
   * reachable only by position.
   */
  'aria-label'?: string
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
   * Called to build the insertion text from a selected secret name.
   * Default is a template token: {{secret("NAME")}}
   */
  buildSecretToken?: (name: string) => string
  /**
   * When true, renders a textarea instead of a text input.
   */
  multiline?: boolean
  /**
   * Offers "Insert function", and says how a call is written here:
   * `expression` for a value that is itself an expression (a Set Fields
   * value), `token` for a template, where a call is a {{ }} token.
   *
   * Off unless asked for. Whether a call is evaluated depends on what resolves
   * the field, and not every template is resolved by something that does.
   */
  functions?: 'expression' | 'token'
  /**
   * A credential: typed text is hidden until revealed. A secret reference --
   * {{secret("NAME")}} -- is only a name, so it is shown as written.
   */
  masked?: boolean
  /** Monospace text, for URLs, headers and other machine-read values. */
  mono?: boolean
  /**
   * Offers secrets only: a value that decides where a credential goes, which
   * record data must not choose (evaluator.ResolveTemplateScoped).
   */
  secretsOnly?: boolean
  /**
   * Offers no secrets: a value whose tokens are read as plain paths, such as
   * a SQL template whose every token becomes a bound parameter, where a
   * secret token would bind NULL.
   */
  noSecrets?: boolean
}

function defaultBuildSecretToken(name: string) {
  return `{{secret("${name}")}}`
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
  buildSecretToken = defaultBuildSecretToken,
  multiline,
  functions,
  masked,
  mono,
  secretsOnly,
  noSecrets,
  'aria-label': ariaLabel,
}: TemplateFieldProps) {
  const [revealed, setRevealed] = useState(false)
  const hidden = masked && !revealed && !/^\s*\{\{\s*secret[(:]/.test(value || '')
  const [opened, setOpened] = useState(false)
  const [building, setBuilding] = useState(false)
  const [q, setQ] = useState('')
  const inputRef = useRef<HTMLInputElement & HTMLTextAreaElement>(null as any)

  const filtered = useMemo(() => {
    const query = q.trim().toLowerCase()
    if (secretsOnly) return []
    return (availableFields || []).filter((f) => {
      const path = typeof f === 'string' ? f : f.path
      return !query || path.toLowerCase().includes(query)
    })
  }, [q, availableFields, secretsOnly])

  const vhostSecrets = useContext(SecretNamesContext)
  const secretNames = noSecrets ? [] : vhostSecrets
  const filteredSecrets = useMemo(() => {
    const query = q.trim().toLowerCase()
    return secretNames.filter((name) => !query || name.toLowerCase().includes(query))
  }, [q, secretNames])

  // Whether the caret has ever been put in the input. Until it has, where the
  // element says its caret is depends on the browser -- the start in some, the
  // end in others -- and "Bearer " plus a variable came out as the variable
  // followed by "Bearer ".
  const placed = useRef(false)

  // The caret, or the end of the text for an input that has never had one.
  const selection = (): [number, number] => {
    const el: any = inputRef.current
    const length = (value || '').length
    if (!placed.current) return [length, length]
    return [el?.selectionStart ?? length, el?.selectionEnd ?? length]
  }

  const select = (start: number, end: number) => {
    const el: any = inputRef.current
    if (!el) return
    requestAnimationFrame(() => {
      try {
        el.focus()
        el.setSelectionRange?.(start, end)
      } catch {}
    })
  }

  const insertAtCursor = (text: string) => {
    const [start, end] = selection()
    const before = (value || '').slice(0, start)
    const after = (value || '').slice(end)
    onChange(`${before}${text}${after}`)
    // restore cursor after inserted text
    select(start + text.length, start + text.length)
  }

  /**
   * Inserts a field or a secret, written for where the caret is.
   *
   * Inside a {{ }} token that is still open the text is an expression -- the
   * argument of a call -- where a field is `source.x`. A token of its own
   * there, {{upper({{.name}})}}, is not something any resolver reads. And a
   * Set Fields value that holds a token is a template, where the bare
   * `source.x` this field otherwise inserts would be text.
   *
   * Only for a field that offers functions. `source.x` is read by the
   * resolvers those fields use; evaluator.MessageResolver, behind most other
   * templates, does not read it.
   */
  const insertReference = (expression: string, token: string) => {
    const text = value || ''
    if (functions && isInsideToken(text, selection()[0])) {
      insertAtCursor(expression)
    } else if (functions === 'expression' && text.includes('{{')) {
      insertAtCursor(`{{${token}}}`)
    } else {
      insertAtCursor(token)
    }
  }

  const insertFunction = (fn: ExpressionFunction) => {
    if (!functions) return
    const [start, end] = selection()
    const edit = applyFunction(value || '', start, end, fn, functions)
    onChange(edit.value)
    // The next placeholder, so typing or picking a variable replaces it.
    select(edit.selectionStart, edit.selectionEnd)
  }

  const FieldList = (
    <Stack gap={6} style={{ width: 260 }}>
      <TextInput
        size="xs"
        placeholder={secretsOnly ? 'Search secrets...' : 'Search fields...'}
        value={q}
        data-autofocus
        {...ESCAPE_STOPS_HERE}
        leftSection={<IconSearch size="0.8rem" />}
        onChange={(e) => setQ(e.currentTarget.value)}
      />
      <ScrollArea h={220} type="auto">
        <Stack gap={4} pr={4}>
          {filtered.map((f) => {
            const path = typeof f === 'string' ? f : f.path;
            const type = typeof f === 'string' ? undefined : f.type;
            
            // A button, as the secrets below are. It was a row with a click
            // handler: nothing Tab stops on, so the list could be opened from
            // the keyboard and nothing in it picked.
            return (
              <UnstyledButton
                key={path}
                p={6}
                {...ESCAPE_STOPS_HERE}
                style={{
                  borderRadius: 6,
                  border: '1px solid var(--mantine-color-default-border)',
                }}
                onClick={() => {
                  insertReference(`source.${path}`, buildToken(path))
                  setOpened(false)
                }}
              >
                <Group justify="space-between" wrap="nowrap">
                  <Stack gap={0} style={{ overflow: 'hidden' }}>
                    <Group gap={6} wrap="nowrap" style={{ overflow: 'hidden' }}>
                      <IconBraces size="0.8rem" style={{ flexShrink: 0 }} />
                      <Text size="xs" fw={500} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {path}
                      </Text>
                    </Group>
                    {type && (
                      <Text size="xs" c="dimmed">
                        {type}
                      </Text>
                    )}
                  </Stack>
                  <Badge variant="light" size="xs">Insert</Badge>
                </Group>
              </UnstyledButton>
            );
          })}
          {filtered.length === 0 && !secretsOnly && (
            <Text size="xs" c="dimmed" px={4}>
              No fields match "{q}"
            </Text>
          )}
          {secretsOnly && filteredSecrets.length === 0 && (
            <Text size="xs" c="dimmed" px={4}>
              {secretNames.length === 0 ? 'This vhost has no secrets yet.' : `No secrets match "${q}"`}
            </Text>
          )}
          {filteredSecrets.length > 0 && (
            <Stack
              component="fieldset"
              gap={4}
              mt={6}
              style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}
            >
              <Text component="legend" size="xs" fw={600} c="dimmed" px={4} mb={4}>
                Secrets of this vhost
              </Text>
              {filteredSecrets.map((name) => (
                <UnstyledButton
                  key={name}
                  p={6}
                  {...ESCAPE_STOPS_HERE}
                  style={{ borderRadius: 6, border: '1px solid var(--mantine-color-default-border)' }}
                  onClick={() => {
                    insertReference(`secret("${name}")`, buildSecretToken(name))
                    setOpened(false)
                  }}
                >
                  <Group justify="space-between" wrap="nowrap">
                    <Group gap={6} wrap="nowrap" style={{ overflow: 'hidden' }}>
                      <IconKey size="0.8rem" />
                      <Text size="xs" fw={500} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {name}
                      </Text>
                    </Group>
                    <Badge variant="light" size="xs" color="grape">Insert</Badge>
                  </Group>
                </UnstyledButton>
              ))}
            </Stack>
          )}
        </Stack>
      </ScrollArea>
      <Text size="xs" c="dimmed">
        {secretsOnly
          ? 'Only secrets: record fields are never read here.'
          : `Tip: Click a field to insert it at the caret.${functions ? ' Use the builder to nest functions.' : ''}`}
      </Text>
    </Stack>
  )

  const commonProps = {
    label,
    'aria-label': ariaLabel,
    placeholder,
    description,
    required,
    disabled,
    error,
    value,
    onChange: (e: any) => onChange(e?.target ? e.target.value : e),
    onFocus: () => {
      placed.current = true
    },
    rightSection: (
      <Group gap={0} wrap="nowrap">
        {masked && (
          <MantineTooltip label={revealed ? 'Hide' : 'Show'}>
            <ActionIcon
              aria-label={revealed ? 'Hide value' : 'Show value'}
              variant="subtle"
              onClick={(e) => {
                e.preventDefault()
                setRevealed((v) => !v)
              }}
            >
              {revealed ? <IconEyeOff size="1rem" /> : <IconEye size="1rem" />}
            </ActionIcon>
          </MantineTooltip>
        )}
        {functions && <FunctionPicker onPick={insertFunction} />}
        {/* The focus goes into the list when it opens: left on the button,
            Escape closed the node's settings and never reached the list. */}
        <Popover opened={opened} onChange={setOpened} withArrow position="bottom-end" trapFocus>
          <Popover.Target>
            <MantineTooltip label={secretsOnly ? 'Insert secret' : 'Insert variable'}>
              <ActionIcon
                aria-label={secretsOnly ? 'Insert secret' : 'Insert variable'}
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
          <Popover.Dropdown tabIndex={-1} {...ESCAPE_STOPS_HERE}>{FieldList}</Popover.Dropdown>
        </Popover>
        {/* After both insert buttons, so Tab from the value reaches them first. */}
        {functions && (
          <MantineTooltip label={building ? 'Close the builder' : 'Build with nested functions'}>
            <ActionIcon
              aria-label="Build expression"
              aria-expanded={building}
              variant={building ? 'light' : 'subtle'}
              onClick={(e) => {
                e.preventDefault()
                setBuilding((v) => !v)
              }}
            >
              <IconBinaryTree2 size="1rem" />
            </ActionIcon>
          </MantineTooltip>
        )}
      </Group>
    ),
    // One button per action; an input reserves the width of one.
    rightSectionWidth: 4 + 28 * (1 + (functions ? 2 : 0) + (masked ? 1 : 0)),
    styles: mono ? { input: { fontFamily: 'var(--mantine-font-family-monospace)' } } : undefined,
    ref: inputRef as any,
  }

  const input = multiline ? (
    <Textarea autosize minRows={2} {...commonProps} />
  ) : (
    <TextInput {...commonProps} type={hidden ? 'password' : 'text'} autoComplete={masked ? 'off' : undefined} />
  )

  if (!functions) return input

  return (
    <Stack gap={6}>
      {input}
      <Collapse expanded={building}>
        {building && (
          <Box
            p="sm"
            style={{
              border: '1px solid var(--mantine-color-default-border)',
              borderRadius: 'var(--mantine-radius-md)',
            }}
          >
            <Suspense fallback={<Text size="xs" c="dimmed">Loading the builder…</Text>}>
              <ValueBuilder
                label={label || ariaLabel || 'Value'}
                value={value}
                onChange={onChange}
                mode={functions}
                fieldPaths={(availableFields || []).map((f: any) => (typeof f === 'string' ? f : f.path))}
                secretNames={secretNames}
                tokens={{ field: buildToken, secret: buildSecretToken }}
                onEditAsText={() => setBuilding(false)}
              />
            </Suspense>
          </Box>
        )}
      </Collapse>
    </Stack>
  )
}

export default TemplateField
