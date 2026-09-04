import { useState, useMemo } from 'react';
import { 
  Tabs, Stack, Group, Paper, Text, ScrollArea, Box, ThemeIcon, UnstyledButton, rem, Title, useMantineColorScheme,
  Select, Checkbox, NumberInput, TextInput, Alert, TagsInput, Divider, ActionIcon, SimpleGrid
} from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '../../../../api';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
// import { UnitTestForm } from '../../../../components/forms/UnitTestForm';
// Node configuration forms are presented in a modal (popup) from the editor page
import { CronInput } from '../../../../components/shared/CronInput';
import { AICopilot } from '../../../../components/shared/AICopilot';
import { NODE_CATEGORIES } from '../constants/nodeCategories';
import { filterCategories, matchesQuery, countMatches } from '../utils/paletteSearch';
import { 
  IconDatabase, IconTable, IconX, IconPlus,
  IconCloudUpload, IconRobot, IconPuzzle, IconSettingsAutomation, IconAdjustments, IconShieldLock,
  IconInfoCircle, IconRefresh, IconFilter, IconTags, IconSearch
} from '@tabler/icons-react';
interface SidebarDrawerProps {
  onDragStart: (event: any, nodeType: string, refId: string, label: string, subType: string, extraData?: any) => void;
  onAddItem: (type: string, refId: string, label: string, subType: string, Icon: any, color: string, extraData?: any) => void;
  sources: any[];
  sinks: any[];
}

