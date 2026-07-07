import { useState, useEffect, useCallback, lazy, Suspense, useMemo } from 'react';
import { 
  TextInput, Select, Stack, Alert, Divider, Text, Group, ActionIcon, 
  Button, Code, List, Autocomplete, JsonInput, Badge, Grid, SimpleGrid,
  PasswordInput, NumberInput, Card, Tabs, ScrollArea, Box,
  Tooltip as MantineTooltip,
  Switch, Textarea, Modal, TagsInput
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { usePreviewTransformation } from '../../pages/workflows/WorkflowEditor/hooks/usePreviewTransformation';
import { useTargetSchema } from '../../pages/workflows/WorkflowEditor/hooks/useTargetSchema';
import type { Condition } from '../workflow/Transformation/FilterEditor';
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
const FilterEditor = lazy(() =>
  import('../workflow/Transformation/FilterEditor').then((m) => ({ default: m.FilterEditor }))
);
const SetFieldEditor = lazy(() =>
  import('../workflow/Transformation/SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);
const QuickActions = lazy(() =>
  import('../workflow/Transformation/QuickActions').then((m) => ({ default: m.QuickActions }))
);
import { IconArrowRight, IconCloud, IconCode, IconDatabase, IconFunction, IconHelpCircle, IconInfoCircle, IconList, IconPlayerPlay, IconPlus, IconPuzzle, IconRefresh, IconSearch, IconSettings, IconVariable } from '@tabler/icons-react';
import { preparePayload, getValByPath } from '@/utils/transformationUtils';

// Modular configuration components (Junie compliance)
import { WaitConfig } from '../workflow/Transformation/configs/logic/WaitConfig';
import { ForeachConfig } from '../workflow/Transformation/configs/logic/ForeachConfig';
import { MappingConfig } from '../workflow/Transformation/configs/data/MappingConfig';
import { FilterConfig } from '../workflow/Transformation/configs/data/FilterConfig';
import { LuaConfig } from '../workflow/Transformation/configs/script/LuaConfig';
import { WasmConfig } from '../workflow/Transformation/configs/script/WasmConfig';
import { StatefulConfig } from '../workflow/Transformation/configs/logic/StatefulConfig';
import { ApprovalConfig } from '../workflow/Transformation/configs/logic/ApprovalConfig';
import { ConditionConfig } from '../workflow/Transformation/configs/logic/ConditionConfig';
import { RouterConfig } from '../workflow/Transformation/configs/logic/RouterConfig';
import { SwitchConfig } from '../workflow/Transformation/configs/logic/SwitchConfig';
import { AggregateConfig } from '../workflow/Transformation/configs/data/AggregateConfig';
import { SetFieldsConfig } from '../workflow/Transformation/configs/data/SetFieldsConfig';
import { LookupConfig } from '../workflow/Transformation/configs/enrichment/LookupConfig';
import { PipelineConfig } from '../workflow/Transformation/configs/data/PipelineConfig';
import { AdvancedConfig } from '../workflow/Transformation/configs/data/AdvancedConfig';
import { ValidatorConfig } from '../workflow/Transformation/configs/data/ValidatorConfig';
import { MaskConfig } from '../workflow/Transformation/configs/data/MaskConfig';
import { RateLimitConfig } from '../workflow/Transformation/configs/logic/RateLimitConfig';
import { SQLConfig } from '../workflow/Transformation/configs/enrichment/SQLConfig';
import { DBLookupConfig } from '../workflow/Transformation/configs/enrichment/DBLookupConfig';
import { MulticastConfig } from '../workflow/Transformation/configs/logic/MulticastConfig';
import { LogConfig } from '../workflow/Transformation/configs/logic/LogConfig';
import { CollectConfig } from '../workflow/Transformation/configs/logic/CollectConfig';
import { DeduplicateConfig } from '../workflow/Transformation/configs/util/DeduplicateConfig';

const JoinConfig = lazy(() => import('../workflow/Transformation/configs/logic/JoinConfig').then(m => ({ default: m.JoinConfig })));
const CircuitBreakerConfig = lazy(() => import('../workflow/Transformation/configs/logic/CircuitBreakerConfig').then(m => ({ default: m.CircuitBreakerConfig })));
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

  const renderStatValidatorEditor = () => {
    return (
      <Stack gap="sm">
        <Text size="sm" fw={500}>Statistical Validation Settings</Text>
        <Grid>
          <Grid.Col span={6}>
            <Autocomplete
              label="Field to Validate"
              description="Numeric field to monitor for anomalies"
              placeholder="e.g. price, amount, latency"
              data={fieldPaths || []}
              value={selectedNode.data.field || ''}
              onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })}
            />
          </Grid.Col>
          <Grid.Col span={6}>
            <Select
              label="On Anomaly Detected"
              description="Action to take when an outlier is found"
              data={[
                { value: 'tag', label: 'Tag only (set metadata anomaly=true)' },
                { value: 'drop', label: 'Drop message (stop processing)' }
              ]}
              value={selectedNode.data.action || 'tag'}
              onChange={(val) => updateNodeConfig(selectedNode.id, { action: val || 'tag' })}
            />
          </Grid.Col>
          <Grid.Col span={6}>
            <NumberInput
              label="Z-Score Threshold"
              description="Number of standard deviations from mean"
              min={1}
              max={10}
              step={0.1}
              decimalScale={1}
              value={Number(selectedNode.data.threshold) || 3.0}
              onChange={(val) => updateNodeConfig(selectedNode.id, { threshold: val })}
            />
          </Grid.Col>
          <Grid.Col span={6}>
            <NumberInput
              label="Minimum Samples"
              description="Samples needed before triggering validation"
              min={1}
              value={Number(selectedNode.data.min_samples) || 10}
              onChange={(val) => updateNodeConfig(selectedNode.id, { min_samples: val })}
            />
          </Grid.Col>
        </Grid>
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          Uses Welford's online algorithm for stable rolling mean and standard deviation.
        </Alert>
      </Stack>
    );
  };








  // (Legacy inline help removed — replaced by lazy HelpContent component)

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
      <Card withBorder padding="xs" radius="md">
        <Group gap="xs" mb="xs">
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
          <Stack gap={4}>
            {filtered.map(f => (
              <Box key={f.name} p={6} style={{ borderRadius: 4, background: 'var(--mantine-color-orange-light)', border: '1px solid var(--mantine-color-orange-light-color)', cursor: 'pointer' }} onClick={() => onInsertExample(f.example)}>
                <Group justify="space-between">
                  <Text size="xs" fw={700} c="var(--mantine-color-orange-light-color)">{f.name}</Text>
                  <ActionIcon size="xs" variant="subtle" color="orange">
                    <IconPlus size="0.8rem" />
                  </ActionIcon>
                </Group>
                <Text size="10px" c="dimmed">{f.desc}</Text>
                <Code mt={2} style={{ fontSize: '10px' }}>{f.example}</Code>
              </Box>
            ))}
          </Stack>
        </ScrollArea>
      </Card>
    );
  };


  const renderCharMapEditor = () => (
    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="user.name"
        data={fieldPaths || []}
        value={selectedNode.data.field || ''}
        onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })}
        required
      />
      <Select
        label="Operation"
        data={[
          { value: 'uppercase', label: 'UPPERCASE' },
          { value: 'lowercase', label: 'lowercase' },
          { value: 'trim', label: 'Trim whitespace' },
          { value: 'trim_left', label: 'Trim Left' },
          { value: 'trim_right', label: 'Trim Right' },
        ]}
        value={selectedNode.data.op || 'uppercase'}
        onChange={(val) => updateNodeConfig(selectedNode.id, { op: val || 'uppercase' })}
      />
    </Stack>
  );

  const renderDataConversionEditor = () => (
    <Stack gap="xs">
      <Autocomplete
        label="Field"
        placeholder="amount"
        data={fieldPaths || []}
        value={selectedNode.data.field || ''}
        onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })}
        required
        description="Field or expression to convert (e.g. amount, lower(source.status))."
      />
      <Select
        label="Target Type"
        data={[
          { value: 'int', label: 'Integer' },
          { value: 'float', label: 'Float' },
          { value: 'string', label: 'String' },
          { value: 'bool', label: 'Boolean' },
          { value: 'date', label: 'Date' },
        ]}
        value={selectedNode.data.targetType || 'string'}
        onChange={(val) => updateNodeConfig(selectedNode.id, { targetType: val || 'string' })}
      />
      {selectedNode.data.targetType === 'date' && (
        <TextInput
          label="Date Format"
          placeholder="2006-01-02"
          value={selectedNode.data.format || ''}
          onChange={(e) => updateNodeConfig(selectedNode.id, { format: e.currentTarget.value })}
          description="Go date format (e.g. 2006-01-02)"
        />
      )}
      <Select
        label="On Error"
        description="How to handle values that cannot be converted to the target type."
        data={[
          { value: 'fail', label: 'Fail (Error output)' },
          { value: 'null', label: 'Set to NULL' },
          { value: 'keep', label: 'Keep original' },
        ]}
        value={selectedNode.data.errorBehavior || 'fail'}
        onChange={(val) => updateNodeConfig(selectedNode.id, { errorBehavior: val || 'fail' })}
      />
      <TextInput
        label="Target Field (Optional)"
        placeholder="Defaults to source field"
        value={selectedNode.data.targetField || ''}
        onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.currentTarget.value })}
      />
    </Stack>
  );

  const renderSamplingEditor = () => (
    <Stack gap="xs">
      <Select
        label="Sampling Type"
        data={[
          { value: 'percentage', label: 'Percentage (%)' },
          { value: 'row', label: 'Nth Row (Every N)' },
        ]}
        value={selectedNode.data.type || 'percentage'}
        onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { type: val || 'percentage' })}
      />
      <NumberInput
        label={selectedNode.data.type === 'row' ? 'Every Nth Row' : 'Percentage (0-100)'}
        value={selectedNode.data.value || 10}
        min={0.00001}
        max={selectedNode.data.type === 'row' ? undefined : 100}
        onChange={(val: string | number | undefined) => updateNodeConfig(selectedNode.id, { value: val })}
      />
      {selectedNode.data.type === 'row' && (
        <NumberInput
          label="Limit (Optional)"
          placeholder="Max rows to emit"
          value={selectedNode.data.limit || 0}
          onChange={(val: string | number | undefined) => updateNodeConfig(selectedNode.id, { limit: val })}
        />
      )}
    </Stack>
  );

  const renderFuzzyLookupEditor = () => (
    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="input_name"
        data={fieldPaths || []}
        value={selectedNode.data.field || ''}
        onChange={(val: string) => updateNodeConfig(selectedNode.id, { field: val })}
        required
        description="Field or expression to use for matching (e.g. name, lower(source.name))."
      />
      <NumberInput
        label="Similarity Threshold (0-1)"
        value={selectedNode.data.threshold || 0.8}
        min={0}
        max={1}
        step={0.05}
        onChange={(val: string | number | undefined) => updateNodeConfig(selectedNode.id, { threshold: val })}
      />
      <JsonInput
        label="Options (JSON Array)"
        placeholder='["Option 1", "Option 2"]'
        value={selectedNode.data.options || ''}
        onChange={(val: string) => updateNodeConfig(selectedNode.id, { options: val })}
        minRows={5}
        formatOnBlur
      />
    </Stack>
  );

  const renderTermExtractionEditor = () => (
    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="description"
        data={fieldPaths || []}
        value={selectedNode.data.field || ''}
        onChange={(val: string) => updateNodeConfig(selectedNode.id, { field: val })}
        required
        description="Field or expression to extract terms from (e.g. description, tostring(source.id))."
      />
      <TextInput
        label="Target Field"
        placeholder="keywords"
        value={selectedNode.data.targetField || 'keywords'}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { targetField: e.currentTarget.value })}
      />
      <NumberInput
        label="Min Word Length"
        value={selectedNode.data.minLength || 3}
        min={1}
        onChange={(val: string | number | undefined) => updateNodeConfig(selectedNode.id, { minLength: val })}
      />
      <TagsInput
        label="Stopwords"
        placeholder="Add words to ignore"
        value={typeof selectedNode.data.stopWords === 'string' ? selectedNode.data.stopWords.split(',') : (selectedNode.data.stopWords || [])}
        onChange={(val: string[]) => updateNodeConfig(selectedNode.id, { stopWords: val.join(',') })}
      />
    </Stack>
  );

  const renderUnpivotEditor = () => (
    <Stack gap="xs">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="About Unpivot">
        <Text size="xs">
          The Unpivot transformation rotates columns into attribute/value rows.
          Use it to convert wide-format data into long-format.
        </Text>
        <Text size="xs" mt="xs" fw={700}>Example:</Text>
        <Code block mt={5}>
          {`// Before:
{ "id": 1, "temp": 22, "hum": 45 }

// After (unpivoted):
[
  { "id": 1, "attribute": "temp", "value": 22 },
  { "id": 1, "attribute": "hum",  "value": 45 }
]`}
        </Code>
      </Alert>

      <TagsInput
        label="Columns to Unpivot"
        placeholder="e.g. Jan, Feb, Mar"
        description="The columns you want to turn into rows"
        value={selectedNode.data.pivotColumns || []}
        onChange={(val) => updateNodeConfig(selectedNode.id, { pivotColumns: val })}
        required
      />

      <TextInput
        label="Attribute Field"
        placeholder="attribute"
        description="Name of the field that will store the column name"
        value={selectedNode.data.attributeField || 'attribute'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { attributeField: e.currentTarget.value })}
      />

      <TextInput
        label="Value Field"
        placeholder="value"
        description="Name of the field that will store the column value"
        value={selectedNode.data.valueField || 'value'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { valueField: e.currentTarget.value })}
      />

      <TextInput
        label="Target Field"
        placeholder="_fanout"
        description="The field where the resulting array will be stored"
        value={selectedNode.data.resultField || '_fanout'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { resultField: e.currentTarget.value })}
      />
    </Stack>
  );

  const renderPivotEditor = () => (
    <Stack gap="xs">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="About Pivot">
        <Text size="xs">
          The Pivot transformation rotates attribute/value rows into columns. 
          Use it to convert long-format data into wide-format.
        </Text>
        <Text size="xs" mt="xs" fw={700}>Example:</Text>
        <Code block mt={5}>
          {`// Before:
{ "id": 1, "attr": "temp", "val": 22 }
{ "id": 1, "attr": "hum",  "val": 45 }

// After (pivoted):
{ "id": 1, "temp": 22, "hum": 45 }`}
        </Code>
      </Alert>

      <TagsInput
        label="Index Keys"
        placeholder="e.g. id, branch_id"
        description="Fields used to identify unique groups of data"
        value={Array.isArray(selectedNode.data.indexKeys) ? selectedNode.data.indexKeys : (selectedNode.data.indexKeys?.split(',').filter(Boolean) || [])}
        onChange={(val) => updateNodeConfig(selectedNode.id, { indexKeys: val })}
        required
      />

      <TextInput
        label="Attribute Field"
        placeholder="attribute"
        description="The field containing the name of the new column"
        value={selectedNode.data.attributeField || 'attribute'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { attributeField: e.currentTarget.value })}
        required
      />

      <TextInput
        label="Value Field"
        placeholder="value"
        description="The field containing the value for the new column"
        value={selectedNode.data.valueField || 'value'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { valueField: e.currentTarget.value })}
        required
      />

      <Select
        label="Aggregation Strategy"
        description="How to handle multiple values for the same attribute and index keys"
        data={[
          { value: 'first', label: 'First (Keep first encountered)' }, 
          { value: 'concat', label: 'Concat (Join values as string)' }
        ]}
        value={selectedNode.data.strategy || 'first'}
        onChange={(val) => updateNodeConfig(selectedNode.id, { strategy: val || 'first' })}
      />

      <TextInput
        label="Target Field"
        placeholder="Leave empty to merge into root"
        description="Optional: Nest the pivoted data under this field"
        value={selectedNode.data.targetField || ''}
        onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.currentTarget.value })}
      />
    </Stack>
  );


  const renderRowCountEditor = () => (
    <Stack gap="xs">
      <TextInput
        label="Target Field"
        placeholder="total_count"
        value={selectedNode.data.targetField || 'total_count'}
        onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.currentTarget.value })}
      />
      <NumberInput
        label="Increment"
        value={selectedNode.data.increment || 1}
        onChange={(val) => updateNodeConfig(selectedNode.id, { increment: val })}
      />
      <Switch
        label="Persistent State"
        checked={selectedNode.data.persistent !== false}
        onChange={(e) => updateNodeConfig(selectedNode.id, { persistent: e.currentTarget.checked })}
        description="Save count across workflow restarts"
      />
    </Stack>
  );


  const renderSCDEditor = () => (
    <Stack gap="xs">
      <Select
        label="Target Source"
        data={(Array.isArray(sources) ? sources : [])
          .filter(s => ['postgres', 'mysql', 'mariadb', 'sqlite', 'mssql', 'sqlserver'].includes(s.type))
          .map((s: any) => ({ value: s.id, label: s.name }))}
        value={selectedNode.data.targetSourceId || ''}
        onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { targetSourceId: val || '' })}
        placeholder="Select a database source"
        required
      />
      <TextInput
        label="Target Table"
        placeholder="e.g. dim_users"
        value={selectedNode.data.targetTable || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { targetTable: e.currentTarget.value })}
        required
      />
      <Select
        label="SCD Type"
        data={[
          { value: '0', label: 'Type 0 (Fixed/Retain Original)' },
          { value: '1', label: 'Type 1 (Overwrite)' },
          { value: '2', label: 'Type 2 (History/Add Row)' },
          { value: '3', label: 'Type 3 (Previous Value Column)' },
          { value: '4', label: 'Type 4 (History Table)' },
          { value: '6', label: 'Type 6 (Hybrid 1+2)' },
        ]}
        value={selectedNode.data.type || '1'}
        onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { type: val || '1' })}
      />
      <TextInput
        label="Business Keys (Comma separated)"
        placeholder="id,email"
        value={selectedNode.data.keys || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { keys: e.currentTarget.value })}
        required
      />
      <TextInput
        label="Monitored Columns (Comma separated)"
        placeholder="name,address,phone"
        value={selectedNode.data.columns || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { columns: e.currentTarget.value })}
        description="Columns to check for changes"
      />
      {selectedNode.data.type === '3' && (
        <TextInput
          label="Column Mappings"
          placeholder="current:previous,email:old_email"
          value={selectedNode.data.mappings || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { mappings: e.currentTarget.value })}
          description="Mapping of current columns to their historical counterparts"
        />
      )}
      {selectedNode.data.type === '4' && (
        <TextInput
          label="History Table"
          placeholder="e.g. dim_users_history"
          value={selectedNode.data.historyTable || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { historyTable: e.currentTarget.value })}
          required
        />
      )}
      {selectedNode.data.type === '6' && (
        <>
          <TextInput
            label="Type 1 Columns (Overwrite)"
            placeholder="email,phone"
            value={selectedNode.data.type1Columns || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { type1Columns: e.currentTarget.value })}
            description="Columns that should be overwritten in all history rows"
          />
          <TextInput
            label="Type 2 Columns (Add Row)"
            placeholder="address,department"
            value={selectedNode.data.type2Columns || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { type2Columns: e.currentTarget.value })}
            description="Columns that trigger a new history row"
          />
        </>
      )}
      {(selectedNode.data.type === '2' || selectedNode.data.type === '6') && (
        <>
          <TextInput
            label="Start Date Column"
            placeholder="start_date"
            value={selectedNode.data.startDateColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { startDateColumn: e.currentTarget.value })}
          />
          <TextInput
            label="End Date Column"
            placeholder="end_date"
            value={selectedNode.data.endDateColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { endDateColumn: e.currentTarget.value })}
          />
          <TextInput
            label="Current Flag Column (Optional)"
            placeholder="is_current"
            value={selectedNode.data.currentFlagColumn || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { currentFlagColumn: e.currentTarget.value })}
          />
        </>
      )}
    </Stack>
  );

  const renderAuditEditor = () => (
    <Stack gap="xs">
      <TextInput
        label="Prefix"
        placeholder="audit_"
        value={selectedNode.data.prefix || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { prefix: e.currentTarget.value })}
        description="Optional prefix for injected metadata fields (e.g. audit_workflow_id)"
      />
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        This node injects: workflow_id, node_id, machine_name, timestamp, and message_id.
      </Alert>
    </Stack>
  );

  const renderJoinEditor = () => (
    <Stack gap="xs">
      <Select
        label="Join Mode"
        data={[
          { label: 'Store (Save current record to state)', value: 'store' },
          { label: 'Lookup (Enrich from state)', value: 'lookup' },
        ]}
        value={selectedNode.data.mode || 'lookup'}
        onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { mode: val || 'lookup' })}
      />
      <TextInput
        label="Join Key (Message Path)"
        placeholder="e.g. order_id"
        value={selectedNode.data.key || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { key: e.target.value })}
        description="Field in the current message used to match records."
      />
      <TextInput
        label="Storage Namespace"
        placeholder="default"
        value={selectedNode.data.namespace || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { namespace: e.target.value })}
        description="Use namespaces to separate different join datasets."
      />
      {selectedNode.data.mode === 'lookup' && (
        <>
          <TextInput
            label="Joined Field Prefix"
            placeholder="joined_"
            value={selectedNode.data.prefix || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { prefix: e.target.value })}
          />
          <TagsInput
            label="Specific Fields to Extract"
            placeholder="Leave empty for all fields"
            value={selectedNode.data.fields || []}
            onChange={(val: string[]) => updateNodeConfig(selectedNode.id, { fields: val })}
          />
        </>
      )}
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo" py="xs" mt="md">
        <Text size="xs">Enrich messages by joining them with data previously 'Stored' by other messages sharing the same key.</Text>
      </Alert>
    </Stack>
  );

  const renderFilterDataEditor = () => {
    let conditions: Condition[] = []
    try {
      conditions = typeof selectedNode.data.conditions === 'string'
        ? JSON.parse(selectedNode.data.conditions || '[]')
        : (selectedNode.data.conditions || [])
    } catch {
      conditions = []
    }
    if (conditions.length === 0 && selectedNode.data.field) {
      conditions.push({
        field: selectedNode.data.field,
        operator: selectedNode.data.operator || '=',
        value: selectedNode.data.value || '',
      })
    }
    return (
      <Stack gap="xs">
        <Box mb="md" p="xs" style={{ border: '1px dashed var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}>
          <Switch 
            label="Set result as boolean field instead of filtering" 
            checked={!!selectedNode.data.asField || transType === 'validate'}
            disabled={transType === 'validate'}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { asField: e.currentTarget.checked })}
            mb={selectedNode.data.asField || transType === 'validate' ? 'xs' : 0}
          />
          {(selectedNode.data.asField || transType === 'validate') && (
            <TextInput 
              label="Target Field Name"
              placeholder="e.g. is_valid"
              value={selectedNode.data.targetField || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
              size="xs"
            />
          )}
        </Box>
        <Suspense fallback={null}>
          <FilterEditor
            conditions={conditions}
            availableFields={availableFields}
            onChange={(next: Condition[]) =>
              updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) })
            }
          />
        </Suspense>
        <Alert icon={<IconInfoCircle size="1rem" />} color={transType === 'condition' ? 'yellow' : 'violet'} py="xs" mt="md">
          <Stack gap={4}>
            <Text size="xs">
              {transType === 'condition' 
                ? 'Conditions branch the flow. Use "true" and "false" labels on outgoing edges.' 
                : 'Filters will stop the message if the condition is not met.'}
            </Text>
          </Stack>
        </Alert>
      </Stack>
    );
  };


  const renderAPILookupEditor = () => (
    <Tabs defaultValue="endpoint">
      <Tabs.List mb="md">
        <Tabs.Tab value="endpoint" leftSection={<IconCloud size="1rem" />}>Endpoint</Tabs.Tab>
        <Tabs.Tab value="payload" leftSection={<IconCode size="1rem" />}>Body/Headers</Tabs.Tab>
        <Tabs.Tab value="settings" leftSection={<IconSettings size="1rem" />}>Auth/Retry</Tabs.Tab>
      </Tabs.List>

      <Tabs.Panel value="endpoint">
        <Stack gap="sm">
          <Group grow>
            <Select
              label="Method"
              data={['GET', 'POST', 'PUT', 'DELETE', 'PATCH']}
              value={selectedNode.data.method || 'GET'}
              onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { method: val || 'GET' })}
            />
            <TextInput
              label="Target Field (Message)"
              placeholder="e.g. enriched_data"
              value={selectedNode.data.targetField || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
            />
          </Group>
          <TextInput
            label="URL"
            placeholder="https://api.example.com/v1/users/{{user_id}}"
            value={selectedNode.data.url || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { url: e.target.value })}
          />
          <TextInput
            label="Response JSON Path"
            placeholder="e.g. data.profile.name (Use '.' for root)"
            value={selectedNode.data.responsePath || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { responsePath: e.target.value })}
          />
          <Button 
            variant="light" 
            color="orange" 
            mt="xs"
            leftSection={<IconPlayerPlay size="0.8rem" />}
            onClick={testLookup}
            loading={testing}
          >
            Test API Call
          </Button>
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="payload">
        <Stack gap="sm">
          <JsonInput
            label="Headers (JSON)"
            placeholder='{"Authorization": "Bearer {{token}}", "X-Api-Key": "secret"}'
            value={selectedNode.data.headers || ''}
            onChange={(val: string) => updateNodeConfig(selectedNode.id, { headers: val })}
            formatOnBlur
            minRows={4}
          />
          <JsonInput
            label="Query Params (JSON)"
            placeholder='{"id": "{{id}}", "ref": "hermod"}'
            value={selectedNode.data.queryParams || ''}
            onChange={(val: string) => updateNodeConfig(selectedNode.id, { queryParams: val })}
            formatOnBlur
            minRows={4}
          />
          {selectedNode.data.method !== 'GET' && (
            <JsonInput
              label="Request Body (JSON)"
              placeholder='{"id": "{{user_id}}", "query": "..."}'
              value={selectedNode.data.body || ''}
              onChange={(val: string) => updateNodeConfig(selectedNode.id, { body: val })}
              formatOnBlur
              minRows={6}
            />
          )}
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="settings">
        <Stack gap="sm">
          <Select
            label="Auth Type"
            data={[{label: 'None', value: ''}, {label: 'Basic', value: 'basic'}, {label: 'Bearer', value: 'bearer'}]}
            value={selectedNode.data.authType || ''}
            onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { authType: val || '' })}
          />
          {selectedNode.data.authType === 'basic' && (
            <Group grow>
              <TextInput
                label="Username"
                value={selectedNode.data.username || ''}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { username: e.target.value })}
              />
              <PasswordInput
                label="Password"
                value={selectedNode.data.password || ''}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { password: e.target.value })}
              />
            </Group>
          )}
          {selectedNode.data.authType === 'bearer' && (
            <PasswordInput
              label="Token"
              value={selectedNode.data.token || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { token: e.target.value })}
            />
          )}
          <Group grow>
            <TextInput
              label="Default Value"
              placeholder="Value if lookup fails"
              value={selectedNode.data.defaultValue || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { defaultValue: e.target.value })}
              description="Used if API call fails or returns no data."
            />
            <TextInput
              label="Timeout"
              placeholder="10s"
              value={selectedNode.data.timeout || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { timeout: e.target.value })}
            />
          </Group>
          <Group grow>
            <TextInput
               label="Cache TTL"
               placeholder="e.g. 5m, 1h"
               value={selectedNode.data.ttl || ''}
               onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { ttl: e.target.value })}
            />
            <NumberInput
              label="Max Retries"
              value={selectedNode.data.maxRetries || 0}
              onChange={(val: string | number | undefined) => updateNodeConfig(selectedNode.id, { maxRetries: val })}
            />
          </Group>
          <TextInput
            label="Retry Delay"
            placeholder="1s"
            value={selectedNode.data.retryDelay || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { retryDelay: e.target.value })}
          />
        </Stack>
      </Tabs.Panel>
    </Tabs>
  );

  const renderAIEditor = () => (
    <Stack gap="md">
      <Select
        label="Provider"
        data={[
          { value: 'openai', label: 'OpenAI' },
          { value: 'ollama', label: 'Ollama (Local)' },
        ]}
        value={selectedNode.data.provider || 'openai'}
        onChange={(val: string | null) => updateNodeConfig(selectedNode.id, { provider: val || 'openai' })}
      />
      <TextInput
        label="Model"
        placeholder={selectedNode.data.provider === 'ollama' ? 'llama3' : 'gpt-4o'}
        value={selectedNode.data.model || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { model: e.target.value })}
      />
      <Textarea
        label="Prompt Template"
        placeholder="Extract the sentiment and primary topic from: {{message}}"
        value={selectedNode.data.prompt || ''}
        onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => updateNodeConfig(selectedNode.id, { prompt: e.target.value })}
        minRows={4}
        description="Use {{message}} to refer to the entire message, or {{field}} for specific fields."
      />
      <TextInput
        label="Target Field"
        placeholder="ai_analysis"
        value={selectedNode.data.targetField || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
      />
      {selectedNode.data.provider === 'openai' && (
        <PasswordInput
          label="API Key"
          placeholder="sk-..."
          value={selectedNode.data.apiKey || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { apiKey: e.target.value })}
        />
      )}
      {selectedNode.data.provider === 'ollama' && (
        <TextInput
          label="Ollama Host"
          placeholder="http://localhost:11434"
          value={selectedNode.data.host || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(selectedNode.id, { host: e.target.value })}
        />
      )}
    </Stack>
  );

  const renderPathHelp = () => (
    <Card withBorder padding="xs" radius="md">
      <Group gap="xs" mb={4}>
        <IconInfoCircle size="1rem" color="var(--mantine-color-blue-filled)" />
        <Text size="xs" fw={700}>Data Access Guide</Text>
      </Group>
      <Stack gap={4}>
        <Text size="xs">• Nested: <Code>user.profile.name</Code></Text>
        <Text size="xs">• Arrays: <Code>items.0.id</Code></Text>
        <Text size="xs">• Reference: prefix with <Code>source.</Code></Text>
      </Stack>
    </Card>
  );

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

    // Specific Node Type handling
    if (selectedNode.type === 'wait') return <WaitConfig {...commonProps} />;
    if (selectedNode.type === 'join') return <Suspense fallback={null}><JoinConfig {...commonProps} /></Suspense>;
    if (selectedNode.type === 'circuit_breaker') return <Suspense fallback={null}><CircuitBreakerConfig {...commonProps} /></Suspense>;
    if (selectedNode.type === 'foreach') return <ForeachConfig {...commonProps} />;
    if (selectedNode.type === 'collect') return <CollectConfig {...commonProps} />;
    if (selectedNode.type === 'log') return <LogConfig {...commonProps} />;
    if (selectedNode.type === 'deduplicate') return <DeduplicateConfig {...commonProps} />;
    if (selectedNode.type === 'approval') return <ApprovalConfig {...commonProps} />;
    if (selectedNode.type === 'stateful') return <StatefulConfig {...commonProps} />;
    if (selectedNode.type === 'condition') return <ConditionConfig {...commonProps} />;
    if (selectedNode.type === 'router') return <RouterConfig {...commonProps} />;
    if (selectedNode.type === 'switch') return <SwitchConfig {...commonProps} />;
    if (selectedNode.type === 'merge') {
      return (
        <Alert icon={<IconInfoCircle size="1rem" />} color="cyan">
          <Text size="sm">Merge nodes join parallel paths by waiting for all incoming branches.</Text>
        </Alert>
      );
    }

    // Transformation Sub-types
    switch (transType) {
      case 'mapping': return <MappingConfig {...commonProps} />;
      case 'filter': return <FilterConfig {...commonProps} />;
      case 'lua': return <LuaConfig {...commonProps} />;
      case 'wasm': return <WasmConfig {...commonProps} />;
      case 'api_lookup': return renderAPILookupEditor();
      case 'ai_enrichment':
      case 'ai_mapper':
        return renderAIEditor();
      case 'aggregate': return <AggregateConfig {...commonProps} />;
      case 'set': return <SetFieldsConfig {...commonProps} onAddFromSource={addFromSource} addField={addField} />;
      case 'lookup': return <LookupConfig {...commonProps} />;
      case 'db_lookup': return <DBLookupConfig {...commonProps} />;
      case 'pipeline': return <PipelineConfig {...commonProps} />;
      case 'validator': return <ValidatorConfig {...commonProps} />;
      case 'mask': return <MaskConfig {...commonProps} />;
      case 'rate_limit': return <RateLimitConfig {...commonProps} />;
      case 'execute_sql': return <SQLConfig {...commonProps} />;
      case 'row_count': return renderRowCountEditor();
      case 'scd': return renderSCDEditor();
      case 'multicast': return <MulticastConfig {...commonProps} />;
      case 'advanced': return <AdvancedConfig {...commonProps} 
                                onAddFromSource={addFromSource} 
                                addField={addField} 
                                transType={transType} />;
      case 'audit': return renderAuditEditor();
      case 'char_map': return renderCharMapEditor();
      case 'data_conversion': return renderDataConversionEditor();
      case 'sampling': return renderSamplingEditor();
      case 'fuzzy_lookup': return renderFuzzyLookupEditor();
      case 'term_extraction': return renderTermExtractionEditor();
      case 'unpivot': return renderUnpivotEditor();
      case 'pivot': return renderPivotEditor();
      case 'join': return renderJoinEditor();
      case 'filter_data':
      case 'condition':
      case 'validate':
        return renderFilterDataEditor();
      case 'stat_validator': return renderStatValidatorEditor();
      default: return (
        <Alert color="gray">
          <Text size="sm">Select a transformation type to begin configuration.</Text>
        </Alert>
      );
    }
  };

  return (
    <>
    <Grid gap="lg" style={{ minHeight: 'calc(100vh - 180px)' }}>
      {/* Column 1: Source Data */}
      <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
        <Stack gap="md" h="100%">
          <Group justify="space-between" px="xs">
            <Group gap="xs">
               <IconDatabase size="1.2rem" color="var(--mantine-color-blue-6)" />
               <Text size="sm" fw={700}>SOURCE DATA</Text>
            </Group>
            <Badge variant="dot" size="sm">INPUT</Badge>
          </Group>
          
          {renderPathHelp()}

          <Card withBorder padding="xs" radius="md">
            <Group justify="space-between" mb="xs">
              <Group gap="xs">
                <IconList size="1rem" color="var(--mantine-color-gray-6)" />
                <Text size="xs" fw={700}>AVAILABLE FIELDS</Text>
                {onRefreshFields && (
                  <MantineTooltip label="Refresh sample data and fields" position="right">
                    <ActionIcon variant="subtle" size="xs" onClick={() => { onRefreshFields(); refetchTarget(); }} color="blue" loading={isRefreshing}>
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

          <Card withBorder padding="xs" radius="md">
            <Group justify="space-between" mb="xs">
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

          <Card withBorder padding="xs" radius="md" bg="var(--mantine-color-body)">
             <Group gap="xs" mb={4}>
                <IconCode size="1rem" color="dimmed" />
                <Text size="10px" fw={700} c="dimmed">RAW PAYLOAD</Text>
             </Group>
             <ScrollArea.Autosize mah={300}>
                <Code block style={{ fontSize: '10px' }}>
                   {incomingPayload ? JSON.stringify(incomingPayload, null, 2) : 'No input sample available'}
                </Code>
             </ScrollArea.Autosize>
          </Card>
        </Stack>
      </Grid.Col>

      {/* Column 2: Configuration */}
      <Grid.Col span={{ base: 12, md: 8, lg: 5 }}>
        <Card withBorder shadow="md" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
          <Stack gap="md" h="100%">
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
                <Badge variant="light" color="blue" size="lg" style={{ textTransform: 'uppercase' }}>{transType}</Badge>
              </Group>
            </Group>

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
                <Stack gap="md" py="xs">
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
                <Stack gap={4}>
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
                <Stack gap={4}>
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


