import { useContext } from 'react'
import { Alert, Anchor, Autocomplete, Button, Code, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconKey } from '@tabler/icons-react'
import { SecretNamesContext } from '@/components/shared/TemplateField'
import { SECRET_NAME_PATTERN, secretNameRule } from '@/lib/vhostSecrets'
import { isPlaintextKey, requiresApiKey, secretNameFromKey, secretToken } from './aiProviders'

interface AIKeyFieldProps {
  /** The config key holding the key: `apiKey`, `fallbackApiKey`, or ai_retrieve's `storeApiKey`. */
  configKey: 'apiKey' | 'fallbackApiKey' | 'storeApiKey'
  label: string
  provider: string | undefined
  value: unknown
  onChange: (patch: Record<string, string>) => void
  /** Overrides whether the key is required, for a key that is not a provider's. */
  required?: boolean
  /** Example secret name for the placeholder. */
  example?: string
}

/**
 * The API key, chosen as the name of a vhost secret.
 *
 * The node stores {{secret("NAME")}} and the engine resolves it for the
 * message's vhost (genai.connection), so the key itself is never saved in
 * the workflow or carried by an export. A key that was typed in before — or
 * arrived in an imported workflow — is called out and can be cleared, the
 * same thing workflow validation warns about.
 */
export function AIKeyField({ configKey, label, provider, value, onChange, required, example = 'OPENAI_API_KEY' }: AIKeyFieldProps) {
  const isRequired = required ?? requiresApiKey(provider)
  const secretNames = useContext(SecretNamesContext)
  const name = secretNameFromKey(value)
  const plaintext = isPlaintextKey(value)
  const otherExpression = !plaintext && !name && typeof value === 'string' && value.trim() !== ''
  const invalid = name !== '' && !SECRET_NAME_PATTERN.test(name)

  return (
    <Stack gap="xs">
      {plaintext && (
        <Alert
          color="yellow"
          variant="light"
          icon={<IconAlertTriangle size="1rem" />}
          title="API key stored in the workflow"
        >
          <Stack gap="xs">
            <Text size="sm">
              This key was typed into the node, so it is saved with the workflow and included in every
              export. Save it as a vhost secret, choose that secret below, and clear the stored key.
            </Text>
            <Button
              size="xs"
              color="yellow"
              variant="light"
              w="fit-content"
              onClick={() => onChange({ [configKey]: '' })}
            >
              Clear stored key
            </Button>
          </Stack>
        </Alert>
      )}
      <Autocomplete
        label={label}
        placeholder={secretNames.length ? 'Pick or type a secret name' : `e.g. ${example}`}
        leftSection={<IconKey size="1rem" />}
        data={secretNames}
        value={name}
        onChange={(v) => onChange({ [configKey]: secretToken(v) })}
        required={isRequired}
        error={invalid ? `A secret name uses ${secretNameRule.charAt(0).toLowerCase()}${secretNameRule.slice(1)}` : undefined}
        description={
          <>
            The name of a secret saved on the{' '}
            <Anchor href="/secrets" target="_blank" size="xs" inherit>
              Secrets
            </Anchor>{' '}
            page for this workflow&apos;s vhost. The node stores <Code>{'{{secret("NAME")}}'}</Code>, never the key.
            {!isRequired && ' Optional for this provider.'}
          </>
        }
      />
      {otherExpression && (
        <Text size="xs" c="dimmed">
          The key is currently the expression <Code>{String(value)}</Code>. Choosing a secret replaces it.
        </Text>
      )}
    </Stack>
  )
}
