import { useState } from 'react'
import { Alert, Button, Group, Loader, NumberInput, Paper, SimpleGrid, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconGauge } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  QUOTA_KEYS, getQuotas, mlQuotasKey, putQuotas, type MLQuotaSettings, type MLQuotasView, type QuotaKey,
} from '@/lib/mlModels'

const QUOTA_LABEL: Record<QuotaKey, string> = {
  max_datasets: 'Datasets',
  max_dataset_rows: 'Rows per dataset',
  max_dataset_bytes: 'Bytes per dataset',
  max_models: 'Models',
  max_concurrent_trainings: 'Concurrent trainings',
  max_predictions_per_second: 'Predictions per second',
}

/** A limit in words: 0 is no limit. */
function limitText(key: QuotaKey, n: number | null | undefined): string {
  if (!n) return 'No limit'
  return key === 'max_predictions_per_second' ? `${n} per second` : n.toLocaleString()
}

const ownSettings = (view: MLQuotasView): MLQuotaSettings =>
  Object.fromEntries(QUOTA_KEYS.map((k) => [k, view.quotas[k] ?? null])) as MLQuotaSettings

/**
 * The vhost's ML quotas. Anyone on the vhost sees the limits in force; an
 * Administrator sets the vhost's own, a blank one falling back to the server
 * default (HERMOD_ML_MAX_*), 0 meaning no limit.
 */
export function QuotasPanel({ vhost, canEdit }: { vhost: string; canEdit: boolean }) {
  const { data, isLoading, error } = useQuery({
    queryKey: mlQuotasKey(vhost),
    queryFn: ({ signal }) => getQuotas(vhost, signal),
    retry: false,
  })

  return (
    <Paper component="section" aria-label="ML quotas" p="md" withBorder radius="md">
      <Stack gap="sm">
        <Group gap="xs">
          <IconGauge size="1.3rem" color="var(--mantine-color-grape-filled)" />
          <Title order={4}>ML quotas</Title>
        </Group>
        <Text size="sm" c="dimmed">
          Limits on what {vhost} may hold and run. Predictions per second and concurrent trainings are counted on
          each Hermod replica separately.
        </Text>
        {isLoading ? (
          <Group><Loader size="xs" /><Text size="sm" c="dimmed">Loading quotas…</Text></Group>
        ) : error || !data ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The quotas could not be loaded">
            {(error as Error | null)?.message}
          </Alert>
        ) : canEdit ? (
          <QuotasForm key={JSON.stringify(data.quotas)} vhost={vhost} view={data} />
        ) : (
          <Table verticalSpacing="xs" maw={520}>
            <Table.Tbody>
              {QUOTA_KEYS.map((k) => (
                <Table.Tr key={k}>
                  <Table.Td><Text size="sm">{QUOTA_LABEL[k]}</Text></Table.Td>
                  <Table.Td><Text size="sm" fw={500}>{limitText(k, data.effective[k])}</Text></Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{data.quotas[k] == null ? 'server default' : 'set for this vhost'}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Paper>
  )
}

function QuotasForm({ vhost, view }: { vhost: string; view: MLQuotasView }) {
  const queryClient = useQueryClient()
  const [values, setValues] = useState<MLQuotaSettings>(() => ownSettings(view))
  const save = useMutation({
    mutationFn: () => putQuotas(vhost, values),
    onSuccess: (next) => queryClient.setQueryData(mlQuotasKey(vhost), next),
  })

  return (
    <Stack gap="sm">
      <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }}>
        {QUOTA_KEYS.map((k) => (
          <NumberInput
            key={k}
            label={QUOTA_LABEL[k]}
            description={`In force: ${limitText(k, view.effective[k])}`}
            placeholder={`Server default: ${limitText(k, view.defaults[k])}`}
            min={0}
            allowNegative={false}
            allowDecimal={k === 'max_predictions_per_second'}
            thousandSeparator={false}
            value={values[k] ?? ''}
            onChange={(v) => setValues((cur) => ({ ...cur, [k]: v === '' ? null : Number(v) }))}
          />
        ))}
      </SimpleGrid>
      <Text size="xs" c="dimmed">Leave a quota blank for the server default; 0 means no limit.</Text>
      {save.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The quotas were not saved">
          {(save.error as Error).message}
        </Alert>
      )}
      <Group>
        <Button size="sm" loading={save.isPending} onClick={() => save.mutate()}>Save quotas</Button>
      </Group>
    </Stack>
  )
}
