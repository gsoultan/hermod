import { useState, useEffect, useCallback, useRef, lazy, Suspense, useMemo } from 'react';
import { FormRow } from '@/components/common/FormRow';
import { TextInput, Select, Stack, Alert, Divider, Text, Group, ActionIcon, Button, Code, Badge, Grid, SimpleGrid, Card, ScrollArea, Box, Modal, Loader, Tooltip as MantineTooltip } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { usePreviewTransformation } from '../../pages/workflows/WorkflowEditor/hooks/usePreviewTransformation';
import { useTargetSchema } from '../../pages/workflows/WorkflowEditor/hooks/useTargetSchema';
import { useWorkflowStore } from '../../pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { useVHostSecretNames } from '@/lib/vhostSecrets';
import { SecretNamesContext } from '@/components/shared/TemplateField';
import { resolveConfigComponent } from '../workflow/Transformation/configs/registry';
import { nextColumnFieldName } from '../workflow/Transformation/fieldMappings/columnFields';
import { FunctionLibrary } from '../workflow/Transformation/FunctionLibrary';
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
const QuickActions = lazy(() =>
  import('../workflow/Transformation/QuickActions').then((m) => ({ default: m.QuickActions }))
);
// Module scope, not the component body. A `lazy()` call inside render mints a
// new component type every render, so React unmounts and remounts whatever it
// wraps — here, the whole help modal, on every keystroke.
const HelpContent = lazy(() => import('../workflow/Transformation/HelpContent'));
import { IconArrowsMaximize, IconArrowsMinimize, IconCode, IconDatabase, IconHelpCircle, IconInfoCircle, IconList, IconRefresh, IconSearch, IconSettings, IconVariable } from '@tabler/icons-react';
import { preparePayload, getValByPath } from '@/utils/transformationUtils';
import { guideFor } from '@/lib/transformationGuide';
import { UpstreamNotRunNotice } from '@/components/common/UpstreamNotRunNotice';
import type { APILookupTestOutcome } from '../workflow/Transformation/configs/enrichment/APILookupConfig';

// How long to wait after the last edit before previewing. Short enough to feel
// live, long enough that a burst of keystrokes costs one request.
const PREVIEW_DEBOUNCE_MS = 400;

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
  /**
   * How many nodes before this one have no output for incomingPayload (see
   * useNodeContext). Non-zero means Test and the Live Preview work on data the
   * running workflow would have changed first.
   */
  inputSkipped?: number;
}

/**
 * Focus editor: the configuration takes the whole width and the source data
 * and live preview step aside. Remembered per browser, since someone who wants
 * the room for one node tends to want it for the next.
 */
const FOCUS_KEY = 'hermod.transformationForm.focus';

function readFocus(): boolean {
  try {
    return localStorage.getItem(FOCUS_KEY) === '1';
  } catch {
    return false;
  }
}

