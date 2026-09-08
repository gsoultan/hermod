import {
  Alert,
  Card,
  Group,
  PasswordInput,
  Select,
  Stack,
  TagsInput,
  Text,
  ThemeIcon,
  rem,
} from '@mantine/core'
import { useMemo } from 'react'
import { IconAlertTriangle, IconInfoCircle, IconKey, IconLock, IconLockOpen } from '@tabler/icons-react'

interface EncryptConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  availableFields?: any[]
  transType?: string
}

/**
 * Configures both the `encrypt` and `decrypt` transformations.
 *
 * They take the same options and differ only in direction, so one editor serves
 * both; `transType` decides the wording and whether the failure-policy control
 * is shown (it only applies to decryption).
 */
export function EncryptConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  transType,
}: EncryptConfigProps) {
  const decrypting = transType === 'decrypt'

  const fieldPaths = useMemo(
    () => (availableFields || []).map((f) => (typeof f === 'string' ? f : f.path)).filter(Boolean),
    [availableFields],
  )

  // The backend accepts a single `field`, a `fields` array, or a comma-separated
  // string. The editor always writes the array and folds any legacy single
  // `field` into it, so a node configured before this control existed keeps
  // working and is migrated the first time it is edited.
  const fields: string[] = useMemo(() => {
    if (Array.isArray(config.fields)) return config.fields
    if (typeof config.fields === 'string' && config.fields.trim()) {
      return config.fields.split(',').map((f: string) => f.trim()).filter(Boolean)
    }
    if (typeof config.field === 'string' && config.field.trim()) return [config.field.trim()]
    return []
  }, [config.fields, config.field])

  return (
    <Stack gap="md">
      <Card withBorder radius="md" p="md">
        <Group gap="xs" mb="sm">
          <ThemeIcon variant="light" color="violet" size="sm">
            {decrypting ? <IconLockOpen size={rem(14)} /> : <IconLock size={rem(14)} />}
          </ThemeIcon>
          <Text fw={500} size="sm">
            {decrypting ? 'Fields to decrypt' : 'Fields to encrypt'}
          </Text>
        </Group>

        <Stack gap="sm">
          <TagsInput
            label="Fields"
            data={fieldPaths}
            value={fields}
            onChange={(next) =>
              // `field` is cleared so the two keys can never disagree.
              updateNodeConfig(nodeId, { fields: next, field: undefined })
            }
            placeholder="ssn, user.email"
            description={
              decrypting
                ? 'Dotted paths address nested values, for example user.email. List the same fields the encrypt node was given.'
                : 'Dotted paths address nested values, for example user.email. There is no wildcard: encrypting every field would destroy keys and routing columns.'
            }
            size="sm"
            clearable
          />

          <PasswordInput
            label="Encryption key"
            value={config.key || ''}
            onChange={(e) => updateNodeConfig(nodeId, { key: e.currentTarget.value })}
            placeholder="Key used to derive AES-256-GCM"
            description="Any length; hashed to 256 bits. Encrypt and decrypt nodes must use exactly the same key."
            size="sm"
            leftSection={<IconKey size={rem(16)} />}
          />

          {decrypting && (
            <Select
              label="On decryption failure"
              data={[
                { label: 'Fail the message (recommended)', value: 'fail' },
                { label: 'Skip — leave the value encrypted', value: 'skip' },
                { label: 'Null — clear the value', value: 'null' },
              ]}
              value={config.onError || 'fail'}
              onChange={(val) => updateNodeConfig(nodeId, { onError: val || 'fail' })}
              size="sm"
              description="Applies when a value cannot be authenticated — a wrong key or an altered ciphertext."
            />
          )}
        </Stack>
      </Card>

      <Alert icon={<IconAlertTriangle size="1rem" />} color="orange" variant="light">
        <Text size="sm">
          This key is stored with the workflow definition. Anyone who can read or export this
          workflow can read the key, and rotating it does not re-encrypt data already written
          under the old one.
        </Text>
      </Alert>

      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
        <Text size="sm">
          AES-256-GCM with a fresh random nonce per value, so the same input encrypts differently
          every time — an encrypted field cannot be used as a join or lookup key downstream.
          Values are written as <Text span ff="monospace">enc:v1:…</Text>; already-encrypted values
          are left alone, so re-running a workflow will not encrypt them twice. Name leaf fields
          only: a field holding an object or array is refused rather than stringified.
        </Text>
      </Alert>
    </Stack>
  )
}
