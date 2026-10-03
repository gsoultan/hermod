import { useState } from 'react'
import { Alert, Button, Group, Modal, PasswordInput, Stack, Switch, Text, TextInput, Textarea } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { SECRET_NAME_PATTERN, saveVHostSecret, secretNameRule, vhostSecretsKey } from '@/lib/vhostSecrets'

/**
 * Adds a secret, or replaces the value of one (`name` given).
 *
 * The value box always starts empty. The API does not return a stored value,
 * so there is none to show, and rotating means typing the new one.
 */
export function SecretFormModal({ vhost, name, onClose }: { vhost: string; name?: string; onClose: () => void }) {
  const rotating = name !== undefined
  const queryClient = useQueryClient()
  const [newName, setNewName] = useState('')
  const [value, setValue] = useState('')
  const [multiline, setMultiline] = useState(false)
  const [nameError, setNameError] = useState<string | null>(null)
  const [valueError, setValueError] = useState<string | null>(null)

  const save = useMutation({
    mutationFn: () => saveVHostSecret(vhost, rotating ? (name as string) : newName.trim(), value),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: vhostSecretsKey(vhost) })
      onClose()
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const badName = !rotating && !SECRET_NAME_PATTERN.test(newName.trim())
    setNameError(badName ? `"${newName.trim()}" is not a valid name.` : null)
    setValueError(value === '' ? 'Enter the value to save.' : null)
    if (badName || value === '') return
    save.mutate()
  }

  const valueLabel = rotating ? 'New value' : 'Value'
  const valueDescription = 'Encrypted when saved, and never shown again.'

  return (
    <Modal opened onClose={onClose} title={rotating ? `Rotate ${name}` : 'Add secret'} centered>
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {rotating
              ? `Replaces the value of ${name} in ${vhost}. Running workflows read the new value on their next message.`
              : `Saved in ${vhost}, and readable only by workflows of ${vhost}.`}
          </Text>
          {!rotating && (
            <TextInput
              label="Name"
              description={`${secretNameRule} Workflows read it as secret("NAME").`}
              placeholder="API_KEY"
              required
              value={newName}
              onChange={(e) => { setNewName(e.currentTarget.value); setNameError(null) }}
              error={nameError}
              styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
              data-autofocus
            />
          )}
          {multiline ? (
            <Textarea
              label={valueLabel}
              description={valueDescription}
              required
              autosize
              minRows={4}
              maxRows={12}
              value={value}
              onChange={(e) => { setValue(e.currentTarget.value); setValueError(null) }}
              error={valueError}
              styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
            />
          ) : (
            <PasswordInput
              label={valueLabel}
              description={valueDescription}
              required
              value={value}
              onChange={(e) => { setValue(e.currentTarget.value); setValueError(null) }}
              error={valueError}
              visibilityToggleButtonProps={{ 'aria-label': 'Show what was typed' }}
              autoComplete="off"
              data-autofocus={rotating || undefined}
            />
          )}
          <Switch
            label="Multi-line value"
            description="For a private key or a JSON credential. The box is not masked."
            checked={multiline}
            onChange={(e) => setMultiline(e.currentTarget.checked)}
          />
          {save.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The secret was not saved">
              {(save.error as Error).message}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>Save secret</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
