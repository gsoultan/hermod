import { useRef, useState } from 'react'
import { ActionIcon, Alert, Badge, Box, Button, Grid, Group, Modal, Paper, ScrollArea, Stack, Text, ThemeIcon, Tooltip } from '@mantine/core'
import { IconAlertCircle, IconCloudUpload, IconFile, IconFileSpreadsheet, IconFileTypeCsv, IconUpload, IconX } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { datasetsKey, uploadDataset, type DatasetInfo } from '@/lib/mlModels'
import { LIMITS } from '@/lib/mlDataGuide'
import { FILE_ACCEPT, fileFormat, type FileFormat } from '@/lib/mlDataProfile'
import { datasetNameFromFile, formatBytes, parseCsvPreview, type CsvPreview } from '@/lib/mlFilePreview'
import { FileHelp } from '../dataNeeds'
import { useDatasetName } from './datasetForms'
import { DIALOG_BREAKPOINTS, GuidePanel } from './QueryModal'
import { RowsPreview } from './RowsPreview'

/** How much of a CSV is read for its preview: the header and first rows fit with room to spare. */
const PREVIEW_BYTES = 64 * 1024

const FORMAT: Record<FileFormat, { label: string; icon: React.ReactNode; color: string }> = {
  csv: { label: 'CSV', icon: <IconFileTypeCsv size="1.4rem" />, color: 'teal' },
  xlsx: { label: 'Excel', icon: <IconFileSpreadsheet size="1.4rem" />, color: 'green' },
  parquet: { label: 'Parquet', icon: <IconFile size="1.4rem" />, color: 'violet' },
}

const DELIMITER_NAME: Record<string, string> = { ',': 'commas', ';': 'semicolons', '\t': 'tabs', '|': 'pipes' }

