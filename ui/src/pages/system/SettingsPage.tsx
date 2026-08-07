import { Box, Button, Group, Modal, NumberInput, SimpleGrid, Stack, Tabs, Text, TextInput, Textarea, Title } from '@mantine/core';
import { IconActivity, IconCode, IconHistory, IconServer, IconShieldLock, IconWorld } from '@tabler/icons-react';
import { useSettingsController } from './settings/useSettingsController';
import { PlatformTab } from './settings/PlatformTab';
import { ConnectivityTab } from './settings/ConnectivityTab';
import { SecurityTab } from './settings/SecurityTab';
import { ObservabilityTab } from './settings/ObservabilityTab';
import { GovernanceTab } from './settings/GovernanceTab';
import { DeveloperTab } from './settings/DeveloperTab';

/**
 * Settings shell.
 *
 * Was a single 1,294-line component with 41 useState hooks, 11 mutations and
 * six tabs inline. State now lives in useSettingsController and each tab is its
 * own component, so a change to one tab no longer means reading all six.
 */
export function SettingsPage() {
  const ctx = useSettingsController();
  const { closeWSModal, createWSMutation, maxCPU, maxMemory, maxThroughput, maxWorkflows, newWSDesc, newWSName, setMaxCPU, setMaxMemory, setMaxThroughput, setMaxWorkflows, setNewWSDesc, setNewWSName, wsModalOpened } = ctx;

  return (
    <Box pb="xl">
      <Stack gap="xs" mb="xl">
        <Title order={2}>Platform Settings</Title>
        <Text c="dimmed" size="sm">Configure and manage your Hermod instance</Text>
      </Stack>

      <Tabs defaultValue="platform" orientation="vertical" variant="pills" styles={{
        root: { display: 'flex', gap: '2rem' },
        list: { width: 220, flexShrink: 0 },
        panel: { flex: 1, minWidth: 0 }
      }}>
        <Tabs.List>
          <Tabs.Tab value="platform" leftSection={<IconServer size="1.1rem" />}>Platform</Tabs.Tab>
          <Tabs.Tab value="connectivity" leftSection={<IconWorld size="1.1rem" />}>Connectivity</Tabs.Tab>
          <Tabs.Tab value="security" leftSection={<IconShieldLock size="1.1rem" />}>Security</Tabs.Tab>
          <Tabs.Tab value="observability" leftSection={<IconActivity size="1.1rem" />}>Observability</Tabs.Tab>
          <Tabs.Tab value="governance" leftSection={<IconHistory size="1.1rem" />}>Governance</Tabs.Tab>
          <Tabs.Tab value="developer" leftSection={<IconCode size="1.1rem" />}>Developer</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="platform">
          <PlatformTab ctx={ctx} />
        </Tabs.Panel>
        <Tabs.Panel value="connectivity">
          <ConnectivityTab ctx={ctx} />
        </Tabs.Panel>
        <Tabs.Panel value="security">
          <SecurityTab ctx={ctx} />
        </Tabs.Panel>
        <Tabs.Panel value="observability">
          <ObservabilityTab ctx={ctx} />
        </Tabs.Panel>
        <Tabs.Panel value="governance">
          <GovernanceTab ctx={ctx} />
        </Tabs.Panel>
        <Tabs.Panel value="developer">
          <DeveloperTab ctx={ctx} />
        </Tabs.Panel>

      </Tabs>

      <Modal opened={wsModalOpened} onClose={closeWSModal} title="Create New Workspace">
        <Stack>
          <TextInput
            label="Workspace Name"
            placeholder="e.g. Production, Marketing"
            required
            value={newWSName}
            onChange={(e) => setNewWSName(e.currentTarget.value)}
          />
          <Textarea
            label="Description"
            placeholder="What is this workspace for?"
            value={newWSDesc}
            onChange={(e) => setNewWSDesc(e.currentTarget.value)}
          />
          <SimpleGrid cols={2}>
            <NumberInput
              label="Max Workflows"
              description="0 for unlimited"
              min={0}
              value={maxWorkflows}
              onChange={(val) => setMaxWorkflows(Number(val))}
            />
            <NumberInput
              label="Max Throughput (msg/s)"
              description="0 for unlimited"
              min={0}
              value={maxThroughput}
              onChange={(val) => setMaxThroughput(Number(val))}
            />
            <NumberInput
              label="Max CPU (Cores)"
              description="0 for unlimited"
              min={0}
              step={0.1}
              decimalScale={1}
              value={maxCPU}
              onChange={(val) => setMaxCPU(Number(val))}
            />
            <NumberInput
              label="Max Memory (MB)"
              description="0 for unlimited"
              min={0}
              value={maxMemory}
              onChange={(val) => setMaxMemory(Number(val))}
            />
          </SimpleGrid>
          <Group justify="flex-end" mt="md">
            <Button variant="outline" color="gray" onClick={closeWSModal}>Cancel</Button>
            <Button onClick={() => createWSMutation.mutate()} loading={createWSMutation.isPending}>Create Workspace</Button>
          </Group>
        </Stack>
      </Modal>
    </Box>
  )
}



