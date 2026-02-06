import { IconCheck, IconCopy, IconPlus, IconSearch } from '@tabler/icons-react';
import { useState } from 'react'
import { ActionIcon, Box, Group, ScrollArea, Stack, Text, TextInput, Tooltip as MantineTooltip } from '@mantine/core'import { getValByPath } from '../../utils/transformationUtils'

interface FieldExplorerProps {
  availableFields: string[]
  incomingPayload?: any
  onAdd: (path: string) => void
}

export function FieldExplorer({ availableFields = [], incomingPayload, onAdd }: FieldExplorerProps) {
  const [search, setSearch] = useState('')
  const [copiedField, setCopiedField] = useState<string | null>(null)
  const filtered = (availableFields || []).filter((f) => f.toLowerCase().includes(search.toLowerCase()))

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedField(text)
    setTimeout(() => setCopiedField(null), 2000)
  }

  return (
    <Stack gap="xs">
      <TextInput
        placeholder="Search fields..."
        size="xs"
        leftSection={<IconSearch size="0.8rem" />}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <ScrollArea h={300} type="auto">
        <Stack gap={4}>
          {filtered.map((field) => (
            <Group
              key={field}
              justify="space-between"
              wrap="nowrap"
              p={4}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData('text/plain', `source.${field}`)
                e.dataTransfer.effectAllowed = 'copy'
              }}
              style={{
                borderRadius: 4,
                background: 'var(--mantine-color-blue-0)',
                border: '1px dashed var(--mantine-color-blue-2)',
                cursor: 'grab',
              }}
            >
              <Box style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                <Text size="xs" fw={500} truncate>
                  {field}
                </Text>
                <Text size="10px" c="dimmed" truncate>
                  {JSON.stringify(getValByPath(incomingPayload, field))}
                </Text>
              </Box>
              <Group gap={4}>
                <MantineTooltip label="Copy path">
                  <ActionIcon aria-label="Copy field path" size="xs" variant="subtle" onClick={() => copyToClipboard(field)}>
                    {copiedField === field ? <IconCheck size="0.8rem" color="green" /> : <IconCopy size="0.8rem" />}
                  </ActionIcon>
                </MantineTooltip>
                <MantineTooltip label="Add to config">
                  <ActionIcon
                    aria-label="Add field to config"
                    size="xs"
                    variant="subtle"
                    color="blue"
                    onClick={() => onAdd(field)}
                  >
                    <IconPlus size="0.8rem" />
                  </ActionIcon>
                </MantineTooltip>
              </Group>
            </Group>
          ))}
        </Stack>
      </ScrollArea>
    </Stack>
  )
}


