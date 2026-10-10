import { useState } from 'react'
import { ActionIcon, Alert, Button, Code, CopyButton, Group, Modal, Stack, Text, Tooltip } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { disableServing, mlModelsKey, rotateServingKey, servingUrl, type MLModel } from '@/lib/mlModels'

/**
 * Turns serving on (makes a key), replaces the key, or turns serving off. The
 * key is shown here once: only its hash is stored.
 */
export function ServingKeyModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [key, setKey] = useState<string | null>(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
  const make = useMutation({ mutationFn: () => rotateServingKey(vhost, model.name), onSuccess: (k) => { setKey(k); refresh() } })
  const off = useMutation({ mutationFn: () => disableServing(vhost, model.name), onSuccess: () => { refresh(); onClose() } })
  const url = servingUrl(vhost, model.name)
  const error = make.error ?? off.error

  return (
    <Modal opened onClose={onClose} title={`Serving key for ${model.name}`} centered size="lg">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          An application calls the model with <Code>POST {url}</Code> and the key as the <Code>X-API-Key</Code> header,
          or over gRPC with <Code>hermod.ml.v1.InferenceService/Predict</Code> and the key as <Code>x-api-key</Code> metadata.
        </Text>
        {key ? (
          <Alert color="green" title="Copy the key now: it is not shown again">
            <Group gap="xs" wrap="nowrap">
              <Code style={{ wordBreak: 'break-all' }}>{key}</Code>
              <CopyButton value={key}>
                {({ copied, copy }) => (
                  <Tooltip label={copied ? 'Copied' : 'Copy'}>
                    <ActionIcon variant="subtle" onClick={copy} aria-label="Copy the key">
                      {copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />}
                    </ActionIcon>
                  </Tooltip>
                )}
              </CopyButton>
            </Group>
          </Alert>
        ) : (
          <Text size="sm">
            {model.serving
              ? 'Serving is on. Making a new key stops the old one working at once.'
              : 'Serving is off. Applications cannot call this model until a key exists.'}
          </Text>
        )}
        {error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="That did not work">
            {(error as Error).message}
          </Alert>
        )}
        <Group justify="flex-end">
          {model.serving && !key && (
            <Button variant="light" color="red" onClick={() => off.mutate()} loading={off.isPending}>Turn off serving</Button>
          )}
          {!key && (
            <Button onClick={() => make.mutate()} loading={make.isPending}>
              {model.serving ? 'Make a new key' : 'Make a key'}
            </Button>
          )}
          {key && <Button onClick={onClose}>Done</Button>}
        </Group>
      </Stack>
    </Modal>
  )
}