export function TransformationForm({ selectedNode, updateNodeConfig, onRunSimulation: _onRunSimulation, availableFields = [], incomingPayload, sources = [], sinkSchema, onRefreshFields, isRefreshing, inputSkipped = 0 }: TransformationFormProps) {
  const [testing, setTesting] = useState(false);
  // The last Test API Call's outcome, which the editor keeps on screen: a toast
  // is gone before a refusal's reason can be read.
  const [lastTest, setLastTest] = useState<APILookupTestOutcome | null>(null);
  useEffect(() => {
    setLastTest(null);
  }, [selectedNode?.id]);
  const { fields: targetSchema, loading: loadingTarget, refetch: refetchTarget } = useTargetSchema({ sinkSchema });

  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  const [previewResult, setPreviewResult] = useState<any>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const [configSearch, setConfigSearch] = useState('');
  const [focus, setFocus] = useState(readFocus);
  const toggleFocus = () => {
    const next = !focus;
    setFocus(next);
    try {
      localStorage.setItem(FOCUS_KEY, next ? '1' : '0');
    } catch {}
  };
  // Accessibility: IDs for help modal labelling
  const helpTitleId = 'transformation-help-modal-title';
  const helpDescId = 'transformation-help-modal-desc';

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

  // The vhost of the workflow being edited: whose secrets secret("NAME") reads
  // in a preview, and whose secret names the value fields offer.
  const editorVHost = useWorkflowStore((s) => s.vhost);
  const secretNames = useVHostSecretNames(editorVHost);

  const transType = selectedNode?.data?.transType || selectedNode?.type || '';
  // Field mappings are rows of long paths and JSON values; they get the wider
  // middle column even when the side panels are showing.
  const wideConfig = transType === 'set' || transType === 'advanced';

  // The node's own type, not just transType: a `foreach` node and a
  // `transformation` with transType foreach both compute transType "foreach"
  // above, and the two do opposite things to the message count. Same
  // discriminator ForeachConfig takes as its `nodeType` prop.
  const guide = guideFor(transType, selectedNode?.type);

  const { run: runPreviewRequest } = previewMutation;

  const runPreview = useCallback(async () => {
    if (!incomingPayload) return;
    setTesting(true);
    setPreviewError(null);
    runPreviewRequest(
      {
        transformation: {
          type: transType,
          config: selectedNode.data,
        },
        message: incomingPayload,
        vhost: editorVHost,
      },
      {
        onSuccess: (data: any) => {
          if (data?.error) {
            setPreviewError(data.error);
          } else if (typeof data?.branch === 'string') {
            // A routing node answers with the branch it took and the message
            // untouched. The panel's Result/Diff/Input tabs all expect a
            // message, so hand it the message; the branch is what the Test
            // button reports.
            setPreviewResult(data.result ?? {});
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
  }, [runPreviewRequest, incomingPayload, selectedNode.data, transType, editorVHost]);

  // Schedule off *content*, never off callback identity.
  //
  // The effect below used to list `runPreview` as a dependency. `runPreview`
  // depended on the React Query mutation result, which is a new object on every
  // render, so the effect tore down and re-armed its timer on every render and
  // the 1s debounce ran as a 1s poll — a request per second, forever, with the
  // user idle. Keying on a serialised snapshot means the timer re-arms only when
  // the thing being previewed actually changed, whatever happens to identities.
  const previewKey = useMemo(() => {
    try {
      return JSON.stringify({
        t: transType,
        c: selectedNode?.data ?? null,
        m: incomingPayload ?? null,
      });
    } catch {
      // Cyclic or otherwise unserialisable config: fall back to a key that only
      // changes with the transformation type, so we degrade to fewer previews
      // rather than to an unbounded loop.
      return `unserializable:${transType}`;
    }
  }, [transType, selectedNode?.data, incomingPayload]);

  const latestRunPreview = useRef(runPreview);
  useEffect(() => {
    latestRunPreview.current = runPreview;
  }, [runPreview]);

  useEffect(() => {
    if (!incomingPayload) return;
    const timer = setTimeout(() => {
      latestRunPreview.current();
    }, PREVIEW_DEBOUNCE_MS);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewKey]);

  if (!selectedNode) return null;

  const addField = (path: string = '', value: string = '') => {
    // The generated name has to be one no row holds. It used to be the row
    // count, so deleting a middle row made the next "Add Field" land on a name
    // that was still taken: no row appeared and that row's value was replaced.
    const fieldName = path || nextColumnFieldName(selectedNode.data);
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
      } catch { conditions = []; }
      
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
        // Reported below, once. apiFetch's own toast made a refused request
        // show two.
        silent: true,
        body: JSON.stringify({
          transformation: {
            type: transType,
            config: selectedNode.data
          },
          message: incomingPayload,
          vhost: editorVHost
        })
      });
      const data = await res.json();
      if (data.error) {
        setLastTest({ ok: false, message: data.error });
        notifications.show({ title: 'Test Failed', message: data.error, color: 'orange' });
      } else if (typeof data.branch === 'string') {
        // Routing nodes — switch, condition, router — do not change the
        // message, so there is no target field to report. What the user is
        // testing is which way the message goes.
        notifications.show({
          title: 'Test Success',
          message: `Branch taken: "${data.branch}"`,
          color: 'green',
        });
      } else {
        const result = preparePayload(data);
        const targetField = selectedNode.data.targetField || selectedNode.data.target_field;
        const val = getValByPath(result, targetField);
        const message = `Result for "${targetField}": ${val === undefined ? 'Not Found' : JSON.stringify(val, null, 2)}`;

        setLastTest({ ok: true, message });
        notifications.show({
          title: 'Test Success',
          message: `Result for "${targetField}": ${val === undefined ? 'Not Found' : JSON.stringify(val)}`,
          color: 'green'
        });
      }
    } catch (e: any) {
      setLastTest({ ok: false, message: e.message });
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
            lastTest={lastTest}
            transType={transType}
            nodeType={selectedNode.type}
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
      {!focus && (
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

          <Card withBorder padding="md" radius="md" data-testid="available-fields-panel">
            <Group justify="space-between" mb="sm">
              <Group gap="xs">
                <IconList size="1rem" color="var(--mantine-color-gray-6)" />
                <Text size="xs" fw={700}>AVAILABLE FIELDS</Text>
                {onRefreshFields && (
                  <MantineTooltip label="Refresh sample data and fields" position="right">
                    <ActionIcon aria-label="Refresh sample data and fields" variant="subtle" size="xs" onClick={() => { onRefreshFields(); refetchTarget(); }} color="blue" loading={isRefreshing}>
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

          {/* Both read a row's value as an expression (wideConfig is the
              same pair), so both get the list of what one can call. */}
          {wideConfig && <FunctionLibrary onInsert={onInsertExample} />}

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
      )}

      {/* Column 2: Configuration */}
      <Grid.Col span={focus ? 12 : { base: 12, md: 8, lg: wideConfig ? 6 : 5 }}>
        <Card withBorder shadow="md" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
          <Stack gap="lg" h="100%">
            <Group justify="space-between" px="xs">
              <Group gap="xs">
                <IconSettings size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700}>TRANSFORM LOGIC</Text>
              </Group>
              <Group gap="xs">
                <MantineTooltip label={focus ? 'Show source data and preview' : 'Give the editor the full width'} position="left">
                  <ActionIcon
                    aria-label="Focus editor"
                    aria-pressed={focus}
                    variant={focus ? 'filled' : 'light'}
                    color="blue"
                    onClick={toggleFocus}
                  >
                    {focus ? <IconArrowsMinimize size="1rem" /> : <IconArrowsMaximize size="1rem" />}
                  </ActionIcon>
                </MantineTooltip>
                <MantineTooltip label="How to use this transformation" position="left">
                  <ActionIcon aria-label="Open transformation help" variant="light" color="blue" onClick={() => setHelpOpen(true)}>
                    <IconHelpCircle size="1rem" />
                  </ActionIcon>
                </MantineTooltip>
                {/* The human name, not the raw registry key: "History tracking
                    (SCD)" orients; "SCD" is a password. */}
                <Badge variant="light" color="blue" size="lg">{guide.title}</Badge>
              </Group>
            </Group>

            {/* What this node does and what to do first, where the eyes
                already are — the answer used to live only behind the help
                icon's modal. */}
            {guide.what && (
              <Text size="sm" c="dimmed">
                {guide.what}{' '}
                <Text span size="sm" fw={500} c="var(--mantine-color-text)">
                  {guide.firstStep}
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

            {incomingPayload && (
              <UpstreamNotRunNotice skipped={inputSkipped} onRun={onRefreshFields} running={isRefreshing} />
            )}

            <Divider />

            <FormRow>
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
            </FormRow>

            <Box flex={1} style={{ overflow: 'hidden' }}>
              <ScrollArea h="100%" offsetScrollbars>
                <Stack gap="lg" py="md">
                  <Suspense fallback={null}>
                    <QuickActions onApplyTemplate={handleApplyTemplate} />
                  </Suspense>
                  <Divider label="Configuration" labelPosition="center" />
                  
                  <SecretNamesContext.Provider value={secretNames}>
                    {renderConfiguration()}
                  </SecretNamesContext.Provider>

                  <Divider label="Advanced" labelPosition="center" mt="xl" />


          {/*
            Every transformation type configures itself through the registry
            above (configs/registry.ts). The inline blocks that used to follow
            here — foreach, lua, wasm, aggregate, set, pipeline, advanced — were
            the pre-registry versions and were never deleted, so seven types
            rendered their settings twice: once under Configuration, again under
            Advanced, with stale labels and fewer fields the second time.
          */}

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

      {!focus && (
      <Grid.Col span={{ base: 12, md: 12, lg: wideConfig ? 3 : 4 }}>
        <Suspense fallback={<Text size="sm" c="dimmed">Loading preview…</Text>}>
          <PreviewPanel
            title="3. LIVE PREVIEW"
            loading={testing || (previewMutation as any)?.isPending}
            error={previewError || ((previewMutation as any)?.error?.message ?? null)}
            result={previewResult || (previewMutation as any)?.data}
            original={incomingPayload}
            onRun={runPreview}
            // execute_sql has no targetField; what it produces is the rows its
            // statement returns, under resultField.
            targetField={
              selectedNode.data.targetField ||
              selectedNode.data.target_field ||
              (transType === 'execute_sql' ? selectedNode.data.resultField : undefined)
            }
          />
        </Suspense>
      </Grid.Col>
      )}
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


