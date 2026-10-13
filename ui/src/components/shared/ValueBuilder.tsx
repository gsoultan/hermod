import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  ActionIcon,
  Autocomplete,
  Box,
  Button,
  Code,
  Group,
  Select,
  Stack,
  Text,
  TextInput,
  Tooltip,
  type ComboboxItem,
  type ComboboxLikeRenderOptionInput,
} from '@mantine/core'
import { IconBraces, IconFunction, IconKey, IconLetterCase, IconPlus, IconX } from '@tabler/icons-react'
import { EXPRESSION_FUNCTIONS, groupByCategory, type ExpressionFunction } from '@/lib/functionCatalog'
import {
  formatValue,
  parseExpression,
  parseValue,
  type RootTokens,
  type ValueMode,
  type ValueNode,
} from '@/lib/valueExpression'

/** Deep enough for anything a person writes by hand; a bound keeps the layout readable. */
const MAX_DEPTH = 8

export type Kind = ValueNode['kind']

const KIND_OPTIONS: { value: Kind; label: string }[] = [
  { value: 'text', label: 'Text' },
  { value: 'field', label: 'Field' },
  { value: 'secret', label: 'Secret' },
  { value: 'call', label: 'Function' },
]

const KIND_ICON: Record<Kind, ReactNode> = {
  text: <IconLetterCase size="1rem" />,
  field: <IconBraces size="1rem" />,
  secret: <IconKey size="1rem" />,
  call: <IconFunction size="1rem" />,
}

/** What each kind's editor is called, after the slot's own name. */
const KIND_NOUN: Record<Kind, string> = { text: 'text', field: 'field', secret: 'secret', call: 'function' }

const BY_NAME = new Map(EXPRESSION_FUNCTIONS.map((f) => [f.name.toLowerCase(), f]))

const FUNCTION_OPTIONS = groupByCategory(EXPRESSION_FUNCTIONS).map(([group, members]) => ({
  group,
  items: members.map((f) => ({ value: f.name, label: f.name })),
}))

interface Parameter {
  name: string
  optional: boolean
}

interface Signature {
  params: Parameter[]
  /** Takes any number of arguments after the named ones. */
  variadic: boolean
}

/** `round(number, [decimals])` → number, decimals (optional); `concat(a, b, ...)` → a, b, and more. */
export function readSignature(signature: string): Signature {
  const inner = /\((.*)\)/.exec(signature)?.[1] ?? ''
  const params: Parameter[] = []
  let variadic = false
  for (const raw of inner.split(',').map((p) => p.trim()).filter(Boolean)) {
    if (raw === '...' || raw === '…') {
      variadic = true
      continue
    }
    const optional = raw.startsWith('[') && raw.endsWith(']')
    params.push({ name: optional ? raw.slice(1, -1).trim() : raw, optional })
  }
  return { params, variadic }
}

const signatureOf = (fn: string): Signature =>
  BY_NAME.has(fn.toLowerCase()) ? readSignature(BY_NAME.get(fn.toLowerCase())!.signature) : { params: [], variadic: true }

/** A catalog placeholder -- `value`, `' '`, `2` -- as the slot a new call starts with. */
function placeholderNode(placeholder: string): ValueNode {
  const literal = parseExpression(placeholder)
  if (literal && literal.kind === 'text') return literal
  return { kind: 'field', path: '' }
}

function emptyNode(kind: Kind): ValueNode {
  switch (kind) {
    case 'text':
      return { kind: 'text', value: '' }
    case 'field':
      return { kind: 'field', path: '' }
    case 'secret':
      return { kind: 'secret', name: '' }
    case 'call':
      return { kind: 'call', fn: '', args: [] }
  }
}

const isBlank = (node: ValueNode) =>
  (node.kind === 'text' && node.value === '') ||
  (node.kind === 'field' && node.path === '') ||
  (node.kind === 'secret' && node.name === '')

/**
 * A call to `fn`. What the slot held becomes the first argument, so turning a
 * field into a function wraps it -- `email` becomes `lower(source.email)` --
 * and the remaining arguments start as the catalog's placeholders.
 */
function startCall(fn: ExpressionFunction | undefined, name: string, previous: ValueNode | null): ValueNode {
  const placeholders = fn?.args ?? []
  const args = placeholders.map(placeholderNode)
  if (previous && !isBlank(previous) && args.length > 0) args[0] = previous
  return { kind: 'call', fn: name, args }
}

interface ValueBuilderProps {
  /** Names the root slot; every nested control is named from it. */
  label: string
  value: unknown
  onChange: (value: string) => void
  mode: ValueMode
  fieldPaths: string[]
  secretNames?: string[]
  /** How a field and a secret are written at the top of the value. */
  tokens?: RootTokens
  /** Called when the value cannot be shown here, to hand it back to the text input. */
  onEditAsText?: () => void
  /** What an empty value starts as: a field for "which field", text for "which value". */
  emptyKind?: Kind
}

