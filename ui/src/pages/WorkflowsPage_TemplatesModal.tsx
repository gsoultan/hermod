import { IconAlertCircle, IconCloud, IconDatabase, IconGitBranch, IconHistory, IconSearch, IconSend, IconTimeline } from '@tabler/icons-react';
import { Button, Card, SimpleGrid, Stack, Text, ThemeIcon, Group, Loader, Alert, Box } from '@mantine/core'

import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../api'const ICON_MAP: Record<string, any> = {
  IconGitBranch,
  IconSend,
  IconHistory,
  IconTimeline,
  IconDatabase,
  IconCloud,
  IconSearch
}

type TemplateDef = {
  name: string
  description: string
  icon: string | any
  color: string
  data: any
}

export default function TemplatesModal({ onUseTemplate }: { onUseTemplate: (data: any) => void }) {
  const { data: templates, isLoading, error } = useQuery({
    queryKey: ['templates'],
    queryFn: async () => {
      const res = await apiFetch('/api/templates')
      if (!res.ok) throw new Error('Failed to load templates')
      return res.json()
    }
  })

  if (isLoading) return <Group justify="center" p="xl"><Loader size="md" /></Group>
  if (error) return <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{String(error)}</Alert>

  return (
    <Stack>
      <Text size="sm" c="dimmed">
        Choose a pre-built template to jumpstart your workflow development.
      </Text>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        {(templates as TemplateDef[])?.map((template, index) => {
          const Icon = typeof template.icon === 'string' ? (ICON_MAP[template.icon] || IconGitBranch) : (template.icon || IconGitBranch)
          return (
            <Card key={index} withBorder shadow="sm" radius="md" padding="lg">
              <Stack gap="sm" h="100%">
                <Group justify="space-between">
                  <ThemeIcon size="lg" radius="md" variant="light" color={template.color || 'blue'}>
                    <Icon size="1.2rem" />
                  </ThemeIcon>
                  <Button
                    size="xs"
                    variant="light"
                    color={template.color || 'blue'}
                    onClick={() => onUseTemplate(template.data)}
                  >
                    Use Template
                  </Button>
                </Group>

                <Box style={{ flex: 1 }}>
                  <Text fw={700} size="sm" mb={4}>
                    {template.name}
                  </Text>
                  <Text size="xs" c="dimmed" lineClamp={3}>
                    {template.description}
                  </Text>
                </Box>
              </Stack>
            </Card>
          )
        })}
      </SimpleGrid>
    </Stack>
  )
}


