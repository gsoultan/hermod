import { ActionIcon, Badge, Box, Button, Menu, Text, TextInput } from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import {
  IconActivity, IconCheck, IconChevronDown, IconCopy, IconDatabase, IconDots, IconDownload, IconEraser,
  IconHistory, IconPlayerPlay, IconRefresh, IconSettings,
  IconShieldLock, IconSparkles, IconTerminal2, IconTimeline, IconTrash
} from '@tabler/icons-react';
import { StatePill } from '@/components/rows';
import { flowState } from '@/components/flows/FlowRow';
import { FlowHeader } from '@/components/flows/FlowHeader';


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
  isSaving: boolean;
  isTesting: boolean;
  isToggling: boolean;
  /** Kept for the page that passes them; the virtual host and worker are chosen in Flow settings now. */
  vhosts?: any[];
  workers?: any[];
  /** Live workflow status, shown inline so the editor needs only one header bar. */
  workflowStatus?: string;
}

/**
 * The editor's one header, the same frame as the read-only view's: back, the
 * name and its state on the left; Test ▾, Turn on, Save and ⋯ on the right.
 * Zoom, Fit and Tidy up live on the canvas itself (CanvasControls). Everything the old Tools and Inspect
 * menus held is behind ⋯, and the virtual host and worker moved into Flow
 * settings, so nothing competes with the canvas.
 */
export function EditorToolbar({
  id, isNew, onSave, onTest, onConfigureTest, onToggle, onRebuild, onClearTest,
  onExport, onClone, onDelete,
  isSaving, isTesting, isToggling,
  workflowStatus,
}: EditorToolbarProps) {
  const {
    name, vhost, dryRun, testResults, active,
    setName, setDryRun, setDrawerOpened, setDrawerTab, setQuickAddSource,
    setTraceInspectorOpened, setSchemaRegistryOpened, setHistoryOpened, setLiveStreamOpened,
    setAIGeneratorOpened, setComplianceReportOpened, pulseEnabled, setPulseEnabled
  } = useWorkflowStore(useShallow(state => ({
    name: state.name,
    vhost: state.vhost,
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

  const backTo = isNew
    ? { to: '/workflows', label: 'Back to flows' }
    : { to: '/workflows/$id', params: { id }, label: 'Back to the flow' };
  // The same words as the read-only view and the flows list: Off, Stopped, Running.
  const state = isNew ? null : flowState({ active, status: workflowStatus ?? '' });

  const actions = (
    <>
      <Button.Group aria-label="Test">
        <Button
          variant="default"
          leftSection={<IconPlayerPlay size="1rem" />}
          onClick={() => onTest()}
          loading={isTesting}
        >
          Test
        </Button>
        <Menu position="bottom-end" withArrow shadow="md">
          <Menu.Target>
            <Button aria-label="More test actions" variant="default" px={10}>
              <IconChevronDown size="1rem" />
            </Button>
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
      </Button.Group>
      {!isNew && (
        <Button
          variant="default"
          leftSection={<Box component="i" aria-hidden className="hermod-dot" data-tone={active ? 'warn' : 'ok'} />}
          onClick={onToggle}
          loading={isToggling}
        >
          {active ? 'Turn off' : 'Turn on'}
        </Button>
      )}
      <Button onClick={onSave} loading={isSaving}>Save</Button>
    </>
  );

  return (
    <FlowHeader
      back={backTo}
      title={(
        <TextInput
          aria-label="Flow name"
          placeholder="Untitled flow"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          variant="unstyled"
          classNames={{ input: 'hermod-flow-name' }}
        />
      )}
      meta={(
        <>
          {state ? <StatePill tone={state.tone} fz="inherit">{state.label}</StatePill> : <span>Draft</span>}
          <span aria-hidden>·</span>
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>Virtual host {vhost || 'default'}</span>
          {dryRun && <Badge color="orange" variant="light" size="sm" radius="sm" style={{ flex: 'none' }}>Dry run</Badge>}
        </>
      )}
      actions={actions}
      menu={(
        <Menu position="bottom-end" withArrow shadow="md">
          <Menu.Target>
            <ActionIcon aria-label="More" variant="subtle" color="gray" size={36} radius="md"><IconDots size="1.1rem" /></ActionIcon>
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
      )}
    />
  );
}
