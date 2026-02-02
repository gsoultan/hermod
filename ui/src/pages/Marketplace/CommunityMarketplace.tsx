import { useState } from 'react';
import { 
  Title, Text, SimpleGrid, Card, Group, Badge, Button, 
  TextInput, Stack, Box, ThemeIcon, Tabs, Paper, Tooltip, Divider, Loader, Center
} from '@mantine/core';
import { 
  IconSearch, IconPuzzle, IconCloudDownload, IconExternalLink, 
  IconShieldCheck, IconUser, IconStar, IconTrash, IconCheck
} from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';

interface Plugin {
    id: string;
    name: string;
    description: string;
    author: string;
    stars: number;
    category: string;
    certified: boolean;
    type: string;
    wasm_url?: string;
    installed: boolean;
    installed_at?: string;
}

export function CommunityMarketplace() {
  const [search, setSearch] = useState('');
  const [activeTab, setActiveTab] = useState<string | null>('all');
  const queryClient = useQueryClient();

  const { data: plugins, isLoading, error } = useQuery<Plugin[]>({
    queryKey: ['marketplace', 'plugins'],
    queryFn: async () => {
      const res = await fetch('/api/marketplace/plugins');
      if (!res.ok) throw new Error('Failed to fetch plugins');
      return res.json();
    }
  });

  const installMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await fetch('/api/marketplace/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id })
      });
      if (!res.ok) throw new Error('Failed to install plugin');
      return res.json();
    },
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: ['marketplace', 'plugins'] });
      notifications.show({
        title: 'Success',
        message: `Plugin ${id} installed successfully`,
        color: 'green'
      });
    },
    onError: (err) => {
      notifications.show({
        title: 'Error',
        message: err.message,
        color: 'red'
      });
    }
  });

  const uninstallMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await fetch('/api/marketplace/uninstall', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id })
      });
      if (!res.ok) throw new Error('Failed to uninstall plugin');
      return res.json();
    },
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: ['marketplace', 'plugins'] });
      notifications.show({
        title: 'Success',
        message: `Plugin ${id} uninstalled successfully`,
        color: 'blue'
      });
    },
    onError: (err) => {
      notifications.show({
        title: 'Error',
        message: err.message,
        color: 'red'
      });
    }
  });

  const filteredPlugins = plugins?.filter(plugin => {
    const matchesSearch = plugin.name.toLowerCase().includes(search.toLowerCase()) || 
                          plugin.description.toLowerCase().includes(search.toLowerCase());
    
    if (activeTab === 'wasm') return matchesSearch && plugin.type.toLowerCase() === 'wasm';
    if (activeTab === 'connectors') return matchesSearch && plugin.type.toLowerCase() === 'connector';
    return matchesSearch;
  });

  if (isLoading) {
    return (
      <Center h={400}>
        <Loader size="xl" />
      </Center>
    );
  }

  if (error) {
    return (
      <Center h={400}>
        <Text c="red">Error loading marketplace: {(error as Error).message}</Text>
      </Center>
    );
  }

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="indigo.0">
          <Group gap="sm">
            <IconPuzzle size="2rem" color="var(--mantine-color-indigo-6)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Community Marketplace</Title>
              <Text size="sm" c="dimmed">
                Extend Hermod with custom WASM transformers, sinks, and sources from the community.
              </Text>
            </Box>
            <Button variant="filled" color="indigo" leftSection={<IconExternalLink size="1rem" />}>
              Publish Plugin
            </Button>
          </Group>
        </Paper>

        <Group justify="space-between">
          <TextInput
            placeholder="Search plugins, transformers..."
            leftSection={<IconSearch size="1rem" />}
            style={{ width: 400 }}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
          <Tabs value={activeTab} onChange={setActiveTab}>
            <Tabs.List>
              <Tabs.Tab value="all">All</Tabs.Tab>
              <Tabs.Tab value="wasm">WASM</Tabs.Tab>
              <Tabs.Tab value="connectors">Connectors</Tabs.Tab>
            </Tabs.List>
          </Tabs>
        </Group>

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lg">
          {filteredPlugins?.map(plugin => (
            <Card key={plugin.id} withBorder padding="lg" radius="md" shadow="sm">
              <Card.Section withBorder inheritPadding py="xs">
                <Group justify="space-between">
                  <Text fw={700}>{plugin.name}</Text>
                  <Group gap={5}>
                    <IconStar size="0.8rem" color="orange" />
                    <Text size="xs" fw={700}>{plugin.stars}</Text>
                  </Group>
                </Group>
              </Card.Section>

              <Text size="sm" c="dimmed" mt="md" h={40}>
                {plugin.description}
              </Text>

              <Group mt="md" gap="xs">
                <Badge variant="light" color="indigo">{plugin.category}</Badge>
                <Badge variant="dot" color="blue">{plugin.type}</Badge>
                {plugin.certified && (
                  <Tooltip label="Certified Connector">
                    <ThemeIcon size="sm" color="green" variant="subtle">
                      <IconShieldCheck size="1rem" />
                    </ThemeIcon>
                  </Tooltip>
                )}
                {plugin.installed && (
                   <Badge color="green" variant="filled" leftSection={<IconCheck size="0.8rem" />}>Installed</Badge>
                )}
              </Group>

              <Card.Section inheritPadding mt="lg" pb="md">
                <Divider mb="sm" />
                <Group justify="space-between">
                  <Group gap={5}>
                    <IconUser size="0.8rem" color="var(--mantine-color-gray-6)" />
                    <Text size="xs" c="dimmed">{plugin.author}</Text>
                  </Group>
                  
                  {plugin.installed ? (
                    <Button 
                      variant="light" 
                      color="red" 
                      size="xs" 
                      leftSection={<IconTrash size="1rem" />}
                      loading={uninstallMutation.isPending && uninstallMutation.variables === plugin.id}
                      onClick={() => uninstallMutation.mutate(plugin.id)}
                    >
                      Uninstall
                    </Button>
                  ) : (
                    <Button 
                      variant="light" 
                      size="xs" 
                      leftSection={<IconCloudDownload size="1rem" />}
                      loading={installMutation.isPending && installMutation.variables === plugin.id}
                      onClick={() => installMutation.mutate(plugin.id)}
                    >
                      Install
                    </Button>
                  )}
                </Group>
              </Card.Section>
            </Card>
          ))}
        </SimpleGrid>
      </Stack>
    </Box>
  );
}
