import { Alert, Badge, Button, Group, Loader, Modal, Table, Text } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listVersions, mlModelsKey, promoteVersion, versionsKey, type MLModel } from '@/lib/mlModels'
import { formatMetric } from './TrainModal'

/**
 * A trained model's versions, newest first, with how each scored. Any one can
 * be put live: a newer one that was held back, or an older one to roll back.
 */
export function VersionsModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const queryClient = useQueryClient()
  const { data: versions, isLoading, error } = useQuery({
    queryKey: versionsKey(vhost, model.name),
    queryFn: ({ signal }) => listVersions(vhost, model.name, signal),
    retry: false,
  })
  const promote = useMutation({
    mutationFn: (version: string) => promoteVersion(vhost, model.name, version),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) }),
  })
  const live = promote.data?.remote_version ?? model.remote_version

  return (
    <Modal opened onClose={onClose} title={`Versions of ${model.name}`} centered size="xl">
      {isLoading ? (
        <Group justify="center" p="lg"><Loader size="sm" /></Group>
      ) : error ? (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The versions could not be loaded">{(error as Error).message}</Alert>
      ) : !versions?.length ? (
        <Text size="sm" c="dimmed">No versions yet. Train the model to make one.</Text>
      ) : (
        <Table.ScrollContainer minWidth={640}>
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Version</Table.Th>
                <Table.Th>Trained</Table.Th>
                <Table.Th>Dataset</Table.Th>
                <Table.Th>Score</Table.Th>
                <Table.Th>Rows</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {versions.map((v) => (
                <Table.Tr key={v.version}>
                  <Table.Td ff="var(--mantine-font-family-monospace)">{v.version}</Table.Td>
                  <Table.Td><Text size="sm">{new Date(v.created_at).toLocaleString()}</Text></Table.Td>
                  <Table.Td><Text size="sm">{v.dataset} → {v.target}</Text></Table.Td>
                  <Table.Td ff="var(--mantine-font-family-monospace)">{v.metrics.score !== undefined ? formatMetric(v.metrics.score) : '—'}</Table.Td>
                  <Table.Td><Text size="sm">{v.rows.train + v.rows.test}</Text></Table.Td>
                  <Table.Td style={{ textAlign: 'right' }}>
                    {live === v.version ? (
                      <Badge color="green" variant="light">Live</Badge>
                    ) : (
                      <Button size="xs" variant="light" aria-label={`Put version ${v.version} live`}
                        loading={promote.isPending && promote.variables === v.version}
                        onClick={() => promote.mutate(v.version)}>
                        Put live
                      </Button>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {promote.error && (
        <Alert mt="md" color="red" icon={<IconAlertCircle size="1rem" />} title="The version was not put live">
          {(promote.error as Error).message}
        </Alert>
      )}
    </Modal>
  )
}
