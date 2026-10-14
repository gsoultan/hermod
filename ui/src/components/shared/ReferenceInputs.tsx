import { forwardRef, useContext, useRef, useState, type ReactNode, type Ref } from 'react'
import {
  ActionIcon,
  Box,
  Button,
  Group,
  PasswordInput as MantinePasswordInput,
  Popover,
  ScrollArea,
  Stack,
  Text,
  TextInput as MantineTextInput,
  Tooltip,
  UnstyledButton,
  type PasswordInputProps,
  type TextInputProps,
} from '@mantine/core'
import { IconKey } from '@tabler/icons-react'
import { SecretNamesContext } from './TemplateField'

/**
 * Text and password inputs for a source, sink or transformation setting. They
 * are Mantine's, with one addition: a button beside the input that inserts a
 * secret of the vhost, or a server environment variable, at the cursor.
 *
 * The engine resolves both in every setting (secrets.ExpandReferences for a
 * connector, when it connects; registry.expandNodeConfig for a node, per
 * message): a secret from the vhost's secrets then the secret manager, an env
 * variable from HERMOD_ENV_<NAME> on the server. Only names reach the browser.
 *
 * Pass `references={false}` for an input where a reference means nothing.
 */

/** What the server reads: secrets.referenceName. */
const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/** The standard spelling, the one every setting reads: secrets.DottedReference. */
export const secretToken = (name: string) => `{{secret.${name}}}`
export const envToken = (name: string) => `{{env.${name}}}`

/**
 * Puts text into the input at its selection, through the native setter and an
 * input event, so React's onChange sees a real change whichever way the form
 * listens -- an event handler, a Mantine form's getInputProps, or neither.
 */
function insertAtCursor(el: HTMLInputElement, text: string) {
  const start = el.selectionStart ?? el.value.length
  const end = el.selectionEnd ?? start
  const next = el.value.slice(0, start) + text + el.value.slice(end)
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  setter?.call(el, next)
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.focus()
  const caret = start + text.length
  el.setSelectionRange(caret, caret)
}

function ReferenceMenu({ onInsert }: { onInsert: (token: string) => void }) {
  const secretNames = useContext(SecretNamesContext)
  const [opened, setOpened] = useState(false)
  const [envName, setEnvName] = useState('')
  const envValid = NAME.test(envName)

  const insert = (token: string) => {
    setOpened(false)
    setEnvName('')
    onInsert(token)
  }

  return (
    <Popover opened={opened} onChange={setOpened} position="bottom-end" width={280} shadow="md" trapFocus withinPortal>
      <Popover.Target>
        <Tooltip label="Insert a secret or environment variable" withinPortal>
          <ActionIcon
            variant="subtle"
            color="gray"
            onClick={() => setOpened((o) => !o)}
            // Not named after the field: a label query for the field must find
            // the input alone.
            aria-label="Insert a secret or environment variable"
          >
            <IconKey size="1rem" />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap="sm">
          <Stack gap={4}>
            <Text size="xs" fw={600} c="dimmed">Secrets</Text>
            {secretNames.length === 0 ? (
              <Text size="xs" c="dimmed">No secrets saved in this vhost yet.</Text>
            ) : (
              <ScrollArea.Autosize mah={180}>
                <Stack gap={2}>
                  {secretNames.map((name) => (
                    <UnstyledButton
                      key={name}
                      onClick={() => insert(secretToken(name))}
                      px={6}
                      py={2}
                      style={{ fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-xs)' }}
                    >
                      {name}
                    </UnstyledButton>
                  ))}
                </Stack>
              </ScrollArea.Autosize>
            )}
          </Stack>
          <Stack gap={4}>
            <MantineTextInput
              size="xs"
              label="Environment variable"
              description="Read from HERMOD_ENV_<NAME> on the Hermod server."
              placeholder="PG_HOST"
              value={envName}
              onChange={(e) => setEnvName(e.currentTarget.value.trim())}
              error={envName !== '' && !envValid ? 'Letters, digits and underscores, starting with a letter.' : undefined}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && envValid) {
                  e.preventDefault()
                  insert(envToken(envName))
                }
              }}
            />
            <Button size="xs" variant="light" disabled={!envValid} onClick={() => insert(envToken(envName))}>
              Insert variable
            </Button>
          </Stack>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

interface ReferenceProps {
  /** Offer secrets and env variables. On unless the input is disabled or read-only. */
  references?: boolean
}

function assignRef<T>(ref: Ref<T> | undefined, value: T) {
  if (typeof ref === 'function') ref(value)
  else if (ref) (ref as { current: T }).current = value
}

/** The wrapper both inputs share: the input, then the menu, on one row. */
function useReferenceContainer(
  forwarded: Ref<HTMLInputElement>,
  props: { disabled?: boolean; readOnly?: boolean; references?: boolean; inputContainer?: (children: ReactNode) => ReactNode },
) {
  const inputRef = useRef<HTMLInputElement | null>(null)
  const setRef = (el: HTMLInputElement | null) => {
    inputRef.current = el
    assignRef(forwarded, el)
  }
  const enabled = props.references !== false && !props.disabled && !props.readOnly

  const inputContainer = (children: ReactNode) => {
    const inner = props.inputContainer ? props.inputContainer(children) : children
    if (!enabled) return inner
    return (
      <Group gap={4} wrap="nowrap" align="center">
        <Box flex={1} miw={0}>{inner}</Box>
        <ReferenceMenu onInsert={(token) => inputRef.current && insertAtCursor(inputRef.current, token)} />
      </Group>
    )
  }
  return { setRef, inputContainer }
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps & ReferenceProps>(function TextInput(
  { references, ...props },
  ref,
) {
  const { setRef, inputContainer } = useReferenceContainer(ref, { ...props, references })
  return <MantineTextInput {...props} ref={setRef} inputContainer={inputContainer} />
})

export const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps & ReferenceProps>(function PasswordInput(
  { references, ...props },
  ref,
) {
  const { setRef, inputContainer } = useReferenceContainer(ref, { ...props, references })
  return <MantinePasswordInput {...props} ref={setRef} inputContainer={inputContainer} />
})