/**
 * A value built from slots: text, a field, a secret of the vhost, or a
 * function whose arguments are slots again, to any depth. Every edit is
 * written back as the one string the engine reads (lib/valueExpression).
 *
 * The tree is held here as well as in the saved string, because a slot that
 * has not been filled in yet -- a field not picked -- saves as nothing and
 * would otherwise lose the shape around it.
 */
export function ValueBuilder({
  label,
  value,
  onChange,
  mode,
  fieldPaths,
  secretNames = [],
  tokens,
  onEditAsText,
  emptyKind = 'text',
}: ValueBuilderProps) {
  const saved = value === undefined || value === null ? '' : String(value)
  const emitted = useRef<string | null>(null)
  const read = (text: string) => (text.trim() === '' ? emptyNode(emptyKind) : parseValue(text, mode))
  const [tree, setTree] = useState<ValueNode | null>(() => read(saved))

  // A value changed from outside -- typed in the text input, or undone --
  // replaces the tree; the echo of our own change does not.
  useEffect(() => {
    if (saved !== emitted.current) setTree(read(saved))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved, mode])

  const commit = (next: ValueNode) => {
    setTree(next)
    const text = formatValue(next, mode, tokens)
    emitted.current = text
    onChange(text)
  }

  if (!tree) {
    const note = 'This value mixes text and tokens, so it is edited as text. The builder takes one text, field, secret or function.'
    if (onEditAsText) {
      return (
        <Stack gap={6}>
          <Text size="xs" c="dimmed">{note}</Text>
          <Group gap="xs">
            <Button size="compact-xs" variant="subtle" onClick={onEditAsText}>
              Keep editing as text
            </Button>
          </Group>
        </Stack>
      )
    }
    // No text input of its own around it: the value stays editable as typed.
    return (
      <Stack gap={6}>
        <TextInput
          aria-label={`${label} text`}
          value={saved}
          onChange={(e) => {
            emitted.current = e.currentTarget.value
            onChange(e.currentTarget.value)
          }}
          description={note}
          rightSection={
            <Tooltip label="Start again with the builder">
              <ActionIcon
                aria-label={`Rebuild ${label}`}
                variant="subtle"
                color="gray"
                onClick={() => commit(emptyNode(emptyKind))}
              >
                <IconX size="1rem" />
              </ActionIcon>
            </Tooltip>
          }
        />
      </Stack>
    )
  }

  return (
    <Stack gap="xs">
      <Slot
        name={label}
        node={tree}
        depth={0}
        onChange={commit}
        fieldPaths={fieldPaths}
        secretNames={secretNames}
      />
      {saved && tree.kind === 'call' && (
        <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
          Saved as <Code>{saved}</Code>
        </Text>
      )}
    </Stack>
  )
}

interface SlotProps {
  name: string
  node: ValueNode
  depth: number
  onChange: (node: ValueNode) => void
  fieldPaths: string[]
  secretNames: string[]
  /** Shown above the slot: the parameter this argument fills. */
  caption?: string
  onRemove?: () => void
}

