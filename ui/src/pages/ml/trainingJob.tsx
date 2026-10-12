import { useEffect, useRef, useState } from 'react'
import { Button, Group, Loader, Progress, Stack, Text } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  cancelJob, getJob, jobFinished, jobResult, mlJobKey, mlJobsKey, startTraining, type MLJob, type TrainResult, type TrainSpec,
} from '@/lib/mlModels'

/** How often a training job is read while it runs. */
export const JOB_POLL_MS = 1000

/**
 * Trains as a job: starts it with "respond": "async" and follows it until it
 * ends. `onTrained` is told once, with the result, when it succeeds. Against
 * a server that trains while the request waits, the result comes back at once.
 */
export function useTraining(vhost: string, onTrained: (result: TrainResult) => void) {
  const queryClient = useQueryClient()
  const [jobId, setJobId] = useState<string | null>(null)

  const start = useMutation({
    mutationFn: ({ name, spec }: { name: string; spec: TrainSpec }) => startTraining(vhost, name, spec),
    onMutate: () => setJobId(null),
    onSuccess: (res) => {
      if (res.job) {
        setJobId(res.job.job_id)
        queryClient.invalidateQueries({ queryKey: mlJobsKey(vhost) })
      }
    },
  })

  const job = useQuery({
    queryKey: mlJobKey(vhost, jobId ?? ''),
    queryFn: ({ signal }) => getJob(vhost, jobId as string, signal),
    enabled: !!jobId,
    retry: false,
    refetchInterval: (q) => (jobFinished(q.state.data?.status) ? false : JOB_POLL_MS),
  })
  const current = jobId ? job.data : undefined
  const succeeded = current?.status === 'succeeded' ? current : undefined

  const result = useQuery({
    queryKey: [...mlJobKey(vhost, jobId ?? ''), 'result'],
    queryFn: ({ signal }) => jobResult<TrainResult>(vhost, succeeded as MLJob, signal),
    enabled: !!succeeded,
    retry: false,
  })

  const cancel = useMutation({
    mutationFn: () => cancelJob(vhost, jobId as string),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mlJobKey(vhost, jobId ?? '') })
      queryClient.invalidateQueries({ queryKey: mlJobsKey(vhost) })
    },
  })

  const trained = start.data?.result ?? (succeeded ? result.data : undefined)
  const told = useRef<TrainResult | undefined>(undefined)
  useEffect(() => {
    if (trained && told.current !== trained) {
      told.current = trained
      queryClient.invalidateQueries({ queryKey: mlJobsKey(vhost) })
      onTrained(trained)
    }
  }, [trained, onTrained, queryClient, vhost])

  const failed = current?.status === 'failed' ? new Error(current.error || 'The training failed and gave no reason.') : null
  const error = (start.error ?? job.error ?? result.error ?? failed) as Error | null
  const running = start.isPending || (!!jobId && !error && !jobFinished(current?.status)) || (!!succeeded && result.isPending)

  return {
    start: (name: string, spec: TrainSpec) => start.mutate({ name, spec }),
    running,
    job: current,
    trained,
    error,
    cancelled: current?.status === 'cancelled',
    cancel: () => cancel.mutate(),
    cancelling: cancel.isPending || !!current?.cancel_requested,
    cancelError: cancel.error as Error | null,
  }
}

/** How far a training job has got, in a bar and in words, with a way to stop it. */
export function TrainingProgress({ job, about, cancelling, onCancel }: {
  job?: MLJob
  about: string
  cancelling: boolean
  onCancel: () => void
}) {
  const percent = Math.round((job?.progress ?? 0) * 100)
  const words = !job ? 'Starting the training…'
    : job.status === 'queued' ? 'Waiting for a free training slot…'
    : `${job.progress_message || 'Training on the ML worker'} (${percent}%)`
  return (
    <Stack gap={6} aria-live="polite">
      <Group gap="xs" wrap="nowrap">
        <Loader size="xs" />
        <Text size="sm">{words}</Text>
      </Group>
      <Progress value={job ? Math.max(percent, 2) : 2} animated aria-label="Training progress" />
      <Text size="xs" c="dimmed">
        {about}You can close this window: the training goes on, and the Jobs list on the Machine Learning page shows how it went.
      </Text>
      {job && (
        <Group>
          <Button size="compact-sm" variant="light" color="red" loading={cancelling} onClick={onCancel}>Stop training</Button>
        </Group>
      )}
    </Stack>
  )
}
