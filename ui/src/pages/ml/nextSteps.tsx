import { Alert, Badge, Button, CopyButton, Group, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy, IconPlayerPlay, IconRocket } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { mlModelsKey, promoteVersion, servingUrl, versionsKey, type TrainResult } from '@/lib/mlModels'

/** The curl an application runs to call model `name` with `body`. */
export function servingCurl(vhost: string, name: string, body: unknown): string {
  return [
    `curl -X POST '${window.location.origin}${servingUrl(vhost, name)}' \\`,
    `  -H 'X-API-Key: <serving key>' -H 'Content-Type: application/json' \\`,
    `  -d '${JSON.stringify(body)}'`,
  ].join('\n')
}

/**
 * What to do with a version just trained: try it, put it live, or copy the
 * call an application makes. `workflow` says how a workflow uses it.
 */
export function NextSteps({ vhost, result, apiCall, onTry, workflow }: {
  vhost: string
  result: TrainResult
  apiCall: string
  onTry: () => void
  workflow: string
}) {
  const queryClient = useQueryClient()
  const v = result.version
  const name = result.model?.name ?? v.model
  const promote = useMutation({
    mutationFn: () => promoteVersion(vhost, name, v.version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      queryClient.invalidateQueries({ queryKey: versionsKey(vhost, name) })
    },
  })
  const live = result.live || promote.isSuccess
  return (
    <Stack gap="xs">
      <Text fw={600} size="sm">Next steps</Text>
      <Group gap="xs">
        <Button variant="light" leftSection={<IconPlayerPlay size="1rem" />} onClick={onTry}>Try it</Button>
        {live ? (
          <Badge color="green" variant="light" size="lg" tt="none">Live</Badge>
        ) : (
          <Button variant="light" color="green" leftSection={<IconRocket size="1rem" />} loading={promote.isPending}
            onClick={() => promote.mutate()}>
            Make it live
          </Button>
        )}
        <CopyButton value={apiCall}>
          {({ copied, copy }) => (
            <Button variant="default" leftSection={copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />} onClick={copy}>
              {copied ? 'Copied' : 'Copy API call'}
            </Button>
          )}
        </CopyButton>
      </Group>
      <Text size="xs" c="dimmed">
        {live ? 'It is live. ' : `It is not live yet${result.reason ? ` (${result.reason})` : ''}. `}
        {workflow} Applications call it with a serving key, made from the model&apos;s key button.
      </Text>
      {promote.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The version was not put live">
          {(promote.error as Error).message}
        </Alert>
      )}
    </Stack>
  )
}
