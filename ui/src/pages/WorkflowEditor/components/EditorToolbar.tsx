import { 
  ActionIcon, Button, Divider, Flex, Group, Paper, Select, Text, TextInput, Menu, Badge
} from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { 
  IconDeviceFloppy, IconPlayerPlay, IconPlayerPause, 
  IconEraser, IconZoomIn, IconZoomOut, IconFocus2, IconSettings,
  IconChevronDown, IconLayoutSidebarRight, IconRefresh, IconTimeline, IconDatabase,
  IconHistory, IconTerminal2, IconSparkles, IconShieldLock
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface EditorToolbarProps {
  id: string;
  isNew: boolean;
  onSave: () => void;
  onTest: (dryRun?: boolean) => void;
  onConfigureTest: () => void;
  onToggle: () => void;
  onRebuild: () => void;
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
  isNew, onSave, onTest, onConfigureTest, onToggle, onRebuild, onClearTest,
  isSaving, isTesting, isToggling,
  zoom, zoomIn, zoomOut, fitView,
  vhosts, workers
}: EditorToolbarProps) {
  const { 
    name, dryRun, vhost, workerID, testResults, active,
    setName, setVHost, setWorkerID, setDrawerOpened, setQuickAddSource, 
    setTraceInspectorOpened, setSchemaRegistryOpened, setHistoryOpened, setLiveStreamOpened,
    setAIGeneratorOpened, setComplianceReportOpened
  } = useWorkflowStore(useShallow(state => ({
    name: state.name,
    dryRun: state.dryRun,
    vhost: state.vhost,
    workerID: state.workerID,
    testResults: state.testResults,
    active: state.active,
    setName: state.setName,
    setVHost: state.setVHost,
    setWorkerID: state.setWorkerID,
    setDrawerOpened: state.setDrawerOpened,
    setQuickAddSource: state.setQuickAddSource,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setSchemaRegistryOpened: state.setSchemaRegistryOpened,
    setHistoryOpened: state.setHistoryOpened,
    setLiveStreamOpened: state.setLiveStreamOpened,
    setAIGeneratorOpened: state.setAIGeneratorOpened,
    setComplianceReportOpened: state.setComplianceReportOpened
  })));

  return (
    <Paper withBorder p="xs" radius="md" mb="md" shadow="xs">
      <Flex justify="space-between" align="center" gap="md">
        <Group gap="sm" style={{ flex: 1, maxWidth: 900 }}>
          <TextInput
            placeholder="Workflow Name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            style={{ flex: 1 }}
            size="sm"
          />
          {dryRun && (
            <Badge color="orange" variant="light" size="lg" radius="sm">Dry-Run Mode</Badge>
          )}
          <Select
            placeholder="VHost"
            data={(Array.isArray(vhosts) ? vhosts : []).map((vh: any) => ({ value: vh.name, label: vh.name }))}
            value={vhost}
            onChange={(val) => setVHost(val || 'default')}
            size="sm"
            style={{ width: 120 }}
          />
          <Select
            placeholder="Worker"
            data={(Array.isArray(workers) ? workers : []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
            value={workerID}
            onChange={(val) => setWorkerID(val || '')}
            size="sm"
            style={{ width: 120 }}
          />
        </Group>

        <Group gap="xs">
          <Group gap={4} mr="xs">
             <ActionIcon aria-label="Zoom out" variant="subtle" size="sm" onClick={zoomOut}><IconZoomOut size="1rem" /></ActionIcon>
             <Text size="xs" fw={700} w={35} ta="center">{Math.round(zoom * 100)}%</Text>
             <ActionIcon aria-label="Zoom in" variant="subtle" size="sm" onClick={zoomIn}><IconZoomIn size="1rem" /></ActionIcon>
             <ActionIcon aria-label="Fit to view" variant="subtle" size="sm" onClick={fitView}><IconFocus2 size="1rem" /></ActionIcon>
          </Group>

          <Divider orientation="vertical" />

          <Menu position="bottom-end" withArrow shadow="md">
            <Menu.Target>
              <Button 
                variant="light"
                color="indigo"
                size="sm"
                leftSection={<IconSettings size="1rem" />}
                rightSection={<IconChevronDown size="0.8rem" />}
              >
                Tools
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconDatabase size="1rem" />} onClick={() => setSchemaRegistryOpened(true)}>
                Schema Registry
              </Menu.Item>
              <Menu.Item leftSection={<IconSparkles size="1rem" />} onClick={() => setAIGeneratorOpened(true)}>
                AI Generator
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>

          {testResults ? (
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
                onClick={() => onTest(false)}
                loading={isTesting}
                style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
              >
                Test
              </Button>
              <Menu position="bottom-end" withArrow>
                <Menu.Target>
                  <ActionIcon 
                    aria-label="More test actions"
                    variant="light" 
                    color="blue" 
                    size={36} 
                    style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: '1px solid rgba(0,0,0,0.1)' }}
                  >
                    <IconChevronDown size="1rem" />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item leftSection={<IconPlayerPlay size="1rem" />} onClick={() => onTest(false)}>
                    Run Simulation (Auto)
                  </Menu.Item>
                  <Menu.Item leftSection={<IconPlayerPlay size="1rem" />} color="orange" onClick={() => onTest(true)}>
                    Dry-run (Full Execute)
                  </Menu.Item>
                  <Menu.Item leftSection={<IconSettings size="1rem" />} onClick={() => onConfigureTest()}>
                    Configure & Run...
                  </Menu.Item>
                  <Menu.Divider />
                  <Menu.Item leftSection={<IconRefresh size="1rem" />} color="blue" onClick={onRebuild}>
                    Rebuild Projections
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          )}

          {!isNew && (
            <Menu position="bottom-end" withArrow shadow="md">
              <Menu.Target>
                <Button 
                  variant="light"
                  color="blue"
                  size="sm"
                  leftSection={<IconTerminal2 size="1rem" />}
                  rightSection={<IconChevronDown size="0.8rem" />}
                >
                  Inspect
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item leftSection={<IconTerminal2 size="1rem" />} onClick={() => setLiveStreamOpened(true)}>
                  Live Stream
                </Menu.Item>
                <Menu.Item leftSection={<IconTimeline size="1rem" />} onClick={() => setTraceInspectorOpened(true)}>
                  Trace Analysis
                </Menu.Item>
                <Menu.Item leftSection={<IconShieldLock size="1rem" />} onClick={() => setComplianceReportOpened(true)}>
                  Compliance Report
                </Menu.Item>
                <Menu.Item leftSection={<IconHistory size="1rem" />} onClick={() => setHistoryOpened(true)}>
                  Version History
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          )}

          {!isNew && (
            <Button 
              color={active ? 'red' : 'green'} 
              variant="light"
              size="sm" 
              leftSection={active ? <IconPlayerPause size="1rem" /> : <IconPlayerPlay size="1rem" />} 
              onClick={onToggle}
              loading={isToggling}
            >
              {active ? 'Stop' : 'Start'}
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
            aria-label="Workflow panel"
            variant="filled" 
            color="blue"
            size="lg" 
            onClick={() => {
              setDrawerOpened(true);
              setQuickAddSource(null);
            }}
          >
            <IconLayoutSidebarRight size="1.2rem" />
          </ActionIcon>
        </Group>
      </Flex>
    </Paper>
  );
}
