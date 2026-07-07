import { IconCheck, IconCopy, IconPlus, IconSearch } from '@tabler/icons-react';
import { useState, useMemo } from 'react'
import { ActionIcon, Box, Group, ScrollArea, Stack, Text, TextInput, Tooltip as MantineTooltip, Badge } from '@mantine/core'
import { getValByPath } from '../../../utils/transformationUtils'

interface FieldExplorerProps {
  availableFields: any[]
  incomingPayload?: any
  onAdd: (path: string) => void
}

export function FieldExplorer({ availableFields = [], incomingPayload, onAdd }: FieldExplorerProps) {
  const [search, setSearch] = useState('')
  const [copiedField, setCopiedField] = useState<string | null>(null)
  
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (availableFields || []).filter((f) => {
      const path = typeof f === 'string' ? f : f.path;
      return !q || path.toLowerCase().includes(q);
    });
  }, [availableFields, search]);

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
          {filtered.map((f) => {
            const path = typeof f === 'string' ? f : f.path;
            const type = typeof f === 'string' ? undefined : f.type;
            
            return (
              <Group
                key={path}
                justify="space-between"
                wrap="nowrap"
                p={4}
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData('text/plain', `source.${path}`)
                  e.dataTransfer.effectAllowed = 'copy'
                }}
                style={{
                  borderRadius: 4,
                  background: 'var(--mantine-color-blue-light)',
                  border: '1px dashed var(--mantine-color-blue-light-color)',
                  cursor: 'grab',
                }}
              >
                <Box style={{ overflow: 'hidden', textOverflow: 'ellipsis', flex: 1 }}>
                  <Group gap={4} wrap="nowrap">
                    <Text size="xs" fw={500} truncate>
                      {path}
                    </Text>
                    {type && (
                      <Badge variant="outline" size="xs" color="blue" radius="xs">
                        {type}
                      </Badge>
                    )}
                  </Group>
                  <Text size="10px" c="dimmed" truncate>
                    {JSON.stringify(getValByPath(incomingPayload, path))}
                  </Text>
                </Box>
                <Group gap={4}>
                  <MantineTooltip label="Copy path">
                    <ActionIcon aria-label="Copy field path" size="xs" variant="subtle" onClick={() => copyToClipboard(path)}>
                      {copiedField === path ? <IconCheck size="0.8rem" color="green" /> : <IconCopy size="0.8rem" />}
                    </ActionIcon>
                  </MantineTooltip>
                  <MantineTooltip label="Add to config">
                    <ActionIcon
                      aria-label="Add field to config"
                      size="xs"
                      variant="subtle"
                      color="blue"
                      onClick={() => onAdd(path)}
                    >
                      <IconPlus size="0.8rem" />
                    </ActionIcon>
                  </MantineTooltip>
                </Group>
              </Group>
            );
          })}
        </Stack>
      </ScrollArea>
    </Stack>
  )
}


