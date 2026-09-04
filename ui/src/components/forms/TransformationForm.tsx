import { useState, useEffect, useCallback, lazy, Suspense, useMemo } from 'react';
import { TextInput, Select, Stack, Alert, Divider, Text, Group, ActionIcon, Button, Code, List, Autocomplete, JsonInput, Badge, Grid, SimpleGrid, NumberInput, Card, ScrollArea, Box, Switch, Textarea, Modal, Loader, Tooltip as MantineTooltip } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { usePreviewTransformation } from '../../pages/workflows/WorkflowEditor/hooks/usePreviewTransformation';
import { useTargetSchema } from '../../pages/workflows/WorkflowEditor/hooks/useTargetSchema';
import { resolveConfigComponent } from '../workflow/Transformation/configs/registry';
// Lazy-load heavy UI components to reduce initial bundle size (Junie compliance)
const PreviewPanel = lazy(() =>
  import('../workflow/Transformation/PreviewPanel').then((m) => ({ default: m.PreviewPanel }))
);
const FieldExplorer = lazy(() =>
  import('../workflow/Transformation/FieldExplorer').then((m) => ({ default: m.FieldExplorer }))
);
const TargetExplorer = lazy(() =>
  import('../workflow/Transformation/TargetExplorer').then((m) => ({ default: m.TargetExplorer }))
);
const SetFieldEditor = lazy(() =>
  import('../workflow/Transformation/SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);
const QuickActions = lazy(() =>
  import('../workflow/Transformation/QuickActions').then((m) => ({ default: m.QuickActions }))
);
import { IconArrowRight, IconCode, IconDatabase, IconFunction, IconHelpCircle, IconInfoCircle, IconList, IconPlus, IconPuzzle, IconRefresh, IconSearch, IconSettings, IconVariable } from '@tabler/icons-react';
import { preparePayload, getValByPath } from '@/utils/transformationUtils';
import { guideFor } from '@/lib/transformationGuide';

// Modular configuration components (Junie compliance)

interface TransformationFormProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  onRunSimulation?: (payload?: any) => void;
  availableFields: any[];
  incomingPayload?: any;
  sources?: any[];
  sinkSchema?: any;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
}

export function TransformationForm({ selectedNode, updateNodeConfig, onRunSimulation: _onRunSimulation, availableFields = [], incomingPayload, sources = [], sinkSchema, onRefreshFields, isRefreshing }: TransformationFormProps) {
  const [testing, setTesting] = useState(false);
  const { fields: targetSchema, loading: loadingTarget, refetch: refetchTarget } = useTargetSchema({ sinkSchema });

  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  const [previewResult, setPreviewResult] = useState<any>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const [configSearch, setConfigSearch] = useState('');
  // Accessibility: IDs for help modal labelling
  const helpTitleId = 'transformation-help-modal-title';
  const helpDescId = 'transformation-help-modal-desc';

  // Lazy-load heavy help content to reduce initial bundle
  const HelpContent = lazy(() => import('../workflow/Transformation/HelpContent'));

  const handleApplyTemplate = (template: string) => {
    switch (template) {
      case 'pii_masking':
        updateNodeConfig(selectedNode.id, { 
          transType: 'mask', 
          field: '*', 
          maskType: 'pii',
          label: 'Mask PII'
        }, true);
        break;
      case 'mask_emails':
        updateNodeConfig(selectedNode.id, { 
          transType: 'mask', 
          field: 'email', 
          maskType: 'email',
          label: 'Mask Emails'
        }, true);
        break;
      case 'flatten':
        updateNodeConfig(selectedNode.id, { 
          transType: 'set', 
          'column.': '.', 
          label: 'Flatten Record'
        }, true);
        break;
      case 'audit_fields':
        updateNodeConfig(selectedNode.id, { 
          'column._processed_at': `now()`,
          'column._node_id': selectedNode.id,
        });
        notifications.show({ message: 'Audit fields added.', color: 'green' });
        break;
      case 'clear':
        updateNodeConfig(selectedNode.id, { label: selectedNode.data.label }, true);
        notifications.show({ message: 'Configuration cleared.', color: 'blue' });
        break;
    }
  };

  const previewMutation = usePreviewTransformation();

  const transType = selectedNode?.data?.transType || selectedNode?.type || '';
  const isForeach = transType === 'foreach' || transType === 'fanout';
  const isAggregate = transType === 'aggregate' || transType === 'stateful';

  const runPreview = useCallback(async () => {
    if (!incomingPayload) return;
    setTesting(true);
    setPreviewError(null);
    previewMutation.mutate(
      {
        transformation: {
          type: transType,
          config: selectedNode.data,
        },
        message: incomingPayload,
      },
      {
        onSuccess: (data: any) => {
          if (data?.error) {
            setPreviewError(data.error);
          } else {
            setPreviewResult(data);
          }
        },
        onError: (e: any) => {
          setPreviewError(e?.message || 'Preview failed');
        },
        onSettled: () => setTesting(false),
      }
    );
  }, [previewMutation, incomingPayload, selectedNode.data, transType]);


  useEffect(() => {
    const timer = setTimeout(() => {
      runPreview();
    }, 1000);
    return () => clearTimeout(timer);
  }, [selectedNode.data, incomingPayload, runPreview]);

  if (!selectedNode) return null;

  const addField = (path: string = '', value: string = '') => {
    const fields = Object.entries(selectedNode.data)
      .filter(([k]) => k.startsWith('column.'));
    const fieldName = path || `new_field_${fields.length}`;
    updateNodeConfig(selectedNode.id, { [`column.${fieldName}`]: value });
  };

  const addFromSource = async (path: string) => {
    if (transType === 'advanced') {
      addField(path, `source.${path}`);
    } else if (transType === 'set') {
      addField(path, `source.${path}`);
    } else if (transType === 'mapping') {
      updateNodeConfig(selectedNode.id, { field: path });
    } else if (transType === 'filter_data' || transType === 'condition' || transType === 'validate') {
      let conditions: any[] = [];
      try {
        conditions = typeof selectedNode.data.conditions === 'string' 
          ? JSON.parse(selectedNode.data.conditions || '[]')
          : (selectedNode.data.conditions || []);
      } catch (e) { conditions = []; }
      
      const next = [...conditions, { field: path, operator: '=', value: '' }];
      updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) });
    } else if (transType === 'mask') {
      updateNodeConfig(selectedNode.id, { field: path });
    } else {
      try { await navigator.clipboard.writeText(path); } catch {}
      notifications.show({ message: `Path "${path}" copied to clipboard.`, color: 'blue' });
    }
  };

  const testLookup = async () => {
    if (!incomingPayload) {
      notifications.show({ title: 'Error', message: 'No sample input available to test with.', color: 'red' });
      return;
    }
    setTesting(true);
    try {
      const res = await apiFetch('/api/transformations/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transformation: {
            type: transType,
            config: selectedNode.data
          },
          message: incomingPayload
        })
      });
      const data = await res.json();
      if (data.error) {
        notifications.show({ title: 'Test Failed', message: data.error, color: 'orange' });
      } else {
        const result = preparePayload(data);
        const targetField = selectedNode.data.targetField || selectedNode.data.target_field;
        const val = getValByPath(result, targetField);
        
        notifications.show({ 
          title: 'Test Success', 
          message: `Result for "${targetField}": ${val === undefined ? 'Not Found' : JSON.stringify(val)}`, 
          color: 'green' 
        });
      }
    } catch (e: any) {
      notifications.show({ title: 'Error', message: e.message, color: 'red' });
    } finally {
      setTesting(false);
    }
  };


  // Helper for Validator Rules

  const renderPathHelp = () => (
    <Card withBorder padding="md" radius="md">
      <Group gap="xs" mb="sm">
        <IconInfoCircle size="1rem" color="var(--mantine-color-blue-filled)" />
        <Text size="xs" fw={700}>Data Access Guide</Text>
      </Group>
      <Stack gap="xs">
        <Text size="xs">• Nested: <Code>user.profile.name</Code></Text>
        <Text size="xs">• Arrays: <Code>items.0.id</Code></Text>
        <Text size="xs">• Reference: prefix with <Code>source.</Code></Text>
      </Stack>
    </Card>
  );

  const onInsertExample = (example: string) => addField('', example);

  const FunctionLibrary = () => {
    const [search, setSearch] = useState('');
    const functions = [
      { name: 'lower(str)', desc: 'Lowercase a string', example: 'lower(source.name)', snippet: 'lower($0)' },
      { name: 'upper(str)', desc: 'Uppercase a string', example: 'upper(source.name)', snippet: 'upper($0)' },
      { name: 'trim(str)', desc: 'Trim whitespace', example: 'trim(source.name)', snippet: 'trim($0)' },
      { name: 'concat(a, b, ...)', desc: 'Join strings', example: 'concat(source.first, " ", source.last)', snippet: 'concat($0)' },
      { name: 'substring(s, start, [end])', desc: 'Extract part of string', example: 'substring(source.id, 0, 8)', snippet: 'substring($0, 0, 8)' },
      { name: 'replace(s, old, new)', desc: 'Replace substring', example: 'replace(source.email, "@", "[at]")', snippet: 'replace($0, "@", "")' },
      { name: 'coalesce(a, b, ...)', desc: 'First non-empty value', example: 'coalesce(source.nickname, source.name)', snippet: 'coalesce($0)' },
      { name: 'now()', desc: 'Current ISO date', example: 'now()', snippet: 'now()' },
      { name: 'date_format(d, format)', desc: 'Format date', example: 'date_format(source.created, "2006-01-02")', snippet: 'date_format($0, "2006-01-02")' },
      { name: 'hash(s, [algo])', desc: 'SHA256/MD5 hash', example: 'hash(source.email, "md5")', snippet: 'hash($0, "sha256")' },
      { name: 'add(a, b)', desc: 'Addition', example: 'add(source.price, source.tax)', snippet: 'add($0, 0)' },
      { name: 'round(v, [p])', desc: 'Round number', example: 'round(source.total, 2)', snippet: 'round($0, 2)' },
    ];
    const filtered = functions.filter(f => f.name.toLowerCase().includes(search.toLowerCase()) || f.desc.toLowerCase().includes(search.toLowerCase()));

    return (
      <Card withBorder padding="md" radius="md">
        <Group gap="xs" mb="sm">
          <IconFunction size="1rem" color="var(--mantine-color-orange-6)" />
          <Text size="xs" fw={700}>FUNCTION LIBRARY</Text>
        </Group>
        <TextInput 
          placeholder="Search functions..." 
          size="xs" 
          mb="xs"
          leftSection={<IconSearch size="0.8rem" />}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <ScrollArea h={200} type="auto">
          <Stack gap="xs">
            {filtered.map(f => (
              <Box key={f.name} p="xs" style={{ borderRadius: 4, background: 'var(--mantine-color-orange-light)', border: '1px solid var(--mantine-color-orange-light-color)', cursor: 'pointer' }} onClick={() => onInsertExample(f.example)}>
                <Group justify="space-between">
                  <Text size="xs" fw={700} c="var(--mantine-color-orange-light-color)">{f.name}</Text>
                  <ActionIcon size="xs" variant="subtle" color="orange">
                    <IconPlus size="0.8rem" />
                  </ActionIcon>
                </Group>
                <Text size="xs" c="dimmed">{f.desc}</Text>
                <Code mt={2} style={{ fontSize: 'var(--mantine-font-size-xs)' }}>{f.example}</Code>
              </Box>
            ))}
          </Stack>
        </ScrollArea>
      </Card>
    );
  };

  const renderConfiguration = () => {
    const commonProps = {
      config: selectedNode.data,
      updateNodeConfig,
      nodeId: selectedNode.id,
      availableFields,
      sources,
      incomingPayload,
      onTest: testLookup,
      testing
    };

    // Node type, then transformation subtype — resolved from a registry rather
    // than a switch, so adding a transformation never means editing this file.
    // See configs/registry.ts.
    const Config = resolveConfigComponent(selectedNode.type, transType);
    if (Config) {
      return (
        <Suspense fallback={<Loader size="sm" />}>
          <Config
            {...commonProps}
            fieldPaths={fieldPaths}
            addField={addField}
            onAddFromSource={addFromSource}
            testLookup={testLookup}
            transType={transType}
          />
        </Suspense>
      );
    }

    if (selectedNode.type === 'merge') {
      return (
        <Alert icon={<IconInfoCircle size="1rem" />} color="cyan">
          <Text size="sm">Merge nodes join parallel paths by waiting for all incoming branches.</Text>
        </Alert>
      );
    }

    return (
      <Alert color="gray">
        <Text size="sm">Select a transformation type to begin configuration.</Text>
      </Alert>
    );
  };

  return (
    <>
    <Grid gap="lg" style={{ minHeight: 'calc(100vh - 180px)' }}>
      {/* Column 1: Source Data */}
      <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
        <Stack gap="lg" h="100%">
          <Group justify="space-between" px="xs">
            <Group gap="xs">
               <IconDatabase size="1.2rem" color="var(--mantine-color-blue-6)" />
               <Text size="sm" fw={700}>SOURCE DATA</Text>
            </Group>
            <Badge variant="dot" size="sm">INPUT</Badge>
          </Group>
          
          {renderPathHelp()}

          <Card withBorder padding="md" radius="md">
            <Group justify="space-between" mb="sm">
              <Group gap="xs">
                <IconList size="1rem" color="var(--mantine-color-gray-6)" />
                <Text size="xs" fw={700}>AVAILABLE FIELDS</Text>
                {onRefreshFields && (
                  <MantineTooltip label="Refresh sample data and fields" position="right">
                    <ActionIcon aria-label="Refresh" variant="subtle" size="xs" onClick={() => { onRefreshFields(); refetchTarget(); }} color="blue" loading={isRefreshing}>
                      <IconRefresh size="0.8rem" />
                    </ActionIcon>
                  </MantineTooltip>
                )}
              </Group>
              <Badge size="xs" variant="light">{(availableFields || []).length}</Badge>
            </Group>
            <Suspense fallback={<Text size="xs" c="dimmed">Loading fields…</Text>}>
              <FieldExplorer
                availableFields={availableFields}
                incomingPayload={incomingPayload}
                onAdd={(path) => addFromSource(path)}
              />
            </Suspense>
          </Card>

          <Card withBorder padding="md" radius="md">
            <Group justify="space-between" mb="sm">
              <Group gap="xs">
                <IconDatabase size="1rem" color="var(--mantine-color-green-6)" />
                <Text size="xs" fw={700}>TARGET COLUMNS</Text>
              </Group>
              <Badge size="xs" variant="light" color="green">{targetSchema.length}</Badge>
            </Group>
            <Suspense fallback={<Text size="xs" c="dimmed">Loading target columns…</Text>}>
              <TargetExplorer
                fields={targetSchema}
                sinkSchemaPresent={!!sinkSchema}
                currentMappings={selectedNode.data as Record<string, string>}
                tableName={sinkSchema?.config?.table}
                loading={loadingTarget}
                onMap={(column, data) => {
                  updateNodeConfig(selectedNode.id, { [`column.${column}`]: data })
                  notifications.show({
                    title: 'Field mapped',
                    message: `Mapped ${data} to ${column}`,
                    color: 'green',
                  })
                }}
                onClearMap={(column) => {
                  const newData: any = { ...selectedNode.data }
                  delete newData[`column.${column}`]
                  updateNodeConfig(selectedNode.id, newData, true)
                }}
              />
            </Suspense>
          </Card>

          {transType === 'advanced' && <FunctionLibrary />}

          <Card withBorder padding="md" radius="md" bg="var(--mantine-color-body)">
             <Group gap="xs" mb="sm">
                <IconCode size="1rem" color="dimmed" />
                <Text size="xs" fw={700} c="dimmed">RAW PAYLOAD</Text>
             </Group>
             <ScrollArea.Autosize mah={300}>
                <Code block style={{ fontSize: 'var(--mantine-font-size-xs)' }}>
                   {incomingPayload ? JSON.stringify(incomingPayload, null, 2) : 'No input sample available'}
                </Code>
             </ScrollArea.Autosize>
          </Card>
        </Stack>
      </Grid.Col>

      {/* Column 2: Configuration */}
      <Grid.Col span={{ base: 12, md: 8, lg: 5 }}>
        <Card withBorder shadow="md" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
          <Stack gap="lg" h="100%">
            <Group justify="space-between" px="xs">
              <Group gap="xs">
                <IconSettings size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700}>TRANSFORM LOGIC</Text>
              </Group>
              <Group gap="xs">
                <MantineTooltip label="How to use this transformation" position="left">
                  <ActionIcon aria-label="Open transformation help" variant="light" color="blue" onClick={() => setHelpOpen(true)}>
                    <IconHelpCircle size="1rem" />
                  </ActionIcon>
                </MantineTooltip>
                {/* The human name, not the raw registry key: "History tracking
                    (SCD)" orients; "SCD" is a password. */}
                <Badge variant="light" color="blue" size="lg">{guideFor(transType).title}</Badge>
              </Group>
            </Group>

            {/* What this node does and what to do first, where the eyes
                already are — the answer used to live only behind the help
                icon's modal. */}
            {guideFor(transType).what && (
              <Text size="sm" c="dimmed">
                {guideFor(transType).what}{' '}
                <Text span size="sm" fw={500} c="var(--mantine-color-text)">
                  {guideFor(transType).firstStep}
                </Text>
              </Text>
            )}

            {!incomingPayload && (
              <Alert icon={<IconInfoCircle size="1rem" />} color="blue" py="xs">
                <Text size="xs">
                  No sample data yet, so the live preview has nothing to show.
                  Use the refresh icon next to AVAILABLE FIELDS to pull a sample
                  from your source — then every change previews as you type.
                </Text>
              </Alert>
            )}

            <Divider />

            <Group gap="xs" grow>
              <TextInput 
                label="Node Label" 
                placeholder="Display name in editor" 
                leftSection={<IconVariable size="1rem" />}
                value={selectedNode.data.label || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { label: e.target.value })} 
                flex={1}
              />
              <TextInput
                label="Search Settings"
                placeholder="Filter configuration..."
                leftSection={<IconSearch size="1rem" />}
                value={configSearch}
                onChange={(e) => setConfigSearch(e.target.value)}
                flex={1}
              />
            </Group>

            <Box flex={1} style={{ overflow: 'hidden' }}>
              <ScrollArea h="100%" offsetScrollbars>
                <Stack gap="lg" py="md">
                  <Suspense fallback={null}>
                    <QuickActions onApplyTemplate={handleApplyTemplate} />
                  </Suspense>
                  <Divider label="Configuration" labelPosition="center" />
                  
                  {renderConfiguration()}

                  <Divider label="Advanced" labelPosition="center" mt="xl" />


          {isForeach && (
            <>
              <Autocomplete
                label="Array Path"
                placeholder="e.g. items"
                data={fieldPaths || []}
                value={selectedNode.data.arrayPath || ''}
                onChange={(val) => updateNodeConfig(selectedNode.id, { arrayPath: val })}
                description="Path to the array you want to fan out."
              />
              <TextInput
                label="Result Field"
                placeholder="_fanout"
                value={selectedNode.data.resultField ?? '_fanout'}
                onChange={(e) => updateNodeConfig(selectedNode.id, { resultField: e.target.value || '_fanout' })}
                description="Where to store the expanded array on the message."
              />
              <TextInput
                label="Item Path (optional)"
                placeholder="e.g. product.id"
                value={selectedNode.data.itemPath || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { itemPath: e.target.value })}
                description="If items are objects, select a nested value for each item."
              />
              <TextInput
                label="Index Field (optional)"
                placeholder="e.g. _index"
                value={selectedNode.data.indexField || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { indexField: e.target.value })}
                description="If items are objects, also write their index to this field."
              />
              <NumberInput
                label="Limit (optional)"
                placeholder="0 (no limit)"
                min={0}
                value={Number.isFinite(Number(selectedNode.data.limit)) ? Number(selectedNode.data.limit) : 0}
                onChange={(val) => updateNodeConfig(selectedNode.id, { limit: String(val ?? 0) })}
              />
              <Switch
                label="Drop when empty"
                checked={!!selectedNode.data.dropEmpty}
                onChange={(e) => updateNodeConfig(selectedNode.id, { dropEmpty: e.currentTarget.checked })}
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="violet" py="xs">
                <Text size="xs">Foreach/Fanout collects items from the given array and stores them under the Result Field. The preview shows this array directly.</Text>
              </Alert>
            </>
          )}

          {transType === 'lua' && (
            <>
              <Code block mb="xs">
{`-- Lua Script Example
function transform(msg)
  msg.data["new_field"] = "from lua"
  return msg
end`}
              </Code>
              <Textarea 
                label="Lua Script" 
                placeholder="function transform(msg) ... end" 
                value={selectedNode.data.script || ''} 
                onChange={(e: any) => updateNodeConfig(selectedNode.id, { script: e.target.value })} 
                minRows={15}
                autosize
                styles={{ input: { fontFamily: 'monospace' } }}
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="blue" py="xs">
                <Text size="xs">Lua scripts must define a `transform(msg)` function that returns the modified message.</Text>
              </Alert>
            </>
          )}

          {transType === 'wasm' && (
            <>
              {selectedNode.data.pluginID && (
                <Alert icon={<IconPuzzle size="1rem" />} color="indigo" mb="sm">
                  <Text size="sm" fw={700}>Marketplace Plugin: {selectedNode.data.label}</Text>
                  <Text size="xs">Using installed WASM binary for plugin <code>{selectedNode.data.pluginID}</code>. No manual upload or URL needed.</Text>
                </Alert>
              )}
              <TextInput
                label="WASM Function Name"
                placeholder="transform"
                value={selectedNode.data.function || 'transform'}
                onChange={(e) => updateNodeConfig(selectedNode.id, { function: e.target.value })}
                mb="sm"
              />
              {!selectedNode.data.pluginID && (
                <Textarea
                  label="WASM Binary (Base64 or URL)"
                  placeholder="AGFzbQEAAAAB..."
                  value={selectedNode.data.wasmBytes || ''}
                  onChange={(e) => updateNodeConfig(selectedNode.id, { wasmBytes: e.target.value })}
                  minRows={10}
                  autosize
                  styles={{ input: { fontFamily: 'monospace' } }}
                />
              )}
              <Alert icon={<IconInfoCircle size="1rem" />} color="blue" py="xs">
                <Text size="xs">WebAssembly module should use WASI for I/O (JSON via stdin/stdout) and export the specified function.</Text>
              </Alert>
            </>
          )}

          {isAggregate && (
            <>
              <Select 
                label="Operation" 
                data={[
                  { label: 'Count', value: 'count' },
                  { label: 'Sum', value: 'sum' },
                  { label: 'Average', value: 'avg' },
                ]} 
                value={selectedNode.data.type || selectedNode.data.operation || 'count'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { type: val, operation: val })} 
              />
              <Autocomplete 
                label="Field to Aggregate" 
                placeholder="e.g. amount" 
                data={fieldPaths || []}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Supports nested objects and arrays."
              />
              <TextInput 
                label="Group By Field" 
                placeholder="e.g. customer_id" 
                value={selectedNode.data.groupBy || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { groupBy: e.target.value })} 
              />
              <TextInput 
                label="Output Field" 
                placeholder="e.g. total_amount" 
                value={selectedNode.data.targetField || selectedNode.data.outputField || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value, outputField: e.target.value })} 
              />
              <Divider label="Windowing" labelPosition="center" />
              <Group grow>
                <Select
                  label="Window Type"
                  data={[
                    { label: 'Session', value: 'session' },
                    { label: 'Tumbling', value: 'tumbling' },
                  ]}
                  value={selectedNode.data.windowType || 'session'}
                  onChange={(val) => updateNodeConfig(selectedNode.id, { windowType: val || 'session' })}
                />
                <TextInput
                  label="Window Duration"
                  placeholder="e.g. 5m, 1h"
                  value={selectedNode.data.window || ''}
                  onChange={(e) => updateNodeConfig(selectedNode.id, { window: e.target.value })}
                />
              </Group>
              <Switch
                label="Persistent State (Saves across restarts)"
                checked={!!selectedNode.data.persistent}
                onChange={(e) => updateNodeConfig(selectedNode.id, { persistent: e.currentTarget.checked })}
                mt="xs"
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="cyan" py="xs" mt="md">
                <Text size="xs">Aggregate nodes maintain internal state to summarize data over windows or groups.</Text>
              </Alert>
            </>
          )}






          {transType === 'set' && (
            <>
              <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light" mb="sm">
                <Stack gap="xs">
                  <Text size="xs" fw={700}>How to fill from source:</Text>
                  <Text size="xs">1. Use the <Badge size="xs" variant="light">+ field</Badge> badges above for one-click field copying.</Text>
                  <Text size="xs">2. Or type <Code>source.path</Code> in the value field (e.g. <Code>source.id</Code>).</Text>
                  <Text size="xs">3. Use the <IconArrowRight size="0.8rem" /> icon in the value field to auto-fill <Code>source.path</Code>.</Text>
                </Stack>
              </Alert>
              <Suspense fallback={<Text size="xs" c="dimmed">Loading field editor…</Text>}>
                <SetFieldEditor
                  selectedNode={selectedNode}
                  updateNodeConfig={updateNodeConfig}
                  availableFields={availableFields}
                  incomingPayload={incomingPayload}
                  transType={transType}
                  onAddFromSource={addFromSource}
                  addField={addField}
                />
              </Suspense>
              <Divider label="Raw JSON" labelPosition="center" mt="md" />
              <JsonInput 
                label="Fields (JSON)" 
                placeholder='{"column.user.role": "admin", "column.status": 1}' 
                value={JSON.stringify(Object.fromEntries(Object.entries(selectedNode.data).filter(([k]) => k.startsWith('column.'))), null, 2)} 
                onChange={(val) => {
                   try {
                      const parsed = JSON.parse(val);
                      const baseData = Object.fromEntries(Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.')));
                      updateNodeConfig(selectedNode.id, { ...baseData, ...parsed }, true);
                   } catch(e) {}
                }} 
                formatOnBlur
                minRows={10}
                styles={{ 
                  input: { fontFamily: 'monospace', fontSize: '11px' } 
                }}
                description="Specify fields to set using 'column.path' format."
              />
            </>
          )}

          {transType === 'pipeline' && (
            <Stack gap="xs" style={{ flex: 1 }}>
              <Text size="sm" fw={500}>Steps</Text>
              <JsonInput 
                label="Steps (JSON Array)" 
                placeholder='[{"transType": "mask", "field": "email", "maskType": "email"}, {"transType": "set", "column.processed": true}]' 
                value={selectedNode.data.steps || '[]'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { steps: val })} 
                formatOnBlur
                minRows={20}
                styles={{ 
                  root: { flex: 1, display: 'flex', flexDirection: 'column' },
                  wrapper: { flex: 1, display: 'flex', flexDirection: 'column' },
                  input: { flex: 1, fontFamily: 'monospace', fontSize: '11px' } 
                }}
                description="List of transformation steps to execute in order."
              />
            </Stack>
          )}

          {transType === 'advanced' && (
            <>
              <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light" mb="sm">
                <Stack gap="xs">
                  <Text size="xs" fw={700}>How to use advanced expressions:</Text>
                  <Text size="xs">1. Use format: <Code>operation(source.field)</Code> or <Code>operation("literal")</Code></Text>
                  <Text size="xs">2. Support nesting: <Code>upper(trim(source.name))</Code></Text>
                  <Text size="xs">3. Use <Code>source.path</Code> for input fields and quotes for strings.</Text>
                </Stack>
              </Alert>
              <Suspense fallback={<Text size="xs" c="dimmed">Loading field editor…</Text>}>
                <SetFieldEditor
                  selectedNode={selectedNode}
                  updateNodeConfig={updateNodeConfig}
                  availableFields={availableFields}
                  incomingPayload={incomingPayload}
                  transType={transType}
                  onAddFromSource={addFromSource}
                  addField={addField}
                />
              </Suspense>
              <Divider label="Raw JSON" labelPosition="center" mt="md" />
              <JsonInput 
                label="Config (JSON)" 
                placeholder='{"column.user.name": "lower(source.user.name)"}' 
                value={JSON.stringify(Object.fromEntries(Object.entries(selectedNode.data || {}).filter(([k]) => k.startsWith('column.'))), null, 2)} 
                onChange={(val) => {
                   try {
                      const parsed = JSON.parse(val);
                      const baseData = Object.fromEntries(Object.entries(selectedNode.data || {}).filter(([k]) => !k.startsWith('column.')));
                      updateNodeConfig(selectedNode.id, { ...baseData, ...parsed }, true);
                   } catch(e) {}
                }} 
                formatOnBlur
                minRows={10}
                styles={{ 
                  input: { fontFamily: 'monospace', fontSize: '11px' } 
                }}
              />
              <Alert color="blue" py="xs" mt="md">
                <Text size="xs" fw={700}>Supported operations:</Text>
                <Grid gap="xs">
                  <Grid.Col span={4}>
                    <List size="xs">
                      <List.Item><Code>lower</Code>, <Code>upper</Code>, <Code>trim</Code></List.Item>
                      <List.Item><Code>concat(a, b, ...)</Code></List.Item>
                      <List.Item><Code>substring(s, start, [end])</Code></List.Item>
                      <List.Item><Code>coalesce(a, b, ...)</Code></List.Item>
                    </List>
                  </Grid.Col>
                  <Grid.Col span={4}>
                    <List size="xs">
                      <List.Item><Code>add</Code>, <Code>sub</Code>, <Code>mul</Code>, <Code>div</Code></List.Item>
                      <List.Item><Code>abs(n)</Code>, <Code>round(n, [p])</Code></List.Item>
                      <List.Item><Code>now()</Code>, <Code>hash(s, [a])</Code></List.Item>
                      <List.Item><Code>if(cond, t, f)</Code></List.Item>
                    </List>
                  </Grid.Col>
                  <Grid.Col span={4}>
                    <List size="xs">
                      <List.Item><Code>and</Code>, <Code>or</Code>, <Code>not</Code></List.Item>
                      <List.Item><Code>eq</Code>, <Code>gt</Code>, <Code>lt</Code>, <Code>contains</Code></List.Item>
                      <List.Item><Code>toInt</Code>, <Code>toFloat</Code></List.Item>
                      <List.Item><Code>toString</Code>, <Code>toBool</Code></List.Item>
                    </List>
                  </Grid.Col>
                </Grid>
              </Alert>
            </>
          )}

          <Divider label="Error Handling" labelPosition="center" mt="xl" mb="md" />
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <Select 
              label="On Error"
              description="Action to take when an error occurs during transformation."
              data={[{label: 'Fail Workflow', value: 'fail'}, {label: 'Continue', value: 'continue'}, {label: 'Drop Message', value: 'drop'}]}
              value={selectedNode.data.onError || 'fail'}
              onChange={(val) => updateNodeConfig(selectedNode.id, { onError: val || 'fail' })}
            />
            <TextInput 
              label="Status Field"
              placeholder="e.g. _trans_status"
              value={selectedNode.data.statusField || ''}
              onChange={(e) => updateNodeConfig(selectedNode.id, { statusField: e.target.value })}
              description="Field to store success/error status."
            />
          </SimpleGrid>
        </Stack>
            </ScrollArea>
          </Box>
        </Stack>
      </Card>
    </Grid.Col>

      <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
        <Suspense fallback={<Text size="sm" c="dimmed">Loading preview…</Text>}>
          <PreviewPanel
            title="3. LIVE PREVIEW"
            loading={testing || (previewMutation as any)?.isPending}
            error={previewError || ((previewMutation as any)?.error?.message ?? null)}
            result={previewResult || (previewMutation as any)?.data}
            original={incomingPayload}
            onRun={runPreview}
          />
        </Suspense>
      </Grid.Col>
    </Grid>

    <Modal 
      opened={helpOpen} 
      onClose={() => setHelpOpen(false)} 
      title={<Group gap="xs"><IconHelpCircle size="1rem" /><Text id={helpTitleId} size="sm" fw={700}>Transformation Help</Text></Group>} 
      aria-labelledby={helpTitleId}
      aria-describedby={helpDescId}
      size="lg" 
      yOffset="10vh"
      withCloseButton
    >
      <Text id={helpDescId} size="sm" c="dimmed" mb="sm">
        Reference of supported operations and examples for building transformation expressions.
      </Text>
      <ScrollArea h={500} offsetScrollbars>
        <Suspense fallback={<Text size="sm">Loading help…</Text>}>
          <HelpContent />
        </Suspense>
      </ScrollArea>
      <Group justify="right" mt="md">
        <Button variant="light" onClick={() => setHelpOpen(false)}>Close</Button>
      </Group>
    </Modal>
    </>
  );
}