/**
 * Uploads a CSV, Excel or Parquet file as a dataset; `initialName` fills in
 * its name. A file is dropped or picked, named after itself unless a name was
 * typed, and a CSV shows its first rows before anything is sent.
 * `onUploaded` takes over once the file is in (to check the data next);
 * without it the dialog closes.
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
  const [preview, setPreview] = useState<CsvPreview | null>(null)
  const [dragging, setDragging] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  // The file a preview is being read for, so a slow read of an earlier
  // file does not land on a later one.
  const reading = useRef<File | null>(null)
  const format = file ? fileFormat(file.name) : null
  const unreadable = !!file && !format
  const tooBig = !!file && file.size > LIMITS.uploadMaxBytes
  const upload = useMutation({
    mutationFn: () => uploadDataset(vhost, ds.name, file as File),
    onSuccess: (info) => {
      queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) })
      if (onUploaded) onUploaded(info)
      else onClose()
    },
  })

  const choose = async (next: File | null) => {
    setFile(next)
    setPreview(null)
    upload.reset()
    reading.current = next
    if (!next) {
      if (inputRef.current) inputRef.current.value = ''
      return
    }
    const name = datasetNameFromFile(next.name)
    if (name) ds.suggest(name)
    if (fileFormat(next.name) !== 'csv') return
    try {
      const text = await next.slice(0, PREVIEW_BYTES).text()
      if (reading.current === next) setPreview(parseCsvPreview(text, { truncated: next.size > PREVIEW_BYTES }))
    } catch {
      // No preview; the server still reads the file.
    }
  }
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && file && !unreadable && !tooBig) upload.mutate()
  }
  const onDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragging(false)
    void choose(e.dataTransfer.files?.[0] ?? null)
  }

  const dropZone = (
    <Paper component="label" htmlFor="ml-dataset-file" withBorder radius="md" p="xl"
      onDragOver={(e: React.DragEvent) => { e.preventDefault(); setDragging(true) }}
      onDragLeave={() => setDragging(false)} onDrop={onDrop}
      style={{
        cursor: 'pointer',
        borderStyle: 'dashed',
        borderWidth: 2,
        borderColor: dragging ? 'var(--mantine-primary-color-filled)' : undefined,
        background: dragging ? 'var(--mantine-primary-color-light)' : 'var(--mantine-color-default-hover)',
        transition: 'background 120ms ease, border-color 120ms ease',
      }}>
      <Stack align="center" gap={6}>
        <ThemeIcon size={52} radius="xl" variant="light" aria-hidden><IconCloudUpload size="1.8rem" /></ThemeIcon>
        <Text fw={600}>{dragging ? 'Drop the file to use it' : 'Drop a file here, or click to choose one'}</Text>
        <Text size="xs" c="dimmed" ta="center" id="ml-dataset-file-help">
          CSV, Excel .xlsx (the first sheet, with a header row) or Parquet (plain columns, no lists or nested
          groups). At most {LIMITS.uploadMaxBytes / 2 ** 20} MB.
        </Text>
      </Stack>
    </Paper>
  )

  const chosen = file && (
    <Paper withBorder radius="md" p="sm">
      <Group justify="space-between" wrap="nowrap" gap="sm">
        <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
          <ThemeIcon size={40} radius="md" variant="light" color={format ? FORMAT[format].color : 'red'} aria-hidden>
            {format ? FORMAT[format].icon : <IconFile size="1.4rem" />}
          </ThemeIcon>
          <Box style={{ minWidth: 0 }}>
            <Text size="sm" fw={600} truncate="end">{file.name}</Text>
            <Group gap={6}>
              <Text size="xs" c="dimmed">{formatBytes(file.size)}</Text>
              {format && <Badge size="xs" variant="light" color={FORMAT[format].color}>{FORMAT[format].label}</Badge>}
            </Group>
          </Box>
        </Group>
        <Group gap={4} wrap="nowrap">
          <Button size="xs" variant="subtle" onClick={() => inputRef.current?.click()}>Change</Button>
          <Tooltip label="Remove the file">
            <ActionIcon variant="subtle" color="gray" aria-label="Remove the file" onClick={() => void choose(null)}>
              <IconX size="1rem" />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
    </Paper>
  )

  return (
    <Modal opened onClose={onClose} title="Upload a dataset" centered size="64rem" scrollAreaComponent={ScrollArea.Autosize}>
      <form onSubmit={submit} noValidate>
        <Stack gap="lg">
          <Grid type="container" breakpoints={DIALOG_BREAKPOINTS} gap="lg">
            <Grid.Col span={{ base: 12, md: 7 }}>
              <Stack gap="md">
                <input ref={inputRef} id="ml-dataset-file" type="file" aria-label="File" accept={FILE_ACCEPT}
                  aria-describedby="ml-dataset-file-help" aria-invalid={unreadable || tooBig || undefined}
                  onChange={(e) => void choose(e.currentTarget.files?.[0] ?? null)}
                  style={{ position: 'absolute', width: 1, height: 1, opacity: 0, overflow: 'hidden' }} />
                {chosen || dropZone}
                {unreadable && (
                  <Text size="xs" c="red" role="alert">
                    {file?.name} is not a file Hermod reads. Save it as .csv, .xlsx or .parquet first.
                  </Text>
                )}
                {tooBig && (
                  <Text size="xs" c="red" role="alert">
                    {file?.name} is {formatBytes(file!.size)}; a file is at most {LIMITS.uploadMaxBytes / 2 ** 20} MB. Upload part of it,
                    or read the rows from a database instead.
                  </Text>
                )}
                {ds.input}
                {preview && preview.columns.length > 0 && (
                  <Stack gap={6}>
                    <Group justify="space-between" gap="xs">
                      <Text size="sm" fw={600}>First rows</Text>
                      <Text size="xs" c="dimmed">
                        {preview.columns.length} columns, separated by {DELIMITER_NAME[preview.delimiter] ?? preview.delimiter}
                      </Text>
                    </Group>
                    <RowsPreview columns={preview.columns} rows={preview.rows} caption={`First rows of ${file?.name}`} maxHeight={240} />
                  </Stack>
                )}
                {file && format && format !== 'csv' && (
                  <Text size="xs" c="dimmed">
                    {FORMAT[format].label} files show their rows after the upload, under Analyze.
                  </Text>
                )}
              </Stack>
            </Grid.Col>
            <Grid.Col span={{ base: 12, md: 5 }}>
              <GuidePanel title="What to put in the file">
                <FileHelp />
              </GuidePanel>
            </Grid.Col>
          </Grid>
          {upload.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The file was not uploaded">{(upload.error as Error).message}</Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button type="submit" leftSection={<IconUpload size="1rem" />} loading={upload.isPending}
              disabled={!file || unreadable || tooBig}>Upload</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
