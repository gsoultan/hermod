import { Title, Text, SimpleGrid, Paper, Group, ThemeIcon, Box, Stack } from '@mantine/core'
import { IconDatabase, IconArrowsExchange, IconBroadcast, IconLayoutDashboard } from '@tabler/icons-react'

export function DashboardPage() {
  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <style>
        {`
          @keyframes fadeIn {
            from { opacity: 0; transform: translateY(10px); }
            to { opacity: 1; transform: translateY(0); }
          }
        `}
      </style>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconLayoutDashboard size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Dashboard</Title>
              <Text size="sm" c="dimmed">
                Overview of your Hermod instance. Monitor active data flows, message throughput, and system health at a glance.
              </Text>
            </Box>
          </Group>
        </Paper>
      
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="xl">
          <Paper p="xl" radius="md" withBorder style={{ boxShadow: 'var(--mantine-shadow-xs)' }}>
          <Group justify="space-between">
            <Text size="xs" c="dimmed" fw={700} tt="uppercase" lts="0.5px">
              Active Sources
            </Text>
            <ThemeIcon color="indigo" variant="light" size="lg" radius="md">
              <IconDatabase size="1.2rem" />
            </ThemeIcon>
          </Group>
          <Group align="flex-end" gap="xs" mt="md">
            <Text fw={800} size="32px" style={{ lineHeight: 1 }}>12</Text>
            <Text c="teal" size="sm" fw={600} bg="teal.0" px={8} py={2} style={{ borderRadius: '4px' }}>
              +2%
            </Text>
          </Group>
        </Paper>

        <Paper p="xl" radius="md" withBorder style={{ boxShadow: 'var(--mantine-shadow-xs)' }}>
          <Group justify="space-between">
            <Text size="xs" c="dimmed" fw={700} tt="uppercase" lts="0.5px">
              Messages Processed
            </Text>
            <ThemeIcon color="teal" variant="light" size="lg" radius="md">
              <IconArrowsExchange size="1.2rem" />
            </ThemeIcon>
          </Group>
          <Group align="flex-end" gap="xs" mt="md">
            <Text fw={800} size="32px" style={{ lineHeight: 1 }}>1.2M</Text>
            <Text c="teal" size="sm" fw={600} bg="teal.0" px={8} py={2} style={{ borderRadius: '4px' }}>
              +15%
            </Text>
          </Group>
        </Paper>

        <Paper p="xl" radius="md" withBorder style={{ boxShadow: 'var(--mantine-shadow-xs)' }}>
          <Group justify="space-between">
            <Text size="xs" c="dimmed" fw={700} tt="uppercase" lts="0.5px">
              Active Sinks
            </Text>
            <ThemeIcon color="orange" variant="light" size="lg" radius="md">
              <IconBroadcast size="1.2rem" />
            </ThemeIcon>
          </Group>
          <Group align="flex-end" gap="xs" mt="md">
            <Text fw={800} size="32px" style={{ lineHeight: 1 }}>5</Text>
            <Text c="dimmed" size="sm" fw={600} bg="gray.0" px={8} py={2} style={{ borderRadius: '4px' }}>
              0%
            </Text>
          </Group>
        </Paper>
      </SimpleGrid>
      
      <Paper p="xl" radius="md" bg="indigo.0" style={{ border: '1px solid var(--mantine-color-indigo-1)' }}>
        <Text c="indigo.9" fw={500}>
          Welcome to Hermod Management Platform. Use the sidebar to manage your data flows.
        </Text>
      </Paper>
      </Stack>
    </Box>
  )
}
