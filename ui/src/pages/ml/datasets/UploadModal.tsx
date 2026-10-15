import { useState } from 'react'
import { Alert, Button, Group, Modal, Stack, Text } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { datasetsKey, uploadDataset, type DatasetInfo } from '@/lib/mlModels'
import { LIMITS } from '@/lib/mlDataGuide'
import { FILE_ACCEPT, fileFormat } from '@/lib/mlDataProfile'
import { FileHelp } from '../dataNeeds'
import { useDatasetName } from './datasetForms'

/**
 * Uploads a CSV, Excel or Parquet file as a dataset; `initialName` fills in
 * its name. `onUploaded` takes over once the file is in (to check the data
 * next); without it the dialog closes.
 */
export function UploadModal({ vhost, initialName, onClose, onUploaded }: {
  vhost: string
  initialName?: string
  onClose: () => void
  onUploaded?: (info: DatasetInfo) => void
}) {
  const queryClient = useQueryClient()
  const ds = useDatasetName(initialName)
  const [file, setFile] = useState<File | null>(null)
  const unreadable = !!file && !fileFormat(file.name)
  const upload = useMutation({
    mutationFn: () => uploadDataset(vhost, ds.name, file as File),
    onSuccess: (info) => {
      queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) })
      if (onUploaded) onUploaded(info)
      else onClose()
    },
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && file && !unreadable) upload.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Upload a dataset" centered>
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {ds.input}
          <Stack gap={4}>
            <Text size="sm" fw={500} component="label" htmlFor="ml-dataset-file">File</Text>
            <input id="ml-dataset-file" type="file" aria-label="File" accept={FILE_ACCEPT} aria-describedby="ml-dataset-file-help"
              aria-invalid={unreadable || undefined} onChange={(e) => setFile(e.currentTarget.files?.[0] ?? null)} />
            <Text size="xs" c="dimmed" id="ml-dataset-file-help">
              CSV, Excel .xlsx (the first sheet, with a header row) or Parquet (plain columns, no lists or nested
              groups). At most {LIMITS.uploadMaxBytes / 2 ** 20} MB.
            </Text>
            {unreadable && (
              <Text size="xs" c="red" role="alert">
                {file?.name} is not a file Hermod reads. Save it as .csv, .xlsx or .parquet first.
              </Text>
            )}
          </Stack>
          <FileHelp />
          {upload.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The file was not uploaded">{(upload.error as Error).message}</Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={upload.isPending} disabled={!file || unreadable}>Upload</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