function Slot({ name, node, depth, onChange, fieldPaths, secretNames, caption, onRemove }: SlotProps) {
  const kinds = useMemo(
    () => KIND_OPTIONS.map((k) => ({ ...k, disabled: k.value === 'call' && depth >= MAX_DEPTH })),
    [depth],
  )

  const switchKind = (kind: Kind) => {
    if (kind === node.kind) return
    if (kind === 'call') {
      onChange({ kind: 'call', fn: '', args: isBlank(node) ? [] : [node] })
    } else {
      onChange(emptyNode(kind))
    }
  }

  const editorLabel = `${name} ${KIND_NOUN[node.kind]}`

  let editor: ReactNode
  switch (node.kind) {
    case 'text':
      editor = (
        <TextInput
          aria-label={editorLabel}
          placeholder={node.bare ? '0' : 'Type a value'}
          value={node.value}
          onChange={(e) => {
            const text = e.currentTarget.value
            // A number typed where a number was keeps being written bare.
            const bare = node.bare && /^-?\d+(\.\d+)?$|^(true|false)$/.test(text)
            onChange(bare ? { kind: 'text', value: text, bare } : { kind: 'text', value: text })
          }}
        />
      )
      break
    case 'field':
      editor = (
        <Autocomplete
          aria-label={editorLabel}
          placeholder="Search fields"
          data={fieldPaths}
          value={node.path}
          onChange={(path) => onChange({ kind: 'field', path: path.trim() })}
          limit={50}
          comboboxProps={{ withinPortal: true }}
        />
      )
      break
    case 'secret':
      editor = (
        <Select
          aria-label={editorLabel}
          placeholder={secretNames.length ? 'Pick a secret' : 'No secrets in this vhost'}
          data={Array.from(new Set([...secretNames, ...(node.name ? [node.name] : [])]))}
          value={node.name || null}
          onChange={(secret) => onChange({ kind: 'secret', name: secret || '' })}
          searchable
          nothingFoundMessage="No secret by that name"
          comboboxProps={{ withinPortal: true }}
        />
      )
      break
    case 'call':
      editor = (
        <Select
          aria-label={editorLabel}
          placeholder="Pick a function"
          data={
            node.fn && !BY_NAME.has(node.fn.toLowerCase())
              ? [...FUNCTION_OPTIONS, { group: 'Other', items: [{ value: node.fn, label: node.fn }] }]
              : FUNCTION_OPTIONS
          }
          value={node.fn || null}
          onChange={(fn) => {
            if (!fn) return
            const previous = node.args[0] ?? null
            onChange(startCall(BY_NAME.get(fn.toLowerCase()), fn, previous))
          }}
          renderOption={renderFunctionOption}
          searchable
          allowDeselect={false}
          maxDropdownHeight={280}
          nothingFoundMessage="No function matches"
          comboboxProps={{ withinPortal: true, width: 320, position: 'bottom-start' }}
        />
      )
      break
  }

  return (
    <Stack gap={4} miw={0}>
      {caption && (
        <Text size="xs" fw={500} c="dimmed">
          {caption}
        </Text>
      )}
      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Select
          aria-label={`${name} type`}
          data={kinds}
          value={node.kind}
          onChange={(kind) => kind && switchKind(kind as Kind)}
          leftSection={KIND_ICON[node.kind]}
          allowDeselect={false}
          w={132}
          style={{ flexShrink: 0 }}
          comboboxProps={{ withinPortal: true }}
        />
        <Box style={{ flex: 1, minWidth: 0 }}>{editor}</Box>
        {onRemove && (
          <Tooltip label="Remove argument">
            <ActionIcon aria-label={`Remove ${name}`} variant="subtle" color="gray" size="input-sm" onClick={onRemove}>
              <IconX size="1rem" />
            </ActionIcon>
          </Tooltip>
        )}
      </Group>
      {node.kind === 'call' && node.fn && (
        <Arguments
          name={name}
          node={node}
          depth={depth}
          onChange={onChange}
          fieldPaths={fieldPaths}
          secretNames={secretNames}
        />
      )}
    </Stack>
  )
}

interface ArgumentsProps {
  name: string
  node: Extract<ValueNode, { kind: 'call' }>
  depth: number
  onChange: (node: ValueNode) => void
  fieldPaths: string[]
  secretNames: string[]
}

function Arguments({ name, node, depth, onChange, fieldPaths, secretNames }: ArgumentsProps) {
  const { params, variadic } = signatureOf(node.fn)
  const required = params.filter((p) => !p.optional).length
  const canAdd = variadic || node.args.length < params.length

  const setArg = (i: number, arg: ValueNode) => {
    const args = [...node.args]
    args[i] = arg
    onChange({ ...node, args })
  }

  const addArg = () => onChange({ ...node, args: [...node.args, { kind: 'text', value: '' }] })
  const removeArg = (i: number) => onChange({ ...node, args: node.args.filter((_, j) => j !== i) })

  if (params.length === 0 && !variadic) {
    return (
      <Text size="xs" c="dimmed" pl="md">
        Takes no arguments.
      </Text>
    )
  }

  return (
    <Stack
      gap="sm"
      pl="md"
      py={4}
      ml={6}
      style={{ borderLeft: '2px solid var(--mantine-color-default-border)' }}
    >
      {node.args.map((arg, i) => {
        const param = params[i]
        const argName = param ? param.name : `… ${i + 1}`
        return (
          <Slot
            key={i}
            name={`${name} › ${argName}`}
            caption={param ? `${param.name}${param.optional ? ' (optional)' : ''}` : `more (${i + 1})`}
            node={arg}
            depth={depth + 1}
            onChange={(next) => setArg(i, next)}
            fieldPaths={fieldPaths}
            secretNames={secretNames}
            onRemove={i >= required ? () => removeArg(i) : undefined}
          />
        )
      })}
      {canAdd && (
        <Group>
          <Button
            size="compact-sm"
            variant="subtle"
            leftSection={<IconPlus size="0.9rem" />}
            aria-label={`Add argument to ${node.fn}`}
            onClick={addArg}
          >
            Add argument
          </Button>
        </Group>
      )}
    </Stack>
  )
}

function renderFunctionOption({ option }: ComboboxLikeRenderOptionInput<ComboboxItem>) {
  const fn = BY_NAME.get(option.value.toLowerCase())
  return (
    <Stack gap={0} miw={0}>
      <Text size="sm" ff="monospace">
        {fn?.signature ?? option.label}
      </Text>
      {fn && (
        <Text size="xs" c="dimmed" lineClamp={2}>
          {fn.summary}
        </Text>
      )}
    </Stack>
  )
}

export default ValueBuilder
