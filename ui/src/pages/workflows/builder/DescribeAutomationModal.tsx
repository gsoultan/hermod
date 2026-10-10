import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  Alert, Button, Group, Modal, Select, Stack, Text, Textarea, TextInput, Title,
} from '@mantine/core'
import { IconAlertCircle, IconSparkles } from '@tabler/icons-react'
import { SecretNamesContext } from '@/components/shared/TemplateField'
import { AIKeyField } from '@/components/workflow/Transformation/configs/ai/AIKeyField'
import {
  modelPlaceholder, providerSelectData, requiresApiKey, requiresBaseUrl, showsBaseUrl, usesApiKey,
} from '@/components/workflow/Transformation/configs/ai/aiProviders'
import { useVHostSecretNames } from '@/lib/vhostSecrets'
import { buildWorkflow, MAX_DESCRIPTION_LENGTH, type DraftWorkflow } from '@/lib/workflowBuilder'
import { DraftPreview } from './DraftPreview'

interface DescribeAutomationModalProps {
  opened: boolean
  onClose: () => void
  defaultVHost: string
  availableVHosts: string[]
  /** Opens the draft in the editor as a new, unsaved workflow. */
  onOpenInEditor: (workflow: DraftWorkflow) => void
}

/**
 * Drafts a workflow from a plain-language description with a model the user
 * names, and previews the draft with what validation found. Nothing is saved
 * or started: the draft only goes as far as the editor.
 */
export function DescribeAutomationModal({
  opened, onClose, defaultVHost, availableVHosts, onOpenInEditor,
}: DescribeAutomationModalProps) {
  const [description, setDescription] = useState('')
  const [vhost, setVHost] = useState(defaultVHost)
  const [provider, setProvider] = useState<string | null>(null)
  const [model, setModel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const secretNames = useVHostSecretNames(vhost, opened)

  const draft = useMutation({
    mutationFn: buildWorkflow,
    // Shown in the dialog; a toast on top would say it twice.
    onError: () => {},
  })

  const tooLong = description.length > MAX_DESCRIPTION_LENGTH
  const canDraft =
    description.trim() !== '' &&
    !tooLong &&
    !!vhost &&
    !!provider &&
    model.trim() !== '' &&
    (!requiresApiKey(provider) || apiKey !== '') &&
    (!requiresBaseUrl(provider) || baseUrl.trim() !== '')

  const submit = () => {
    if (!canDraft || !provider) return
    draft.mutate({
      description: description.trim(),
      vhost,
      connection: { provider, model: model.trim(), apiKey, baseUrl: baseUrl.trim() },
    })
  }

  const vhostOptions = Array.from(new Set([defaultVHost, ...availableVHosts])).filter(Boolean)

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="xl"
      title={
        <Group gap="xs">
          <IconSparkles size="1.1rem" color="var(--mantine-color-grape-filled)" aria-hidden />
          <Text fw={700}>Describe an automation</Text>
        </Group>
      }
    >
      <Stack gap="md">
        <Textarea
          label="Describe the automation"
          description={`What should happen, from what triggers it to where the result goes. ${description.length.toLocaleString()} / ${MAX_DESCRIPTION_LENGTH.toLocaleString()} characters`}
          placeholder="When a support email arrives, classify it as billing, bug or other, and post bugs to the engineering channel."
          value={description}
          onChange={(e) => setDescription(e.currentTarget.value)}
          error={tooLong ? `Keep the description under ${MAX_DESCRIPTION_LENGTH.toLocaleString()} characters.` : undefined}
          autosize
          minRows={4}
          maxRows={12}
          required
        />

        <Stack gap="xs">
          <Title order={6}>Model to draft with</Title>
          <Text size="xs" c="dimmed">
            Used once to write the draft. The key is chosen from this vhost&apos;s secrets and never leaves the server.
          </Text>
          <Group grow align="flex-start">
            {vhostOptions.length > 1 && (
              <Select
                label="Virtual host"
                description="Where the draft's sources and sinks come from."
                data={vhostOptions}
                value={vhost}
                onChange={(v) => v && setVHost(v)}
                allowDeselect={false}
              />
            )}
            <Select
              label="Provider"
              data={providerSelectData}
              value={provider}
              onChange={(v) => {
                setProvider(v)
                if (!usesApiKey(v ?? undefined)) setApiKey('')
              }}
              placeholder="Choose a provider"
              required
            />
            <TextInput
              label="Model"
              placeholder={modelPlaceholder(provider ?? undefined)}
              value={model}
              onChange={(e) => setModel(e.currentTarget.value)}
              required
            />
          </Group>
          {usesApiKey(provider ?? undefined) && (
            <SecretNamesContext.Provider value={secretNames}>
              <AIKeyField
                configKey="apiKey"
                label="API key secret"
                provider={provider ?? undefined}
                value={apiKey}
                onChange={(patch) => setApiKey(patch.apiKey ?? '')}
              />
            </SecretNamesContext.Provider>
          )}
          {showsBaseUrl(provider ?? undefined) && (
            <TextInput
              label="Base URL"
              placeholder="http://localhost:11434"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.currentTarget.value)}
              required={requiresBaseUrl(provider ?? undefined)}
            />
          )}
        </Stack>

        {draft.isError && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="No draft was made">
            {(draft.error as Error).message}
          </Alert>
        )}

        {draft.data && <DraftPreview result={draft.data} onOpenInEditor={() => onOpenInEditor(draft.data.workflow)} />}

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Close</Button>
          <Button
            leftSection={<IconSparkles size="1rem" />}
            disabled={!canDraft}
            loading={draft.isPending}
            onClick={submit}
            variant={draft.data ? 'light' : 'filled'}
          >
            {draft.data ? 'Draft workflow again' : 'Draft workflow'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
