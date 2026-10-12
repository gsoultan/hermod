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
 * The same call sent as a job: answered 202 at once with the job's id, then
 * read until it has finished. For many rows, or a call that may take longer
 * than the sync time limit.
 */
export function servingCurlAsync(vhost: string, name: string, body: Record<string, unknown>): string {
  return [
    servingCurl(vhost, name, { ...body, respond: 'async' }),
    `# Answers 202 {"job_id": "job_…", "status_url": …}. Read the job until its status is succeeded, failed or cancelled:`,
    `curl '${window.location.origin}/api/ml/serve/${encodeURIComponent(vhost)}/jobs/<job_id>' -H 'X-API-Key: <serving key>'`,
  ].join('\n')
}

/**
 * What to do with a version just trained: try it, put it live, or copy the
 * call an application makes. `workflow` says how a workflow uses it.
 */
export function NextSteps({ vhost, result, apiCall, asyncCall, onTry, workflow }: {
  vhost: string
  result: TrainResult
  apiCall: string
  /** The same call sent as a job ("respond": "async"). */
  asyncCall: string
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
        <CopyButton value={asyncCall}>
          {({ copied, copy }) => (
            <Button variant="default" leftSection={copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />} onClick={copy}>
              {copied ? 'Copied' : 'Copy async call'}
            </Button>
          )}
        </CopyButton>
      </Group>
      <Text size="xs" c="dimmed">
        {live ? 'It is live. ' : `It is not live yet${result.reason ? ` (${result.reason})` : ''}. `}
        {workflow} Applications call it with a serving key, made from the model&apos;s key button. A call has a time
        limit, 10 seconds unless set otherwise; for many rows, or a slow model, send it async and read the job it answers with.
      </Text>
      {promote.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The version was not put live">
          {(promote.error as Error).message}
        </Alert>
      )}
    </Stack>
  )
}