export function SidebarDrawer({ 
  onDragStart, onAddItem, sources, sinks
}: SidebarDrawerProps) {
  const { 
    drawerOpened, drawerTab, deadLetterSinkID, dlqThreshold, prioritizeDLQ, dryRun, 
    maxRetries, retryInterval, reconnectInterval, workspaceID, schemaType, schema, tags,
    cpuRequest, memoryRequest, throughputRequest,
    cron,
    setDrawerOpened, setDrawerTab, setDeadLetterSinkID, setDlqThreshold, setPrioritizeDLQ, 
    setDryRun, setMaxRetries, setRetryInterval, setReconnectInterval, 
    setWorkspaceID, setSchemaType, setSchema, setTags, 
    setCPURequest, setMemoryRequest, setThroughputRequest, setCron,
    nodes
  } = useWorkflowStore(useShallow(state => ({
    drawerOpened: state.drawerOpened,
    drawerTab: state.drawerTab,
    deadLetterSinkID: state.deadLetterSinkID,
    dlqThreshold: state.dlqThreshold,
    prioritizeDLQ: state.prioritizeDLQ,
    dryRun: state.dryRun,
    maxRetries: state.maxRetries,
    retryInterval: state.retryInterval,
    reconnectInterval: state.reconnectInterval,
    workspaceID: state.workspaceID,
    schemaType: state.schemaType,
    schema: state.schema,
    tags: state.tags,
    cpuRequest: state.cpuRequest,
    memoryRequest: state.memoryRequest,
    throughputRequest: state.throughputRequest,
    cron: state.cron,
    nodes: state.nodes,
    setDrawerOpened: state.setDrawerOpened,
    setDrawerTab: state.setDrawerTab,
    setDeadLetterSinkID: state.setDeadLetterSinkID,
    setDlqThreshold: state.setDlqThreshold,
    setPrioritizeDLQ: state.setPrioritizeDLQ,
    setDryRun: state.setDryRun,
    setMaxRetries: state.setMaxRetries,
    setRetryInterval: state.setRetryInterval,
    setReconnectInterval: state.setReconnectInterval,
    setWorkspaceID: state.setWorkspaceID,
    setSchemaType: state.setSchemaType,
    setSchema: state.setSchema,
    setTags: state.setTags,
    setCPURequest: state.setCPURequest,
    setMemoryRequest: state.setMemoryRequest,
    setThroughputRequest: state.setThroughputRequest,
    setCron: state.setCron,
    
  })));

  const nodeCategories = NODE_CATEGORIES;

  // Palette search. Applies to the three tabs that list things to drag onto the
  // canvas; the AI and Settings tabs have nothing to filter.
  const [paletteSearch, setPaletteSearch] = useState('');
  const isPaletteTab = (tab: string) => tab === 'sources' || tab === 'sinks' || tab === 'transformations';
  const searchActive = paletteSearch.trim().length > 0;


  // Saved sources and sinks are searched by the same rules as the built-in
  // catalogue: a connection named "billing-prod" should be findable by name,
  // and by the kind of thing it is.
  const filterExisting = (rows: any[], kind: string) =>
    (Array.isArray(rows) ? rows : []).filter((r) =>
      matchesQuery({ label: r.name, subType: r.type, type: kind }, paletteSearch)
    );

  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const { data: workspacesResponse } = useQuery<any>({
    queryKey: ['workspaces'],
    queryFn: async () => {
      const res = await apiFetch('/api/workspaces');
      return res.json();
    }
  });
  const workspaces = workspacesResponse || [];

  const { data: plugins } = useQuery<any[]>({
    queryKey: ['marketplace', 'plugins', 'installed'],
    queryFn: async () => {
      const res = await apiFetch('/api/marketplace/plugins');
      const allPlugins = await res.json();
      return allPlugins.filter((p: any) => p.installed);
    }
  });

  // Counted across the whole tab, not just the built-in catalogue: a query
  // matching only a saved connection must not report the tab as empty.
  const matchCounts = useMemo(() => {
    const base = countMatches(NODE_CATEGORIES, paletteSearch);
    const matchingPlugins = (Array.isArray(plugins) ? plugins : []).filter((pl: any) =>
      matchesQuery({ label: pl.name, description: pl.description, subType: pl.type }, paletteSearch)
    ).length;
    const named = (rows: any[], kind: string) =>
      (Array.isArray(rows) ? rows : []).filter((r) =>
        matchesQuery({ label: r.name, subType: r.type, type: kind }, paletteSearch)
      ).length;
    return {
      transformations: base.transformations + matchingPlugins,
      sources: base.sources + named(sources, 'source'),
      sinks: base.sinks + named(sinks, 'sink'),
    };
  }, [paletteSearch, plugins, sources, sinks]);

  const TAB_LABELS: Record<string, string> = {
    sources: 'Sources', transformations: 'Transformations', sinks: 'Sinks',
  };

  // A tabbed palette hides matches by design. Showing nothing when the thing
  // exists one tab over is the failure this avoids.
  const renderNoMatches = (tab: 'sources' | 'sinks' | 'transformations') => {
    // Mantine keeps every panel mounted, so without this the message renders
    // three times over -- invisible in the inactive tabs, but really in the
    // document, where a screen reader and a test both find it.
    if (coercedTab !== tab) return null;
    if (!searchActive || matchCounts[tab] > 0) return null;
    const elsewhere = (['sources', 'transformations', 'sinks'] as const).filter(
      (t) => t !== tab && matchCounts[t] > 0
    );
    return (
      <Stack gap="xs" align="center" py="xl" px="md">
        <Text size="sm" c="dimmed" ta="center">
          Nothing here matches &ldquo;{paletteSearch}&rdquo;.
        </Text>
        {elsewhere.map((t) => {
          // Transformations and Sinks stay locked until the workflow has a
          // source, and switching to a locked tab silently bounces back here.
          // Offering it as a link would be a button that does nothing; saying
          // why is the answer the user actually needs.
          const locked = t !== 'sources' && !hasSource;
          return locked ? (
            <Text key={t} size="xs" c="dimmed" ta="center">
              {matchCounts[t]} in {TAB_LABELS[t]} &mdash; add a source first
            </Text>
          ) : (
            <UnstyledButton key={t} onClick={() => setDrawerTab(t)}>
              <Text size="xs" c="blue.5" fw={600}>
                {matchCounts[t]} in {TAB_LABELS[t]} &rarr;
              </Text>
            </UnstyledButton>
          );
        })}
      </Stack>
    );
  };

  const selectedDLQSink = (sinks || []).find(s => s.id === deadLetterSinkID);
  const dlqSupportsRecovery = selectedDLQSink && ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'mongodb', 'cassandra', 'sqlite', 'clickhouse', 'yugabyte', 'kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'pubsub', 'kinesis', 'pulsar', 'elasticsearch', 'discord', 'slack', 'twitter', 'facebook', 'instagram', 'linkedin', 'tiktok'].includes(selectedDLQSink.type);

  const renderDraggableItem = (item: any) => (
    <UnstyledButton
      key={item.label + (item.refId || '') + (item.subType || '')}
      draggable
      onDragStart={(e) => {
        const extraData = item.pluginID ? { pluginID: item.pluginID } : undefined;
        onDragStart(e, item.type, item.refId, item.label, item.subType, extraData);
      }}
      onClick={() => {
        const extraData = item.pluginID ? { pluginID: item.pluginID } : undefined;
        onAddItem(item.type, item.refId, item.label, item.subType, item.icon, item.color, extraData);
      }}
      style={(theme) => ({
        display: 'block',
        width: '100%',
        padding: '8px 12px',
        borderRadius: theme.radius.md,
        color: isDark ? theme.colors.dark[0] : theme.black,
        transition: 'background-color 0.2s ease, transform 0.1s ease',
        '&:hover': {
          backgroundColor: isDark ? theme.colors.dark[6] : theme.colors.gray[0],
          transform: 'translateX(4px)',
        },
        '&:active': {
          transform: 'translateX(2px)',
        }
      })}
    >
      <Group wrap="nowrap" gap="sm">
        <ThemeIcon variant="light" color={item.color} size="lg" radius="md">
          <item.icon style={{ width: rem(20), height: rem(20) }} />
        </ThemeIcon>
        <Box style={{ flex: 1, overflow: 'hidden' }}>
          <Text size="sm" fw={600} truncate="end">{item.label}</Text>
          <Text size="xs" color="dimmed" truncate="end">
            {item.description || item.subType || item.type}
          </Text>
        </Box>
        <IconPlus size="1.1rem" color="var(--mantine-color-gray-4)" style={{ opacity: 0.6 }} />
      </Group>
    </UnstyledButton>
  );

  if (!drawerOpened) return null;

  const hasSource = Array.isArray(nodes) && nodes.some((n: any) => n.type === 'source');
  const coercedTab = (() => {
    // Map legacy 'nodes' tab to new 'transformations' tab
    let t = drawerTab === 'nodes' ? 'transformations' : drawerTab;
    // The old "Config" tab has been removed; redirect to "sources"
    if (t === 'config') t = 'sources';
    if (!hasSource && (t === 'transformations' || t === 'sinks')) return 'sources';
    return t;
  })();

  return (
    <Paper
      withBorder
      shadow="md"
      style={{
        // Floats over the canvas rather than sitting beside it. As an inline
        // flex sibling this panel permanently cost the canvas 400px of width —
        // on a 1440px screen that is nearly a third of the workspace, even
        // while the user is just looking at the graph. n8n overlays its palette
        // for the same reason.
        position: 'absolute',
        top: 0,
        right: 0,
        bottom: 0,
        zIndex: 5,
        width: 400,
        maxWidth: '90%',
        display: 'flex',
        flexDirection: 'column',
        borderRadius: 0,
        borderTop: 'none',
        borderBottom: 'none',
        borderRight: 'none',
      }}
    >
      <Stack p="md" gap="sm" style={{ flex: 1, overflow: 'hidden' }}>
        <Group justify="space-between">
          <Group gap="xs">
            <ThemeIcon variant="light" color="blue" size="md">
              <IconAdjustments size="1.2rem" />
            </ThemeIcon>
            <Title order={4}>Workflow Panel</Title>
          </Group>
          <ActionIcon aria-label="Close" variant="subtle" color="gray" onClick={() => setDrawerOpened(false)}>
            <IconX size="1.2rem" />
          </ActionIcon>
        </Group>

        <Tabs value={coercedTab} onChange={(val) => setDrawerTab(val || "sources")} variant="pills" radius="md" style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          {/* Five tabs do not fit the panel's width. Scrolling them sideways
              hid "AI" and "Settings" past the edge with nothing to suggest they
              existed; wrapping costs one row and keeps every tab discoverable. */}
          <Tabs.List mb="sm" grow={false} style={{ flexWrap: 'wrap', rowGap: 4, paddingBottom: 4 }}>
            <Tabs.Tab value="sources" leftSection={<IconDatabase size="1rem" />} px="xs">Sources</Tabs.Tab>
            <Tabs.Tab value="transformations" leftSection={<IconPlus size="1rem" />} px="xs" disabled={!hasSource}>Transformations</Tabs.Tab>
            <Tabs.Tab value="sinks" leftSection={<IconCloudUpload size="1rem" />} px="xs" disabled={!hasSource}>Sinks</Tabs.Tab>
            <Tabs.Tab value="copilot" leftSection={<IconRobot size="1rem" />} px="xs">AI</Tabs.Tab>
            {/* Config tab removed by request */}
            <Tabs.Tab value="settings" leftSection={<IconSettingsAutomation size="1rem" />} px="xs">Settings</Tabs.Tab>
          </Tabs.List>

          {/* One box for whichever palette tab is open. It is not shown on AI
              or Settings, which list nothing to filter. */}
          {isPaletteTab(coercedTab) && (
            <TextInput
              mb="sm"
              size="xs"
              placeholder="Search sources, transformations and sinks..."
              aria-label="Search the node palette"
              leftSection={<IconSearch size="0.9rem" />}
              value={paletteSearch}
              onChange={(e) => setPaletteSearch(e.currentTarget.value)}
              rightSection={
                searchActive ? (
                  <ActionIcon
                    variant="subtle"
                    color="gray"
                    size="sm"
                    aria-label="Clear search"
                    onClick={() => setPaletteSearch('')}
                  >
                    <IconX size="0.8rem" />
                  </ActionIcon>
                ) : null
              }
            />
          )}

          <Box style={{ flex: 1, overflow: 'hidden' }}>
            <Tabs.Panel value="transformations" h="100%">
              <ScrollArea h="100%" offsetScrollbars type="always" px="xs">
                <Stack gap="lg" py="xs">
                  {renderNoMatches('transformations')}
                  {plugins && plugins.length > 0 && (Array.isArray(plugins) ? plugins : [])
                    .filter(pl => matchesQuery({ label: pl.name, description: pl.description, subType: pl.type }, paletteSearch))
                    .length > 0 && (
                    <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.8' : 'indigo.0'}>
                      <Group gap="xs" px="xs" mb="xs">
                        <IconPuzzle size="1rem" color="var(--mantine-color-indigo-6)" />
                        <Text size="xs" fw={800} c="indigo.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Installed Plugins</Text>
                      </Group>
                      <Stack gap={2}>
                        {(Array.isArray(plugins) ? plugins : [])
                          .filter(pl => matchesQuery({ label: pl.name, description: pl.description, subType: pl.type }, paletteSearch))
                          .map(plugin => renderDraggableItem({
                          type: plugin.type.toLowerCase() === 'connector' ? 'sink' : 'transformation',
                          refId: 'new',
                          label: plugin.name,
                          subType: plugin.type.toLowerCase() === 'wasm' || plugin.type.toLowerCase() === 'transformer' ? 'wasm' : plugin.type.toLowerCase(),
                          icon: IconPuzzle,
                          color: 'indigo',
                          description: plugin.description,
                          pluginID: plugin.id
                        }))}
                      </Stack>
                    </Paper>
                  )}

                  {filterCategories(nodeCategories, paletteSearch).map((cat) => (
                    <Paper key={cat.title} withBorder p="xs" radius="md" bg="var(--mantine-color-body)">
                      <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{cat.title}</Text>
                      <Stack gap={2}>
                        {cat.items.map(renderDraggableItem)}
                      </Stack>
                    </Paper>
                  ))}
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            <Tabs.Panel value="sources" h="100%">
              <ScrollArea h="100%" offsetScrollbars type="always" px="xs">
                <Stack gap="lg" py="xs">
                  {renderNoMatches('sources')}
                  {filterCategories(NODE_CATEGORIES.filter(cat => cat.group === 'sources'), paletteSearch).map((cat) => {
                    const FirstIcon = cat.items[0]?.icon;
                    return (
                      <Paper key={cat.title} withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'blue.0'}>
                        <Group gap="xs" px="xs" mb="xs">
                          {FirstIcon && <FirstIcon size="1rem" color={`var(--mantine-color-${cat.items[0].color}-6)`} />}
                          <Text size="xs" fw={800} c={`${cat.items[0]?.color || 'blue'}.7`} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{cat.title}</Text>
                        </Group>
                        <Stack gap={2}>
                          {cat.items.map(renderDraggableItem)}
                        </Stack>
                      </Paper>
                    );
                  })}
                  
                  {/* A heading with nothing under it reads as a broken list. When a
                      search is running and none of the saved connections match, the
                      whole block goes rather than leaving the label stranded. */}
                  {(!searchActive || filterExisting(sources, 'source').length > 0) && (
                  <Box>
                    <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Existing Sources</Text>
                    <Stack gap={2}>
                      {filterExisting(sources, 'source').map(s => renderDraggableItem({
                        type: 'source',
                        refId: s.id,
                        label: s.name,
                        subType: s.type,
                        icon: IconTable,
                        color: 'blue'
                      }))}
                    </Stack>
                  </Box>
                  )}
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            <Tabs.Panel value="sinks" h="100%">
              <ScrollArea h="100%" offsetScrollbars type="always" px="xs">
                <Stack gap="lg" py="xs">
                  {renderNoMatches('sinks')}
                  {filterCategories(NODE_CATEGORIES.filter(cat => cat.group === 'sinks'), paletteSearch).map((cat) => {
                    const FirstIcon = cat.items[0]?.icon;
                    return (
                      <Paper key={cat.title} withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'green.0'}>
                        <Group gap="xs" px="xs" mb="xs">
                          {FirstIcon && <FirstIcon size="1rem" color={`var(--mantine-color-${cat.items[0].color}-6)`} />}
                          <Text size="xs" fw={800} c={`${cat.items[0]?.color || 'green'}.7`} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{cat.title}</Text>
                        </Group>
                        <Stack gap={2}>
                          {cat.items.map(renderDraggableItem)}
                        </Stack>
                      </Paper>
                    );
                  })}

                  {/* A heading with nothing under it reads as a broken list. When a
                      search is running and none of the saved connections match, the
                      whole block goes rather than leaving the label stranded. */}
                  {(!searchActive || filterExisting(sinks, 'sink').length > 0) && (
                  <Box>
                    <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Existing Sinks</Text>
                    <Stack gap={2}>
                      {filterExisting(sinks, 'sink').map(s => renderDraggableItem({
                        type: 'sink',
                        refId: s.id,
                        label: s.name,
                        subType: s.type,
                        icon: IconTable,
                        color: 'green'
                      }))}
                    </Stack>
                  </Box>
                  )}
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            {/* Config panel removed by request */}

            <Tabs.Panel value="copilot" h="100%">
              <ScrollArea h="100%" offsetScrollbars type="always" px="xs">
                <Stack py="xs">
                  <Paper withBorder p="md" radius="md">
                    <AICopilot />
                  </Paper>
                </Stack>
              </ScrollArea>
            </Tabs.Panel>

            <Tabs.Panel value="settings" h="100%">
              <ScrollArea h="100%" offsetScrollbars type="always" px="xs">
                <Stack gap="lg" py="xs">
                  <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'blue.0'}>
                    <Group gap="xs" mb="md">
                      <ThemeIcon variant="light" color="blue" size="sm">
                        <IconShieldLock size="0.8rem" />
                      </ThemeIcon>
                      <Text fw={700} size="sm">Reliability Policy</Text>
                    </Group>
                    
                    <Stack gap="md">
                      <Select
                        label="Dead Letter Sink"
                        placeholder="None"
                        data={(Array.isArray(sinks) ? sinks : []).map((s: any) => ({ value: s.id, label: s.name }))}
                        value={deadLetterSinkID}
                        onChange={(val) => setDeadLetterSinkID(val || '')}
                        clearable
                        size="xs"
                        description="Sink for messages that exhaust retries"
                        error={deadLetterSinkID && !dlqSupportsRecovery ? "Sink type might not support recovery" : null}
                      />
                      <NumberInput
                        label="DLQ Alert Threshold"
                        placeholder="0 (Disabled)"
                        value={dlqThreshold}
                        onChange={(val) => setDlqThreshold(Number(val))}
                        min={0}
                        size="xs"
                        description="Trigger alert when DLQ reaches this count"
                      />
                      {deadLetterSinkID && !dlqSupportsRecovery && (
                        <Alert color="yellow" icon={<IconInfoCircle size="0.8rem" />} py="xs" styles={{ message: { fontSize: rem(10) } }}>
                          Requires a sink that can also act as a source for recovery.
                        </Alert>
                      )}
                      <Stack gap="xs">
                        <Checkbox 
                          label={<Text size="xs" fw={500}>Prioritize DLQ on startup</Text>}
                          checked={prioritizeDLQ}
                          onChange={(e) => setPrioritizeDLQ(e.currentTarget.checked)}
                          disabled={!!(deadLetterSinkID && !dlqSupportsRecovery)}
                        />
                        
                        <Checkbox 
                          label={<Text size="xs" fw={500}>Dry-Run Mode</Text>}
                          checked={dryRun}
                          onChange={(e) => setDryRun(e.currentTarget.checked)}
                        />
                      </Stack>
                    </Stack>
                  </Paper>
                  
                  <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'orange.0'}>
                    <Group gap="xs" mb="md">
                      <ThemeIcon variant="light" color="orange" size="sm">
                        <IconRefresh size="0.8rem" />
                      </ThemeIcon>
                      <Text fw={700} size="sm">Retry & Reconnect</Text>
                    </Group>

                    <Stack gap="sm">
                      <NumberInput
                        label="Max Retries"
                        value={maxRetries}
                        onChange={(val) => setMaxRetries(Number(val))}
                        min={0}
                        max={100}
                        size="xs"
                      />
                      <Group grow gap="sm">
                        <TextInput
                          label="Retry Interval"
                          placeholder="100ms"
                          value={retryInterval}
                          onChange={(e) => setRetryInterval(e.currentTarget.value)}
                          size="xs"
                        />
                        <TextInput
                          label="Reconnect Interval"
                          placeholder="30s"
                          value={reconnectInterval}
                          onChange={(e) => setReconnectInterval(e.currentTarget.value)}
                          size="xs"
                        />
                      </Group>
                    </Stack>
                  </Paper>

                  <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'indigo.0'}>
                    <Group gap="xs" mb="md">
                      <ThemeIcon variant="light" color="indigo" size="sm">
                        <IconSettingsAutomation size="0.8rem" />
                      </ThemeIcon>
                      <Text fw={700} size="sm">Workflow Schedule</Text>
                    </Group>
                    <CronInput
                      label="Cron Expression"
                      value={cron || ''}
                      onChange={setCron}
                      description="Schedule for the entire workflow"
                    />
                  </Paper>

                  <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'teal.0'}>
                    <Group gap="xs" mb="md">
                      <ThemeIcon variant="light" color="teal" size="sm">
                        <IconDatabase size="0.8rem" />
                      </ThemeIcon>
                      <Text fw={700} size="sm">Data Governance</Text>
                    </Group>
                    
                    <Stack gap="sm">
                      <Select
                        label="Schema Validation"
                        placeholder="Disabled"
                        data={[
                          { value: '', label: 'Disabled' },
                          { value: 'json', label: 'JSON Schema' },
                          { value: 'avro', label: 'Avro' },
                          { value: 'protobuf', label: 'Protobuf' },
                        ]}
                        value={schemaType}
                        onChange={(val) => setSchemaType(val || '')}
                        size="xs"
                        clearable
                      />

                      <Select
                        label="Workspace"
                        placeholder="None (Default)"
                        data={(Array.isArray(workspaces) ? workspaces : []).map((ws: any) => ({ value: ws.id, label: ws.name }))}
                        value={workspaceID}
                        onChange={(val) => setWorkspaceID(val || '')}
                        size="xs"
                        clearable
                        leftSection={<IconFilter size="0.8rem" />}
                      />

                      <Divider label="Resource Requests" labelPosition="center" />

                      <SimpleGrid cols={2}>
                        <NumberInput
                          label="CPU Request"
                          description="Cores"
                          min={0}
                          step={0.1}
                          decimalScale={1}
                          value={cpuRequest}
                          onChange={(val) => setCPURequest(Number(val))}
                          size="xs"
                        />
                        <NumberInput
                          label="Mem Request"
                          description="MB"
                          min={0}
                          value={memoryRequest}
                          onChange={(val) => setMemoryRequest(Number(val))}
                          size="xs"
                        />
                      </SimpleGrid>
                      <NumberInput
                        label="Throughput Request"
                        description="Estimated msgs/sec"
                        min={0}
                        value={throughputRequest}
                        onChange={(val) => setThroughputRequest(Number(val))}
                        size="xs"
                      />
                      
                      {schemaType && (
                        <Stack gap={4}>
                           <Text size="xs" fw={500}>Schema Definition</Text>
                           <Box style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: '4px', overflow: 'hidden' }}>
                              <textarea
                                value={schema}
                                onChange={(e) => setSchema(e.currentTarget.value)}
                                placeholder={schemaType === 'json' ? '{ "type": "object", ... }' : 'Schema definition...'}
                                style={{
                                  width: '100%',
                                  height: '150px',
                                  padding: '8px',
                                  fontFamily: 'monospace',
                                  fontSize: 'var(--mantine-font-size-xs)',
                                  border: 'none',
                                  outline: 'none',
                                  resize: 'vertical',
                                  backgroundColor: isDark ? 'var(--mantine-color-dark-8)' : 'white',
                                  color: isDark ? 'white' : 'black'
                                }}
                              />
                           </Box>
                        </Stack>
                      )}

                      <Divider label="Retention" labelPosition="center" />
                      
                      <Select
                        label="Trace Retention"
                        placeholder="7 Days"
                        data={[
                          { value: '1d', label: '1 Day' },
                          { value: '3d', label: '3 Days' },
                          { value: '7d', label: '7 Days' },
                          { value: '14d', label: '14 Days' },
                          { value: '30d', label: '30 Days' },
                          { value: '90d', label: '90 Days' },
                          { value: '0', label: 'Indefinite' },
                        ]}
                        defaultValue="7d"
                        size="xs"
                      />

                      <Select
                        label="Audit Log Retention"
                        placeholder="30 Days"
                        data={[
                          { value: '7d', label: '7 Days' },
                          { value: '30d', label: '30 Days' },
                          { value: '90d', label: '90 Days' },
                          { value: '180d', label: '180 Days' },
                          { value: '365d', label: '1 Year' },
                          { value: '0', label: 'Indefinite' },
                        ]}
                        defaultValue="30d"
                        size="xs"
                      />
                    </Stack>
                  </Paper>
                  
                  <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'indigo.0'}>
                    <Group gap="xs" mb="md">
                      <ThemeIcon variant="light" color="indigo" size="sm">
                        <IconTags size="0.8rem" />
                      </ThemeIcon>
                      <Text fw={700} size="sm">Organization</Text>
                    </Group>
                    
                    <Stack gap="sm">
                      <TagsInput
                        label="Workflow Tags"
                        placeholder="Add tags..."
                        data={tags || []}
                        value={tags || []}
                        onChange={setTags}
                        size="xs"
                        clearable
                      />
                    </Stack>
                  </Paper>
                </Stack>
              </ScrollArea>
            </Tabs.Panel>
          </Box>
        </Tabs>
      </Stack>
    </Paper>
  );
}


