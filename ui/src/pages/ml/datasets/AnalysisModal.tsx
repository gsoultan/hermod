import { useEffect, type ReactNode } from 'react'
import { Alert, Group, Loader, Modal, SimpleGrid, Stack, Tabs, Text } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useDataset } from '@/lib/mlModels'
import { markAnalyzed, useDatasetProfile, type TrainStart } from '@/lib/mlDataProfile'
import { DatasetPreview } from '../datasetPreview'
import { ColumnCard, Correlations, GoodForList, SummaryCard, WarningsList } from './analysisParts'

export type AnalysisTab = 'overview' | 'columns' | 'related' | 'rows'

/** A dataset's first rows, as the server keeps them for a preview. */
function FirstRows({ vhost, name }: { vhost: string; name: string }) {
  const { data, isLoading, error } = useDataset(vhost, name)
  if (isLoading) return <Group justify="center" p="lg"><Loader size="sm" /></Group>
  if (error) {
    return <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The rows could not be read">{(error as Error).message}</Alert>
  }
  const cols = data?.columns ?? []
  return (
    <Stack gap="xs">
      <Text size="sm" c="dimmed">{data?.rows.toLocaleString()} rows; {cols.map((c) => `${c.name} (${c.type})`).join(', ')}</Text>
      {data && <DatasetPreview info={data} caption={`First rows of ${name}`} />}
    </Stack>
  )
}

/**
 * Checking a dataset (step 2 of getting started): what it holds in plain
 * words, what it is good for with a button to train that model, what to fix
 * and how, then each column with a chart, the number columns that move
 * together, and its first rows. The server computes it over the stored
 * dataset, on an even sample of a big one, which the summary says.
 */
export function AnalysisModal({ vhost, name, initialTab = 'overview', onTrain, canTrain = true, trainBlocked, onClose }: {
  vhost: string
  name: string
  initialTab?: AnalysisTab
  /** Starts training what the data suits; absent, no Train buttons are offered. */
  onTrain?: (start: TrainStart) => void
  canTrain?: boolean
  trainBlocked?: string
  onClose: () => void
}) {
  const { data: profile, isLoading, error } = useDatasetProfile(vhost, name)
  useEffect(() => {
    if (profile) markAnalyzed(vhost)
  }, [profile, vhost])
  const waiting = (
    <Group gap="xs" p="lg" justify="center">
      <Loader size="sm" />
      <Text size="sm" c="dimmed">Reading {name}… On a big dataset this takes a few seconds.</Text>
    </Group>
  )
  const failed = error && (
    <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The dataset could not be analyzed">
      {(error as Error).message} Its first rows are under First rows.
    </Alert>
  )
  const needsProfile = (body: ReactNode) => (isLoading ? waiting : failed || body)
  return (
    <Modal opened onClose={onClose} title={`Check ${name}`} centered size="xl">
      <Tabs defaultValue={initialTab} keepMounted={false}>
        <Tabs.List mb="md">
          <Tabs.Tab value="overview">Overview</Tabs.Tab>
          <Tabs.Tab value="columns">Columns</Tabs.Tab>
          <Tabs.Tab value="related">Related columns</Tabs.Tab>
          <Tabs.Tab value="rows">First rows</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="overview">
          {needsProfile(profile && (
            <Stack gap="lg">
              <SummaryCard profile={profile} />
              <GoodForList profile={profile} onTrain={onTrain} canTrain={canTrain} trainBlocked={trainBlocked} />
              <WarningsList warnings={profile.warnings} />
            </Stack>
          ))}
        </Tabs.Panel>
        <Tabs.Panel value="columns">
          {needsProfile(profile && (
            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              {profile.columns.map((c) => <ColumnCard key={c.name} column={c} />)}
            </SimpleGrid>
          ))}
        </Tabs.Panel>
        <Tabs.Panel value="related">{needsProfile(profile && <Correlations profile={profile} />)}</Tabs.Panel>
        <Tabs.Panel value="rows"><FirstRows vhost={vhost} name={name} /></Tabs.Panel>
      </Tabs>
    </Modal>
  )
}
