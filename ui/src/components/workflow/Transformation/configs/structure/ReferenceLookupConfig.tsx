import { useState } from 'react'
import { Alert, Autocomplete, Button, FileButton, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconAlertCircle, IconUpload } from '@tabler/icons-react'
import { apiFetch } from '@/api'

interface ReferenceLookupConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

/**
 * reference_lookup (pkg/comm/transformer/lookup/reference_lookup.go): enrich
 * from a CSV, TSV or Excel file the worker holds in memory, re-read when the
 * file changes. Upload goes through POST /api/files/upload, the same endpoint
 * and storage as every other uploaded file; its answer is the stored path.
 */
export function ReferenceLookupConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: ReferenceLookupConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState('')
  const isExcel = (config.format || String(config.filePath ?? '').toLowerCase().split('.').pop()) === 'xlsx'

  const upload = async (file: File | null) => {
    if (!file) return
    setUploading(true)
    setUploadError('')
    const form = new FormData()
    form.append('file', file)
    try {
      const res = await apiFetch('/api/files/upload', { method: 'POST', body: form, silent: true })
      const data = res.ok ? await res.json() : null
      if (data?.path) {
        set({ filePath: data.path })
      } else {
        setUploadError('The server did not accept the file.')
      }
    } catch (e: any) {
      setUploadError(e?.message || 'The upload failed.')
    } finally {
      setUploading(false)
    }
  }

  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed">
        Adds columns from a reference file to each record, matched on a key. The file is read once and kept in
        memory, and read again when it changes.
      </Text>
      <Group align="flex-end" wrap="nowrap">
        <TextInput
          label="File path"
          placeholder="/var/hermod/uploads/countries.csv"
          description="A .csv, .tsv or .xlsx file in the upload directory or a directory listed in HERMOD_REFERENCE_DIRS"
          value={config.filePath ?? ''}
          onChange={(e) => set({ filePath: e.currentTarget.value })}
          style={{ flex: 1 }}
          required
        />
        <FileButton onChange={upload} accept=".csv,.tsv,.xlsx">
          {(props) => (
            <Button {...props} variant="light" leftSection={<IconUpload size="1rem" />} loading={uploading}>
              Upload
            </Button>
          )}
        </FileButton>
      </Group>
      {uploadError && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />}>
          {uploadError}
        </Alert>
      )}
      <Group grow align="flex-start">
        <Select
          label="Format"
          placeholder="From the extension"
          clearable
          data={[
            { value: 'csv', label: 'CSV' },
            { value: 'tsv', label: 'TSV' },
            { value: 'xlsx', label: 'Excel (.xlsx)' },
          ]}
          value={config.format || null}
          onChange={(v) => set({ format: v ?? '' })}
        />
        {isExcel ? (
          <TextInput
            label="Sheet"
            placeholder="First sheet"
            value={config.sheet ?? ''}
            onChange={(e) => set({ sheet: e.currentTarget.value })}
          />
        ) : (
          <TextInput
            label="Delimiter"
            placeholder=","
            description='"tab" for a tab'
            value={config.delimiter ?? ''}
            onChange={(e) => set({ delimiter: e.currentTarget.value })}
          />
        )}
      </Group>
      <Group grow align="flex-start">
        <TextInput
          label="Key column"
          placeholder="e.g. code"
          description="The file's column to match on, as its first row names it"
          value={config.keyColumn ?? ''}
          onChange={(e) => set({ keyColumn: e.currentTarget.value })}
          required
        />
        <Autocomplete
          label="Key field"
          placeholder="e.g. country_code"
          description="The record's field holding the key"
          data={fieldPaths}
          value={config.keyField ?? ''}
          onChange={(v) => set({ keyField: v })}
          required
        />
      </Group>
      <TextInput
        label="Columns"
        placeholder="Every column but the key"
        description="Which columns to copy, comma-separated"
        value={config.columns ?? ''}
        onChange={(e) => set({ columns: e.currentTarget.value })}
      />
      <TextInput
        label="Target field"
        placeholder="Onto the record"
        description="Optional: put the columns under this field as one object"
        value={config.targetField ?? ''}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
      />
      <Group grow align="flex-start">
        <Select
          label="When no row matches"
          data={[
            { value: 'passthrough', label: 'Pass the record on' },
            { value: 'default', label: 'Write a default value' },
            { value: 'fail', label: 'Fail the record' },
          ]}
          value={config.onMiss || 'passthrough'}
          onChange={(v) => set({ onMiss: v ?? 'passthrough' })}
        />
        {config.onMiss === 'default' && (
          <TextInput
            label="Default value"
            description="Written to the target field"
            value={config.defaultValue ?? ''}
            onChange={(e) => set({ defaultValue: e.currentTarget.value })}
          />
        )}
      </Group>
      <Text size="xs" c="dimmed">
        Values are copied as text. Files are limited to 32 MiB and 200,000 rows unless the node's maxBytes and maxRows
        say otherwise.
      </Text>
    </Stack>
  )
}
