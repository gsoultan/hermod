import {
  ActionIcon, Anchor, Badge, Button, Divider, Flex, Group, Menu, Text, TextInput, Tooltip
} from '@mantine/core';
import { Link } from '@tanstack/react-router';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import {
  IconActivity, IconCheck, IconChevronDown, IconChevronLeft, IconCopy, IconDatabase, IconDots, IconDownload, IconEraser,
  IconFocus2, IconHistory, IconLayoutDistributeHorizontal, IconPlayerPlay, IconRefresh, IconSearch, IconSettings,
  IconShieldLock, IconSparkles, IconTerminal2, IconTimeline, IconTrash, IconZoomIn, IconZoomOut
} from '@tabler/icons-react';
import { normalizeWorkflowStatus } from '@/utils/workflowStatus';

interface EditorToolbarProps {
  id: string;
  isNew: boolean;
  onSave: () => void;
  onTest: () => void;
  onConfigureTest: () => void;
  onToggle: () => void;
  onRebuild: () => void;
  onClearTest: () => void;
  onExport: () => void;
  onClone: () => void;
  onDelete: () => void;
  onAutoLayout?: () => void;
  onSearch?: () => void;
  isSaving: boolean;
  isTesting: boolean;
  isToggling: boolean;
  zoom: number;
  zoomIn: () => void;
  zoomOut: () => void;
  fitView: () => void;
  /** Kept for the page that passes them; the virtual host and worker are chosen in Flow settings now. */
  vhosts?: any[];
  workers?: any[];
  /** Live workflow status, shown inline so the editor needs only one header bar. */
  workflowStatus?: string;
}

/**
 * The editor's one header: back, the name and its state on the left; Test ▾,
 * Turn on, Save and ⋯ on the right. Everything the old Tools and Inspect
 * menus held is behind ⋯, and the virtual host and worker moved into Flow
 * settings, so nothing competes with the canvas.
 */
