import { useState } from 'react'
import { ActionIcon, Alert, Box, Group, ScrollArea, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle, IconSearch, IconTrash } from '@tabler/icons-react'

interface TargetExplorerProps {
  fields: string[]
  sinkSchemaPresent: boolean
  currentMappings: Record<string, string>
  onMap: (column: string, sourcePath: string) => void
  onClearMap: (column: string) => void
  tableName?: string
  loading?: boolean
}

export function TargetExplorer({
  fields,
  sinkSchemaPresent,
  currentMappings,
  onMap,
  onClearMap,
  tableName,
  loading,
}: TargetExplorerProps) {
  const [search, setSearch] = useState('')
  const filtered = fields.filter((f) => f.toLowerCase().includes(search.toLowerCase()))

  if (!sinkSchemaPresent) {
    return (
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light" py="xs">
        Connect this node to a database sink to see target schema.
      </Alert>
    )
  }

  const onDropToTarget = (e: React.DragEvent<HTMLDivElement>, column: string) => {
    e.preventDefault()
    e.currentTarget.style.background = 'var(--mantine-color-green-0)'
    const data = e.dataTransfer.getData('text/plain')
    if (data) onMap(column, data)
  }

  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Text size="xs" fw={700} c="dimmed">
          {(tableName || 'TARGET TABLE').toUpperCase()}
        </Text>
        {loading ? <ActionIcon loading variant="transparent" /> : null}
      </Group>
      <TextInput
        placeholder="Filter target columns..."
        size="xs"
        leftSection={<IconSearch size="0.8rem" />}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <ScrollArea h={300} type="auto">
        <Stack gap={4}>
          {filtered.map((column) => (
            <Box
              key={column}
              p={6}
              onDragOver={(e) => {
                e.preventDefault()
                e.currentTarget.style.background = 'var(--mantine-color-green-1)'
              }}
              onDragLeave={(e) => {
                e.currentTarget.style.background = 'var(--mantine-color-green-0)'
              }}
              onDrop={(e) => onDropToTarget(e, column)}
              style={{
                borderRadius: 4,
                background: 'var(--mantine-color-green-0)',
                border: '1px dashed var(--mantine-color-green-3)',
                cursor: 'default',
              }}
            >
              <Group justify="space-between" wrap="nowrap">
                <Box style={{ overflow: 'hidden' }}>
                  <Text size="xs" fw={700} c="green.9" truncate>
                    {column}
                  </Text>
                  <Text size="10px" c="dimmed" truncate>
                    {currentMappings[`column.${column}`] || 'Not mapped'}
                  </Text>
                </Box>
                {currentMappings[`column.${column}`] && (
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    color="red"
                    onClick={() => onClearMap(column)}
                    aria-label={`Clear mapping for ${column}`}
                  >
                    <IconTrash size="0.8rem" />
                  </ActionIcon>
                )}
              </Group>
            </Box>
          ))}
          {filtered.length === 0 && !loading && (
            <Text size="xs" c="dimmed" ta="center" py="xl">
              No columns found. Ensure the table exists.
            </Text>
          )}
        </Stack>
      </ScrollArea>
    </Stack>
  )
}
