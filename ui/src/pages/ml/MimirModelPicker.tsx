import { useState } from 'react'
import { Alert, Button, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconAlertCircle, IconRefresh } from '@tabler/icons-react'
import { useMutation } from '@tanstack/react-query'
import { listMimirModels, type MimirModel } from '@/lib/mlModels'

/** What the picker shows for one model: its name, what it is and how it does. */
export function mimirModelLabel(m: MimirModel): string {
  const parts = [m.name]
  if (m.architecture) parts.push(m.architecture)
  if (m.ready && m.accuracy) parts.push(`${Math.round(m.accuracy * 100)}% right`)
  parts.push(m.ready ? 'ready' : m.status || 'not ready')
  return parts.join(' · ')
}

/**
 * Picks the model on a mimir server. Fetch models turns the address and key
 * secret already typed into a list to pick from; the pick fills the model's
 * id, and `onPick` hands the model back so the form can pre-fill the rest. A
 * server the browser's Hermod cannot reach is still usable: the id can be
 * typed instead.
 */
export function MimirModelPicker({ vhost, url, tokenSecret, value, error, onChange, onPick, canFetch }: {
  vhost: string
  url: string
  tokenSecret: string
  value: string
  error?: string
  onChange: (id: string) => void
  onPick: (m: MimirModel) => void
  /** Why Fetch models is off, such as a missing address; undefined when it can run. */
  canFetch?: string
}) {
  const [typing, setTyping] = useState(false)
  const fetchModels = useMutation({
    mutationFn: () => listMimirModels(vhost, url.trim(), tokenSecret.trim()),
  })
  const models = fetchModels.data
  const listed = models !== undefined && !typing

  const options = (models ?? []).map((m) => ({ value: m.id, label: mimirModelLabel(m), disabled: !m.ready }))
  // A saved id the server no longer lists stays selectable, so editing does not lose it.
  if (value && listed && !options.some((o) => o.value === value)) options.unshift({ value, label: `${value} (not on the server now)`, disabled: false })

  return (
    <Stack gap="xs">
      <Group justify="space-between" align="flex-end" wrap="nowrap">
        <Text size="sm" fw={500} component="label" htmlFor="mimir-model">Model on the server</Text>
        <Button size="compact-sm" variant="default" leftSection={<IconRefresh size="0.9rem" />}
          loading={fetchModels.isPending} disabled={!!canFetch} title={canFetch}
          onClick={() => { setTyping(false); fetchModels.mutate() }}>
          Fetch models
        </Button>
      </Group>
      {listed ? (
        <Select id="mimir-model" data={options} value={value || null} searchable error={error}
          placeholder={options.length ? 'Pick a model' : 'The server lists no models'}
          nothingFoundMessage="No model by that name"
          description={models.length === 0
            ? 'This key sees no models. Upload or train one on the mimir server, or use a key with access to one.'
            : `${models.length} ${models.length === 1 ? 'model' : 'models'} found. A model that is not ready is listed but cannot be picked.`}
          onChange={(id) => {
            onChange(id ?? '')
            const m = models.find((x) => x.id === id)
            if (m) onPick(m)
          }} />
      ) : (
        <TextInput id="mimir-model" placeholder="the model's id, such as 01HZX…" error={error} value={value}
          description="Press Fetch models to pick from the server, or enter the id shown on its model page."
          onChange={(e) => onChange(e.currentTarget.value)} />
      )}
      {listed && (
        <Button size="compact-xs" variant="subtle" w="fit-content" onClick={() => setTyping(true)}>Enter the id instead</Button>
      )}
      {fetchModels.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="Hermod could not list the server's models">
          {(fetchModels.error as Error).message}
        </Alert>
      )}
    </Stack>
  )
}