export function EditorToolbar({
  id, isNew, onSave, onTest, onConfigureTest, onToggle, onRebuild, onClearTest,
  onExport, onClone, onDelete, onAutoLayout, onSearch,
  isSaving, isTesting, isToggling,
  zoom, zoomIn, zoomOut, fitView,
  workflowStatus,
}: EditorToolbarProps) {
  const {
    name, dryRun, testResults, active,
    setName, setDryRun, setDrawerOpened, setDrawerTab, setQuickAddSource,
    setTraceInspectorOpened, setSchemaRegistryOpened, setHistoryOpened, setLiveStreamOpened,
    setAIGeneratorOpened, setComplianceReportOpened, pulseEnabled, setPulseEnabled
  } = useWorkflowStore(useShallow(state => ({
    name: state.name,
    dryRun: state.dryRun,
    testResults: state.testResults,
    active: state.active,
    setName: state.setName,
    setDryRun: state.setDryRun,
    setDrawerOpened: state.setDrawerOpened,
    setDrawerTab: state.setDrawerTab,
    setQuickAddSource: state.setQuickAddSource,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setSchemaRegistryOpened: state.setSchemaRegistryOpened,
    setHistoryOpened: state.setHistoryOpened,
    setLiveStreamOpened: state.setLiveStreamOpened,
    setAIGeneratorOpened: state.setAIGeneratorOpened,
    setComplianceReportOpened: state.setComplianceReportOpened,
    pulseEnabled: state.pulseEnabled,
    setPulseEnabled: state.setPulseEnabled
  })));

  const openSettings = () => {
    setQuickAddSource(null);
    setDrawerTab('settings');
    setDrawerOpened(true);
  };

  return (
    <Flex justify="space-between" align="center" gap="sm" wrap="wrap" mb="sm">
      <Group gap="sm" wrap="wrap" preventGrowOverflow={false} style={{ flex: '1 1 260px', maxWidth: 720, minWidth: 0 }}>
        {isNew ? (
          <Anchor component={Link} to="/workflows" size="sm" c="dimmed" style={{ display: 'inline-flex', alignItems: 'center', gap: 2, whiteSpace: 'nowrap' }}>
            <IconChevronLeft size="0.9rem" aria-hidden /> Back to flows
          </Anchor>
        ) : (
          <Anchor component={Link} to="/workflows/$id" params={{ id } as any} size="sm" c="dimmed" style={{ display: 'inline-flex', alignItems: 'center', gap: 2, whiteSpace: 'nowrap' }}>
            <IconChevronLeft size="0.9rem" aria-hidden /> Back to the flow
          </Anchor>
        )}
        <TextInput
          aria-label="Flow name"
          placeholder="Flow name"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          style={{ flex: '1 1 140px', minWidth: 0 }}
          size="sm"
          fw={600}
        />
        {!isNew && workflowStatus && (
          <Badge color={normalizeWorkflowStatus(workflowStatus).color} variant="light" size="lg" radius="sm">
            {normalizeWorkflowStatus(workflowStatus).label}
          </Badge>
        )}
        {dryRun && <Badge color="orange" variant="light" size="lg" radius="sm">Dry run</Badge>}
      </Group>

      <Group gap="xs" wrap="nowrap">
        <Group gap={2} visibleFrom="sm">
          <Tooltip label="Search steps to add (⌘⇧K)" withArrow>
            <ActionIcon aria-label="Search steps" variant="subtle" color="gray" size="md" onClick={onSearch}><IconSearch size="1rem" /></ActionIcon>
          </Tooltip>
          <ActionIcon aria-label="Zoom out" variant="subtle" color="gray" size="md" onClick={zoomOut}><IconZoomOut size="1rem" /></ActionIcon>
          <Text size="xs" fw={600} c="dimmed" w={36} ta="center">{Math.round(zoom * 100)}%</Text>
          <ActionIcon aria-label="Zoom in" variant="subtle" color="gray" size="md" onClick={zoomIn}><IconZoomIn size="1rem" /></ActionIcon>
          <Tooltip label="Fit to view" withArrow>
            <ActionIcon aria-label="Fit to view" variant="subtle" color="gray" size="md" onClick={fitView}><IconFocus2 size="1rem" /></ActionIcon>
          </Tooltip>
          <Tooltip label="Tidy up the layout" withArrow>
            <ActionIcon aria-label="Tidy up" variant="subtle" color="gray" size="md" onClick={() => onAutoLayout?.()}><IconLayoutDistributeHorizontal size="1rem" /></ActionIcon>
          </Tooltip>
          <Divider orientation="vertical" mx={4} />
        </Group>

        <Group gap={0} wrap="nowrap">
          <Button
            variant="light"
            size="sm"
            leftSection={<IconPlayerPlay size="1rem" />}
            onClick={() => onTest()}
            loading={isTesting}
            style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
          >
            Test
          </Button>
          <Menu position="bottom-end" withArrow shadow="md">
            <Menu.Target>
              <ActionIcon
                aria-label="More test actions"
                variant="light"
                size={36}
                style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: '1px solid var(--mantine-color-default-border)' }}
              >
                <IconChevronDown size="1rem" />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconPlayerPlay size="1rem" />} onClick={() => onTest()}>Run simulation</Menu.Item>
              <Menu.Item leftSection={<IconSettings size="1rem" />} onClick={() => onConfigureTest()}>Configure &amp; run…</Menu.Item>
              <Menu.Item leftSection={<IconRefresh size="1rem" />} onClick={onRebuild}>Rebuild projections</Menu.Item>
              <Menu.Divider />
              {/* The real pipeline without writing to a sink. It is a saved setting of the flow, so it is also in Flow settings. */}
              <Menu.Item
                leftSection={<IconActivity size="1rem" />}
                rightSection={dryRun ? <IconCheck size="1rem" aria-hidden /> : undefined}
                onClick={() => setDryRun(!dryRun)}
              >
                Dry-run mode
              </Menu.Item>
              {testResults && (
                <>
                  <Menu.Divider />
                  <Menu.Item color="orange" leftSection={<IconEraser size="1rem" />} onClick={onClearTest}>Clear simulation</Menu.Item>
                </>
              )}
            </Menu.Dropdown>
          </Menu>
        </Group>

        {!isNew && (
          <Button
            color={active ? 'orange' : 'green'}
            variant="light"
            size="sm"
            onClick={onToggle}
            loading={isToggling}
          >
            {active ? 'Turn off' : 'Turn on'}
          </Button>
        )}

        <Button onClick={onSave} loading={isSaving} size="sm">Save</Button>

        <Menu position="bottom-end" withArrow shadow="md">
          <Menu.Target>
            <ActionIcon aria-label="More" variant="default" size={36}><IconDots size="1rem" /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconDatabase size="1rem" />} onClick={() => setSchemaRegistryOpened(true)}>Schema registry</Menu.Item>
            <Menu.Item leftSection={<IconSparkles size="1rem" />} onClick={() => setAIGeneratorOpened(true)}>AI generator</Menu.Item>
            <Menu.Item
              leftSection={<IconActivity size="1rem" />}
              rightSection={<Text size="xs" c="dimmed">{pulseEnabled ? 'On' : 'Off'}</Text>}
              onClick={() => setPulseEnabled(!pulseEnabled)}
            >
              Data pulse
            </Menu.Item>
            {!isNew && (
              <>
                <Menu.Divider />
                <Menu.Item leftSection={<IconTerminal2 size="1rem" />} onClick={() => setLiveStreamOpened(true)}>Live stream</Menu.Item>
                <Menu.Item leftSection={<IconTimeline size="1rem" />} onClick={() => setTraceInspectorOpened(true)}>Trace analysis</Menu.Item>
                <Menu.Item leftSection={<IconShieldLock size="1rem" />} onClick={() => setComplianceReportOpened(true)}>Compliance report</Menu.Item>
                <Menu.Item leftSection={<IconHistory size="1rem" />} onClick={() => setHistoryOpened(true)}>Version history</Menu.Item>
              </>
            )}
            <Menu.Divider />
            <Menu.Item leftSection={<IconSettings size="1rem" />} onClick={openSettings}>Flow settings…</Menu.Item>
            {!isNew && (
              <>
                <Menu.Divider />
                <Menu.Item leftSection={<IconDownload size="1rem" />} onClick={onExport}>Export JSON</Menu.Item>
                <Menu.Item leftSection={<IconCopy size="1rem" />} onClick={onClone}>Clone</Menu.Item>
                <Menu.Item color="red" leftSection={<IconTrash size="1rem" />} onClick={onDelete}>Delete flow</Menu.Item>
              </>
            )}
          </Menu.Dropdown>
        </Menu>
      </Group>
    </Flex>
  );
}
