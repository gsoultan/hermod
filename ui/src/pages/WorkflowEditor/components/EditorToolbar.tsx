import { 
  ActionIcon, Badge, Button, Divider, Flex, Group, Paper, Select, Text, TextInput, Title, Menu
} from '@mantine/core';
import { 
  IconDeviceFloppy, IconPlayerPlay, IconPlayerPause, 
  IconEraser, IconZoomIn, IconZoomOut, IconFocus2, IconSettings,
  IconChevronDown
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface EditorToolbarProps {
  id: string;
  isNew: boolean;
  onSave: () => void;
  onTest: () => void;
  onConfigureTest: () => void;
  onToggle: () => void;
  onClearTest: () => void;
  isSaving: boolean;
  isTesting: boolean;
  isToggling: boolean;
  zoom: number;
  zoomIn: () => void;
  zoomOut: () => void;
  fitView: () => void;
  vhosts: any[];
  workers: any[];
}

export function EditorToolbar({
  isNew, onSave, onTest, onConfigureTest, onToggle, onClearTest,
  isSaving, isTesting, isToggling,
  zoom, zoomIn, zoomOut, fitView,
  vhosts, workers
}: EditorToolbarProps) {
  const store = useWorkflowStore();

  return (
    <Paper withBorder p="xs" radius="md" mb="md" shadow="xs">
      <Flex justify="space-between" align="center" gap="md">
        <Group gap="xs">
          <Title order={4} style={{ whiteSpace: 'nowrap' }}>
            {isNew ? 'New Workflow' : store.name}
          </Title>
          {!isNew && (
            <Badge 
              color={store.active ? 'green' : 'gray'} 
              variant="filled"
              leftSection={store.active ? <IconPlayerPlay size="0.6rem" /> : <IconPlayerPause size="0.6rem" />}
            >
              {store.workflowStatus}
            </Badge>
          )}
        </Group>

        <Group gap="sm" style={{ flex: 1, maxWidth: 800 }}>
          <TextInput
            placeholder="Workflow Name"
            value={store.name}
            onChange={(e) => store.setName(e.currentTarget.value)}
            style={{ flex: 1 }}
            size="sm"
          />
          <Select
            placeholder="VHost"
            data={vhosts.map((vh: any) => ({ value: vh.name, label: vh.name }))}
            value={store.vhost}
            onChange={(val) => store.setVHost(val || 'default')}
            size="sm"
            style={{ width: 120 }}
          />
          <Select
            placeholder="Worker"
            data={workers.map((w: any) => ({ value: w.id, label: w.name || w.id }))}
            value={store.workerID}
            onChange={(val) => store.setWorkerID(val || '')}
            size="sm"
            style={{ width: 120 }}
          />
        </Group>

        <Group gap="xs">
          <Group gap={4} mr="xs">
             <ActionIcon variant="subtle" size="sm" onClick={zoomOut}><IconZoomOut size="1rem" /></ActionIcon>
             <Text size="xs" fw={700} w={35} ta="center">{Math.round(zoom * 100)}%</Text>
             <ActionIcon variant="subtle" size="sm" onClick={zoomIn}><IconZoomIn size="1rem" /></ActionIcon>
             <ActionIcon variant="subtle" size="sm" onClick={fitView}><IconFocus2 size="1rem" /></ActionIcon>
          </Group>

          <Divider orientation="vertical" />

          {store.testResults ? (
            <Button 
              variant="light" 
              color="orange" 
              size="sm" 
              leftSection={<IconEraser size="1rem" />} 
              onClick={onClearTest}
            >
              Clear Simulation
            </Button>
          ) : (
            <Group gap={0}>
              <Button 
                variant="light" 
                color="blue" 
                size="sm" 
                leftSection={<IconPlayerPlay size="1rem" />} 
                onClick={() => onTest()}
                loading={isTesting}
                style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
              >
                Test
              </Button>
              <Menu position="bottom-end" withArrow>
                <Menu.Target>
                  <ActionIcon 
                    variant="light" 
                    color="blue" 
                    size={36} 
                    style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: '1px solid rgba(0,0,0,0.1)' }}
                  >
                    <IconChevronDown size="1rem" />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item leftSection={<IconPlayerPlay size="1rem" />} onClick={() => onTest()}>
                    Run Simulation (Auto)
                  </Menu.Item>
                  <Menu.Item leftSection={<IconSettings size="1rem" />} onClick={() => onConfigureTest()}>
                    Configure & Run...
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          )}

          {!isNew && (
            <Button 
              color={store.active ? 'red' : 'green'} 
              variant="light"
              size="sm" 
              leftSection={store.active ? <IconPlayerPause size="1rem" /> : <IconPlayerPlay size="1rem" />} 
              onClick={onToggle}
              loading={isToggling}
            >
              {store.active ? 'Stop' : 'Start'}
            </Button>
          )}

          <Button 
            leftSection={<IconDeviceFloppy size="1rem" />} 
            onClick={onSave}
            loading={isSaving}
            size="sm"
          >
            Save
          </Button>

          <ActionIcon 
            variant="light" 
            size="lg" 
            onClick={() => {
              store.setDrawerOpened(true);
              store.setQuickAddSource(null);
            }}
          >
            <IconSettings size="1.2rem" />
          </ActionIcon>
        </Group>
      </Flex>
    </Paper>
  );
}
