import { useState, useEffect, useCallback, lazy, Suspense } from 'react';
import { 
  TextInput, Select, Stack, Alert, Divider, Text, Group, ActionIcon, 
  Button, Code, List, Autocomplete, JsonInput, Badge, Grid,
  PasswordInput, NumberInput, Card, Tabs, ScrollArea, Box,
  Tooltip as MantineTooltip,
  Switch, Textarea, Modal, TagsInput
} from '@mantine/core';
import { 
  IconTrash, IconPlus, IconInfoCircle, IconArrowRight, IconPlayerPlay,
  IconSearch, IconFunction,
  IconVariable, IconDatabase, IconCloud, IconList,
  IconSettings, IconCode, IconBracketsContain, IconHelpCircle, IconPuzzle
} from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '../api';
import { usePreviewTransformation } from '../pages/WorkflowEditor/hooks/usePreviewTransformation';
import { getValByPath } from '../utils/transformationUtils';
import { useTargetSchema } from '../pages/WorkflowEditor/hooks/useTargetSchema';
import { PreviewPanel } from './Transformation/PreviewPanel';
import { FieldExplorer } from './Transformation/FieldExplorer';
import { TargetExplorer } from './Transformation/TargetExplorer';
import { FilterEditor, type Condition } from './Transformation/FilterEditor';

interface TransformationFormProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  onRunSimulation?: (payload?: any) => void;
  availableFields: string[];
  incomingPayload?: any;
  sources?: any[];
  sinkSchema?: any;
}

export function TransformationForm({ selectedNode, updateNodeConfig, onRunSimulation: _onRunSimulation, availableFields, incomingPayload, sources, sinkSchema }: TransformationFormProps) {
  const [testing, setTesting] = useState(false);
  const { fields: targetSchema, loading: loadingTarget } = useTargetSchema({ sinkSchema });

  const [previewResult, setPreviewResult] = useState<any>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  // Accessibility: IDs for help modal labelling
  const helpTitleId = 'transformation-help-modal-title';
  const helpDescId = 'transformation-help-modal-desc';

  // Lazy-load heavy help content to reduce initial bundle
  const HelpContent = lazy(() => import('./Transformation/HelpContent'));

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
        notifications.show({ 
          title: 'Test Success', 
          message: `Result for "${selectedNode.data.targetField}": ${JSON.stringify(data[selectedNode.data.targetField])}`, 
          color: 'green' 
        });
      }
    } catch (e: any) {
      notifications.show({ title: 'Error', message: e.message, color: 'red' });
    } finally {
      setTesting(false);
    }
  };

  // Helper for Value Mapping
  const renderMappingEditor = () => {
    let mapping: Record<string, any> = {};
    try {
      mapping = JSON.parse(selectedNode.data.mapping || '{}');
    } catch (e) {
      return <Text size="xs" c="red">Invalid JSON mapping. Use the raw editor below to fix.</Text>;
    }

    const mappingEntries = Object.entries(mapping);

    const updateKey = (oldKey: string, newKey: string) => {
      const next = { ...mapping };
      const value = next[oldKey];
      delete next[oldKey];
      next[newKey] = value;
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const updateValue = (key: string, newValue: any) => {
      const next = { ...mapping };
      next[key] = newValue;
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const removeEntry = (key: string) => {
      const next = { ...mapping };
      delete next[key];
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const addEntry = () => {
      const next = { ...mapping };
      next[`new_key_${mappingEntries.length}`] = '';
      updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
    };

    const addCurrentValue = () => {
      if (!incomingPayload || !selectedNode.data.field) return;
      const val = getValByPath(incomingPayload, selectedNode.data.field);
      if (val === undefined) return;
      const key = String(val);
      const next = { ...mapping };
      if (next[key] === undefined) {
        next[key] = '';
        updateNodeConfig(selectedNode.id, { mapping: JSON.stringify(next) });
      }
    };

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={500}>Value Mapping Rules</Text>
          {incomingPayload && selectedNode.data.field && (
             <Button 
               size="compact-xs" 
               variant="subtle" 
               color="orange"
               leftSection={<IconPlus size="0.8rem" />}
               onClick={addCurrentValue}
             >
               Add current: {String(getValByPath(incomingPayload, selectedNode.data.field))}
             </Button>
          )}
        </Group>
        {mappingEntries.map(([oldVal, newVal], index) => (
          <Group key={index} grow gap="xs">
            <TextInput
              placeholder="Source Value"
              value={oldVal}
              onChange={(e) => updateKey(oldVal, e.target.value)}
            />
            <IconArrowRight size="1rem" style={{ flex: 'none' }} />
            <TextInput
              placeholder="Target Value"
              value={newVal}
              onChange={(e) => updateValue(oldVal, e.target.value)}
            />
            <ActionIcon aria-label="Remove entry" color="red" variant="subtle" onClick={() => removeEntry(oldVal)}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Button 
          size="xs" 
          variant="light" 
          leftSection={<IconPlus size="1rem" />}
          onClick={addEntry}
        >
          Add Mapping Rule
        </Button>
      </Stack>
    );
  };

  // Helper for Validator Rules
  const renderValidatorEditor = () => {
    let rules: Record<string, string> = {};
    try {
      rules = JSON.parse(selectedNode.data.schema || '{}');
    } catch (e) {
      return <Text size="xs" c="red">Invalid JSON schema. Use the raw editor below to fix.</Text>;
    }

    const updatePath = (oldPath: string, newPath: string) => {
      const next = { ...rules };
      const val = next[oldPath];
      delete next[oldPath];
      next[newPath] = val;
      updateNodeConfig(selectedNode.id, { schema: JSON.stringify(next) });
    };

    const updateType = (path: string, type: string) => {
      const next = { ...rules };
      next[path] = type;
      updateNodeConfig(selectedNode.id, { schema: JSON.stringify(next) });
    };

    const removeRule = (path: string) => {
      const next = { ...rules };
      delete next[path];
      updateNodeConfig(selectedNode.id, { schema: JSON.stringify(next) });
    };

    const addRule = () => {
      const next = { ...rules };
      next[`field_${Object.keys(rules).length}`] = 'string';
      updateNodeConfig(selectedNode.id, { schema: JSON.stringify(next) });
    };

    return (
      <Stack gap="xs">
        <Text size="sm" fw={500}>Validation Rules</Text>
        {Object.entries(rules).map(([path, type], index) => (
          <Group key={index} grow gap="xs">
            <Autocomplete
              placeholder="Field Path"
              data={availableFields}
              value={path}
              onChange={(val) => updatePath(path, val)}
            />
            <Select
              placeholder="Expected Type"
              data={['string', 'number', 'boolean', 'object', 'array', 'float64', 'int64']}
              value={type}
              onChange={(val) => updateType(path, val || 'string')}
            />
            <ActionIcon aria-label="Remove rule" color="red" variant="subtle" onClick={() => removeRule(path)}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Button 
          size="xs" 
          variant="light" 
          leftSection={<IconPlus size="1rem" />}
          onClick={addRule}
        >
          Add Validation Rule
        </Button>
      </Stack>
    );
  };

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
              data={availableFields}
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

  // Helper for Set Field
  const renderSetFieldEditor = () => {
    const fields = Object.entries(selectedNode.data)
      .filter(([k]) => k.startsWith('column.'))
      .map(([k, v]) => ({ fullKey: k, path: k.replace('column.', ''), value: v }));

    const updateFieldPath = (oldFullKey: string, newPath: string) => {
      const baseData = Object.fromEntries(
        Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.'))
      );
      const otherFields = Object.fromEntries(
        Object.entries(selectedNode.data).filter(([k]) => k.startsWith('column.') && k !== oldFullKey)
      );
      updateNodeConfig(selectedNode.id, { ...baseData, ...otherFields, [`column.${newPath}`]: selectedNode.data[oldFullKey] }, true);
    };

    const updateFieldValue = (fullKey: string, newValue: any) => {
      updateNodeConfig(selectedNode.id, { [fullKey]: newValue });
    };

    const removeField = (fullKey: string) => {
      const baseData = Object.fromEntries(
        Object.entries(selectedNode.data).filter(([k]) => !k.startsWith('column.'))
      );
      const remainingFields = Object.fromEntries(
        Object.entries(selectedNode.data).filter(([k]) => k.startsWith('column.') && k !== fullKey)
      );
      updateNodeConfig(selectedNode.id, { ...baseData, ...remainingFields }, true);
    };

    const isAdvanced = transType === 'advanced';

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={500}>{isAdvanced ? 'Transformation Rules' : 'Fields to Set'}</Text>
          {incomingPayload && (
            <Group gap="xs">
              <Text size="xs" c="dimmed">Quick add from source:</Text>
              <Group gap={4}>
                {availableFields.slice(0, 5).map(f => (
                  <Badge 
                    key={f} 
                    size="xs" 
                    variant="light" 
                    color="blue"
                    style={{ cursor: 'pointer', textTransform: 'none' }}
                    onClick={() => addFromSource(f)}
                  >
                    + {f}
                  </Badge>
                ))}
              </Group>
            </Group>
          )}
        </Group>

        {fields.length === 0 && (
          <Alert icon={<IconInfoCircle size="1rem" />} color="gray" variant="outline">
            <Text size="xs">No fields defined yet. Click "Add Field" or use the quick-add badges above to start.</Text>
          </Alert>
        )}

        {fields.map((field, index) => (
          <Group key={index} grow gap="xs" style={{ background: 'var(--mantine-color-gray-0)', padding: 8, borderRadius: 8 }}>
            <Autocomplete
              placeholder="Target Path"
              data={availableFields}
              size="xs"
              leftSection={<IconBracketsContain size="0.8rem" />}
              value={field.path}
              onChange={(val) => updateFieldPath(field.fullKey, val)}
            />
            <TextInput
              placeholder={isAdvanced ? "Expression (e.g. upper(source.field))" : "Value (literal or source.path)"}
              size="xs"
              leftSection={isAdvanced ? <IconCode size="0.8rem" /> : <IconVariable size="0.8rem" />}
              value={String(field.value || '')}
              onChange={(e) => updateFieldValue(field.fullKey, e.target.value)}
              rightSection={
                incomingPayload && (
                  <Group gap={2} px={4}>
                    <MantineTooltip label="Use source value" position="top">
                      <ActionIcon 
                        size="xs" 
                        variant="subtle" 
                        onClick={() => updateFieldValue(field.fullKey, `source.${field.path}`)}
                        disabled={!availableFields.includes(field.path)}
                      >
                        <IconArrowRight size="0.8rem" />
                      </ActionIcon>
                    </MantineTooltip>
                  </Group>
                )
              }
            />
            <ActionIcon aria-label="Remove field" color="red" variant="subtle" onClick={() => removeField(field.fullKey)} style={{ flex: 'none' }}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Button 
          size="xs" 
          variant="light" 
          fullWidth
          leftSection={<IconPlus size="1rem" />}
          onClick={() => addField()}
        >
          Add Transformation Rule
        </Button>
      </Stack>
    );
  };

  // Helper for Router Rules
  const renderRouterEditor = () => {
    let rules: any[] = [];
    try {
      rules = typeof selectedNode.data.rules === 'string' 
        ? JSON.parse(selectedNode.data.rules || '[]')
        : (selectedNode.data.rules || []);
    } catch (e) {
      rules = [];
    }

    const updateRule = (index: number, field: string, newValue: any) => {
      const next = [...rules];
      next[index] = { ...next[index], [field]: newValue };
      updateNodeConfig(selectedNode.id, { rules: JSON.stringify(next) });
    };

    const removeRule = (index: number) => {
      const next = rules.filter((_, i) => i !== index);
      updateNodeConfig(selectedNode.id, { rules: JSON.stringify(next) });
    };

    const addRule = () => {
      updateNodeConfig(selectedNode.id, { rules: JSON.stringify([...rules, { label: `path_${rules.length + 1}`, conditions: [] }]) });
    };

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={500}>Routing Rules</Text>
          <Button 
            size="compact-xs" 
            variant="light" 
            leftSection={<IconPlus size="1rem" />}
            onClick={addRule}
          >
            Add Rule
          </Button>
        </Group>
        {rules.length === 0 && (
          <Text size="xs" c="dimmed" ta="center">No rules defined. Messages will follow "default" branch.</Text>
        )}
        {rules.map((rule, index) => (
          <Card key={index} withBorder p="xs" shadow="xs" radius="md">
            <Stack gap="xs">
              <Group grow gap="xs" align="flex-end">
                <TextInput
                  placeholder="Target Branch (e.g. high_priority)"
                  label="Branch Label"
                  size="xs"
                  value={rule.label}
                  onChange={(e) => updateRule(index, 'label', e.target.value)}
                  required
                />
                <ActionIcon 
                  aria-label="Remove router rule"
                  color="red" 
                  variant="subtle" 
                  onClick={() => removeRule(index)} 
                  mb={2} 
                  style={{ flex: 'none' }}
                >
                  <IconTrash size="1rem" />
                </ActionIcon>
              </Group>
              
              <FilterEditor 
                conditions={rule.conditions || []} 
                availableFields={availableFields}
                onChange={(next) => updateRule(index, 'conditions', next)}
              />
            </Stack>
          </Card>
        ))}
      </Stack>
    );
  };

  // Helper for Switch Cases
  const renderSwitchEditor = () => {
    let cases: any[] = [];
    try {
      cases = typeof selectedNode.data.cases === 'string' 
        ? JSON.parse(selectedNode.data.cases || '[]')
        : (selectedNode.data.cases || []);
    } catch (e) {
      cases = [];
    }

    const updateCase = (index: number, field: string, newValue: any) => {
      const next = [...cases];
      next[index] = { ...next[index], [field]: newValue };
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
    };

    const removeCase = (index: number) => {
      const next = cases.filter((_, i) => i !== index);
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
    };

    const addCase = () => {
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify([...cases, { value: '', label: `case_${cases.length + 1}` }]) });
    };

    const addCaseCondition = (index: number) => {
      const next = [...cases];
      const conditions = next[index].conditions || [];
      next[index].conditions = [...conditions, { field: '', operator: '=', value: '' }];
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
    };

    const updateCaseCondition = (index: number, condIdx: number, field: string, value: string) => {
      const next = [...cases];
      const conditions = [...next[index].conditions];
      conditions[condIdx] = { ...conditions[condIdx], [field]: value };
      next[index].conditions = conditions;
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
    };

    const removeCaseCondition = (index: number, condIdx: number) => {
      const next = [...cases];
      next[index].conditions = next[index].conditions.filter((_: any, i: number) => i !== condIdx);
      updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
    };

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={500}>Switch Cases</Text>
          <Button 
            size="compact-xs" 
            variant="light" 
            leftSection={<IconPlus size="1rem" />}
            onClick={addCase}
          >
            Add Case
          </Button>
        </Group>
        {cases.length === 0 && (
          <Text size="xs" c="dimmed" ta="center">No cases defined. Messages will follow "default" branch.</Text>
        )}
        {cases.map((c, index) => (
          <Card key={index} withBorder p="xs" bg="gray.0" radius="md">
            <Stack gap="xs">
              <Group grow gap="xs" align="flex-end">
                <TextInput
                  placeholder="Edge Label"
                  label="Branch Label"
                  size="xs"
                  value={c.label}
                  onChange={(e) => updateCase(index, 'label', e.target.value)}
                  required
                />
                <ActionIcon 
                  aria-label="Remove switch case"
                  color="red" 
                  variant="subtle" 
                  onClick={() => removeCase(index)} 
                  mb={2} 
                  style={{ flex: 'none' }}
                >
                  <IconTrash size="1rem" />
                </ActionIcon>
              </Group>
              
              {!c.conditions || c.conditions.length === 0 ? (
                <Stack gap={4}>
                  <Group grow gap="xs" align="flex-end">
                    <TextInput
                        placeholder="Value"
                        label={`Match "${selectedNode.data.field || 'field'}" with:`}
                        size="xs"
                        value={c.value}
                        onChange={(e) => updateCase(index, 'value', e.target.value)}
                      />
                      <Button 
                        size="compact-xs" 
                        variant="subtle" 
                        onClick={() => updateCase(index, 'conditions', [{field: '', operator: '=', value: ''}])}
                      >
                        Use Conditions
                      </Button>
                  </Group>
                </Stack>
              ) : (
                <Stack gap="xs" p={4} style={{ border: '1px dashed var(--mantine-color-gray-4)', borderRadius: 4 }}>
                   <Group justify="space-between">
                     <Text size="10px" fw={700} c="dimmed">CONDITIONS (AND)</Text>
                     <ActionIcon 
                       size="xs" 
                       variant="subtle" 
                       color="red"
                       onClick={() => {
                          const next = [...cases];
                          delete next[index].conditions;
                          updateNodeConfig(selectedNode.id, { cases: JSON.stringify(next) });
                       }}
                     >
                       <IconTrash size="0.8rem" />
                     </ActionIcon>
                   </Group>
                   {c.conditions.map((cond: any, condIdx: number) => (
                      <Group key={condIdx} grow gap={4} align="flex-end">
                        <Autocomplete 
                          placeholder="Field" 
                          data={availableFields}
                          size="xs"
                          value={cond.field || ''} 
                          onChange={(val) => updateCaseCondition(index, condIdx, 'field', val)} 
                        />
                        <Select 
                          data={['=', '!=', '>', '>=', '<', '<=', 'contains']} 
                          size="xs"
                          value={cond.operator || '='} 
                          onChange={(val) => updateCaseCondition(index, condIdx, 'operator', val || '=')} 
                          style={{ width: 80, flex: 'none' }}
                        />
                        <TextInput 
                          placeholder="Value" 
                          size="xs"
                          value={cond.value || ''} 
                          onChange={(e) => updateCaseCondition(index, condIdx, 'value', e.target.value)} 
                        />
                        <ActionIcon 
                          aria-label="Remove switch condition"
                          color="red" 
                          variant="subtle" 
                          onClick={() => removeCaseCondition(index, condIdx)} 
                          style={{ flex: 'none' }}
                        >
                          <IconTrash size="0.8rem" />
                        </ActionIcon>
                      </Group>
                   ))}
                   <Button 
                     size="compact-xs" 
                     variant="light" 
                     leftSection={<IconPlus size="0.8rem" />}
                     onClick={() => addCaseCondition(index)}
                   >
                     Add Condition
                   </Button>
                </Stack>
              )}
            </Stack>
          </Card>
        ))}
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
              <Box key={f.name} p={6} style={{ borderRadius: 4, background: 'var(--mantine-color-orange-0)', cursor: 'pointer' }} onClick={() => onInsertExample(f.example)}>
                <Group justify="space-between">
                  <Text size="xs" fw={700} c="orange.9">{f.name}</Text>
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

  return (
    <>
    <Grid gutter="lg" style={{ minHeight: 'calc(100vh - 180px)' }}>
      {/* Column 1: Source Data */}
      <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
        <Stack gap="md" h="100%">
          <Group gap="xs" px="xs">
            <IconDatabase size="1.2rem" color="var(--mantine-color-blue-6)" />
            <Text size="sm" fw={700} c="dimmed">1. SOURCE DATA / SAMPLE</Text>
          </Group>
          
          {renderPathHelp()}

          <Card withBorder padding="xs" radius="md">
            <Group justify="space-between" mb="xs">
              <Group gap="xs">
                <IconList size="1rem" color="var(--mantine-color-gray-6)" />
                <Text size="xs" fw={700}>AVAILABLE FIELDS</Text>
              </Group>
              <Badge size="xs" variant="light">{availableFields.length}</Badge>
            </Group>
            <FieldExplorer
              availableFields={availableFields}
              incomingPayload={incomingPayload}
              onAdd={(path) => addFromSource(path)}
            />
          </Card>

          <Card withBorder padding="xs" radius="md">
            <Group justify="space-between" mb="xs">
              <Group gap="xs">
                <IconDatabase size="1rem" color="var(--mantine-color-green-6)" />
                <Text size="xs" fw={700}>TARGET COLUMNS</Text>
              </Group>
              <Badge size="xs" variant="light" color="green">{targetSchema.length}</Badge>
            </Group>
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
          </Card>

          {transType === 'advanced' && <FunctionLibrary />}

          <Card withBorder padding="xs" radius="md" bg="gray.0">
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
                <Text size="sm" fw={700}>2. TRANSFORM LOGIC</Text>
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

            <TextInput 
              label="Node Label" 
              placeholder="Display name in editor" 
              leftSection={<IconVariable size="1rem" />}
              value={selectedNode.data.label || ''} 
              onChange={(e) => updateNodeConfig(selectedNode.id, { label: e.target.value })} 
            />

            <Box flex={1} style={{ overflow: 'hidden' }}>
              <ScrollArea h="100%" offsetScrollbars>
                <Stack gap="md" py="xs">
              {transType === 'router' && (
                <Stack gap="md">
                  <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="Content-Based Router">
                    Rules are evaluated in order. The first rule that matches will determine the branch.
                    If no rules match, the message follows the "default" branch.
                  </Alert>
                  {renderRouterEditor()}
                </Stack>
              )}

              {transType === 'switch' && (
            <>
              <Autocomplete 
                label="Field to Switch On" 
                placeholder="e.g. status" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to branch on. Supports nested objects and arrays."
              />
              {renderSwitchEditor()}
              <Alert icon={<IconInfoCircle size="1rem" />} color="orange" py="xs">
                <Text size="xs">Matching messages will follow the edge with the corresponding label. Unmatched follow "default".</Text>
              </Alert>
            </>
          )}

          {transType === 'merge' && (
            <>
              <Select
                label="Merge Strategy"
                data={[
                  { label: 'Deep Merge (Recursive)', value: 'deep' },
                  { label: 'Shallow Merge (Root level)', value: 'shallow' },
                  { label: 'Overwrite (Last wins)', value: 'overwrite' },
                  { label: 'If Missing (First wins)', value: 'if_missing' }
                ]}
                value={selectedNode.data.strategy || 'deep'}
                onChange={(val) => updateNodeConfig(selectedNode.id, { strategy: val || 'deep' })}
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="pink" title="Merge Strategy">
                <Stack gap="xs">
                  <Text size="xs">Merge nodes wait for ALL branches connected to them to complete before passing a single merged message.</Text>
                </Stack>
              </Alert>
            </>
          )}

          {isAggregate && (
            <>
              <Select 
                label="Operation" 
                data={['count', 'sum', 'avg', 'min', 'max']} 
                value={selectedNode.data.operation || 'count'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { operation: val || 'count' })} 
              />
              <Autocomplete 
                label="Field" 
                placeholder="e.g. amount" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to aggregate."
              />
              <Autocomplete 
                label="Group By Field" 
                placeholder="e.g. user_id" 
                data={availableFields}
                value={selectedNode.data.groupByField || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { groupByField: val })} 
                description="Optional field to group by."
              />
              <TextInput 
                label="Window (Duration)" 
                placeholder="e.g. 1m, 1h" 
                value={selectedNode.data.window || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { window: e.target.value })} 
                description="Optional time window (e.g., 10s, 1m). If empty, aggregates over all time."
              />
              <TextInput 
                label="Output Field" 
                placeholder="e.g. total_amount" 
                value={selectedNode.data.targetField || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })} 
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="cyan" py="xs">
                <Text size="xs">Aggregate nodes perform stateful operations over a stream of messages.</Text>
              </Alert>
            </>
          )}

          {isForeach && (
            <>
              <Autocomplete
                label="Array Path"
                placeholder="e.g. items"
                data={availableFields}
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
                data={availableFields}
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

          {transType === 'join' && (
            <>
              <Select
                label="Join Mode"
                data={[
                  { label: 'Store (Save current record to state)', value: 'store' },
                  { label: 'Lookup (Enrich from state)', value: 'lookup' },
                ]}
                value={selectedNode.data.mode || 'lookup'}
                onChange={(val) => updateNodeConfig(selectedNode.id, { mode: val || 'lookup' })}
              />
              <TextInput
                label="Join Key (Message Path)"
                placeholder="e.g. order_id"
                value={selectedNode.data.key || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { key: e.target.value })}
                description="Field in the current message used to match records."
              />
              <TextInput
                label="Storage Namespace"
                placeholder="default"
                value={selectedNode.data.namespace || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { namespace: e.target.value })}
                description="Use namespaces to separate different join datasets."
              />
              {selectedNode.data.mode === 'lookup' && (
                <>
                  <TextInput
                    label="Joined Field Prefix"
                    placeholder="joined_"
                    value={selectedNode.data.prefix || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { prefix: e.target.value })}
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
            </>
          )}

          {(transType === 'filter_data' || transType === 'condition' || transType === 'validate') && (
            <>
              {(transType === 'filter_data' || transType === 'validate') && (
                 <Box mb="md" p="xs" style={{ border: '1px dashed var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}>
                    <Switch 
                       label="Set result as boolean field instead of filtering" 
                       checked={!!selectedNode.data.asField || transType === 'validate'}
                       disabled={transType === 'validate'}
                       onChange={(e) => updateNodeConfig(selectedNode.id, { asField: e.currentTarget.checked })}
                       mb={selectedNode.data.asField || transType === 'validate' ? 'xs' : 0}
                    />
                    {(selectedNode.data.asField || transType === 'validate') && (
                       <TextInput 
                          label="Target Field Name"
                          placeholder="e.g. is_valid"
                          value={selectedNode.data.targetField || ''}
                          onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
                          size="xs"
                       />
                    )}
                 </Box>
              )}
              {(() => {
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
                  <FilterEditor
                    conditions={conditions}
                    availableFields={availableFields}
                    onChange={(next) =>
                      updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) })
                    }
                  />
                )
              })()}
              <Alert icon={<IconInfoCircle size="1rem" />} color={transType === 'condition' ? 'yellow' : 'violet'} py="xs" mt="md">
                <Stack gap={4}>
                  <Text size="xs">
                    {transType === 'condition' 
                      ? 'Conditions branch the flow. Use "true" and "false" labels on outgoing edges.' 
                      : 'Filters will stop the message if the condition is not met.'}
                  </Text>
                </Stack>
              </Alert>
            </>
          )}

          {transType === 'mapping' && (
            <>
              <Autocomplete 
                label="Field" 
                placeholder="e.g. status" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to map. Supports nested objects and arrays."
              />
              <Select
                label="Mapping Type"
                data={[{label: 'Exact Match', value: 'exact'}, {label: 'Numeric Range', value: 'range'}, {label: 'Regular Expression', value: 'regex'}]}
                value={selectedNode.data.mappingType || 'exact'}
                onChange={(val) => updateNodeConfig(selectedNode.id, { mappingType: val || 'exact' })}
                mb="xs"
                description="Exact: '1' -> 'Active'. Range: '0-10' -> 'Low'. Regex: '^A.*' -> 'Starts with A'."
              />
              {renderMappingEditor()}
              <Divider label="Raw JSON" labelPosition="center" />
              <JsonInput 
                label="Mapping (JSON)" 
                placeholder='{"1": "Active", "0": "Inactive"}' 
                value={selectedNode.data.mapping || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { mapping: val })} 
                formatOnBlur
                minRows={10}
              />
            </>
          )}

          {transType === 'validator' && (
            <>
              {renderValidatorEditor()}
              <Divider label="Raw JSON" labelPosition="center" />
              <JsonInput 
                label="Rules (JSON)" 
                placeholder='{"field.path": "string"}' 
                value={selectedNode.data.schema || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { schema: val })} 
                formatOnBlur
                minRows={10}
              />
            </>
          )}

          {transType === 'stat_validator' && (
            <>
              {renderStatValidatorEditor()}
            </>
          )}

          {transType === 'mask' && (
            <>
              <Autocomplete 
                label="Field" 
                placeholder="e.g. email (use * for all)" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to mask. Supports nested objects and arrays. Use * to scan all fields."
              />
              <Select 
                label="Mask Type" 
                data={[
                  { label: 'All (****)', value: 'all' },
                  { label: 'Partial (ab****yz)', value: 'partial' },
                  { label: 'Email (a****@b.com)', value: 'email' },
                  { label: 'Auto PII Detection (SSN, Cards, IP)', value: 'pii' },
                ]} 
                value={selectedNode.data.maskType || 'all'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { maskType: val || 'all' })} 
              />
            </>
          )}

          {transType === 'rate_limit' && (
            <Stack gap="xs">
              <Group grow>
                <NumberInput 
                  label="Messages Per Second" 
                  min={0.1}
                  step={1}
                  value={selectedNode.data.mps || 100}
                  onChange={(val) => updateNodeConfig(selectedNode.id, { mps: val })}
                />
                <NumberInput 
                  label="Burst Size" 
                  min={1}
                  value={selectedNode.data.burst || 100}
                  onChange={(val) => updateNodeConfig(selectedNode.id, { burst: val })}
                />
              </Group>
              <Select 
                label="Strategy"
                data={[
                  { label: 'Wait (Block)', value: 'wait' },
                  { label: 'Drop (Discard)', value: 'drop' },
                ]}
                value={selectedNode.data.strategy || 'wait'}
                onChange={(val) => updateNodeConfig(selectedNode.id, { strategy: val || 'wait' })}
              />
              <Autocomplete 
                label="Key Field (Optional)" 
                placeholder="e.g. user_id" 
                data={availableFields}
                value={selectedNode.data.keyField || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { keyField: val })} 
                description="If set, limits are applied per unique value of this field."
              />
            </Stack>
          )}

          {transType === 'db_lookup' && (
            <Tabs defaultValue="query">
              <Tabs.List mb="md">
                <Tabs.Tab value="query" leftSection={<IconDatabase size="1rem" />}>Query</Tabs.Tab>
                <Tabs.Tab value="advanced" leftSection={<IconSettings size="1rem" />}>Settings & Test</Tabs.Tab>
              </Tabs.List>

              <Tabs.Panel value="query">
                <Stack gap="sm">
                  <Select
                    label="Database Source"
                    placeholder="Select source"
                    data={(Array.isArray(sources) ? sources : [])
                      .filter((s: any) => {
                        // Allow only DB-like sources first
                        const allowedType = ['postgres', 'mysql', 'mssql', 'sqlite', 'mariadb', 'oracle', 'db2', 'mongodb', 'yugabyte', 'clickhouse'].includes(s.type);
                        if (!allowedType) return false;
                        // Filter out CDC-enabled sources except for SQL Server (mssql)
                        const useCDC = s?.config?.use_cdc;
                        const isCDCEnabled = useCDC !== undefined ? useCDC !== 'false' : false;
                        return s.type === 'mssql' || !isCDCEnabled;
                      })
                      .map((s: any) => ({ label: s.name, value: s.id }))}
                    value={selectedNode.data.sourceId || ''}
                    onChange={(val) => updateNodeConfig(selectedNode.id, { sourceId: val })}
                  />
                  <TextInput
                    label="Table Name"
                    placeholder="e.g. users (or collection for MongoDB)"
                    value={selectedNode.data.table || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { table: e.target.value })}
                  />
                  <Group grow>
                    <TextInput
                      label="Key Column (DB)"
                      placeholder="e.g. id"
                      value={selectedNode.data.keyColumn || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { keyColumn: e.target.value })}
                    />
                    <Autocomplete
                      label="Key Field (Message)"
                      placeholder="e.g. user_id"
                      data={availableFields}
                      value={selectedNode.data.keyField || ''}
                      onChange={(val) => updateNodeConfig(selectedNode.id, { keyField: val })}
                    />
                  </Group>
                  <Group grow>
                    <TextInput
                      label="Value Column (DB)"
                      placeholder="e.g. full_name (use * for all columns)"
                      value={selectedNode.data.valueColumn || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { valueColumn: e.target.value })}
                    />
                    <TextInput
                      label="Target Field (Message)"
                      placeholder="e.g. user_name"
                      value={selectedNode.data.targetField || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
                    />
                  </Group>
                  <Divider label="Or use a full Query Template" labelPosition="center" />
                  <Textarea
                    label="Query Template (SQL)"
                    placeholder="SELECT * FROM users WHERE tenant_id = {{ source.tenant }} AND status = {{ 'active' }}"
                    value={selectedNode.data.queryTemplate || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { queryTemplate: e.currentTarget.value })}
                    autosize
                    minRows={3}
                    description="When provided, Hermod executes this query with safe parameterization of {{ }} tokens. For MongoDB sources, use Where Clause instead."
                  />
                </Stack>
              </Tabs.Panel>

              <Tabs.Panel value="advanced">
                <Stack gap="sm">
                  <TextInput
                    label="Where Clause (SQL or MongoDB JSON)"
                    placeholder="e.g. status = 'active' AND id = {{user_id}}"
                    value={selectedNode.data.whereClause || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { whereClause: e.target.value })}
                    description="Overrides Table/Key Column if provided. Supports {{ field }} templates. For MongoDB, provide a JSON filter string."
                  />
                  <Group grow align="flex-end">
                    <TextInput
                      label="Default Value"
                      placeholder="Value if not found"
                      value={selectedNode.data.defaultValue || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { defaultValue: e.target.value })}
                      description="Populated if no results found."
                    />
                    <TextInput
                      label="Cache TTL"
                      placeholder="e.g. 5m, 1h"
                      value={selectedNode.data.ttl || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { ttl: e.target.value })}
                    />
                    <TextInput
                      label="Flatten Into"
                      placeholder="e.g. customer_flat or '.' for top level"
                      value={selectedNode.data.flattenInto || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { flattenInto: e.target.value })}
                      description="If the result is an object, copy its fields into this path. Use '.' to flatten to top level."
                    />
                    <Button 
                      variant="light" 
                      color="orange" 
                      leftSection={<IconPlayerPlay size="0.8rem" />}
                      onClick={testLookup}
                      loading={testing}
                    >
                      Test Lookup
                    </Button>
                  </Group>
                </Stack>
              </Tabs.Panel>
            </Tabs>
          )}

          {transType === 'api_lookup' && (
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
                      onChange={(val) => updateNodeConfig(selectedNode.id, { method: val || 'GET' })}
                    />
                    <TextInput
                      label="Target Field (Message)"
                      placeholder="e.g. enriched_data"
                      value={selectedNode.data.targetField || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
                    />
                  </Group>
                  <TextInput
                    label="URL"
                    placeholder="https://api.example.com/v1/users/{{user_id}}"
                    value={selectedNode.data.url || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { url: e.target.value })}
                    description={
                      <Stack gap={2} mt={4}>
                        <Text size="10px" c="dimmed">Supports {'{{field}}'} template variables. Click to add:</Text>
                        {availableFields.length > 0 && (
                          <Group gap={4}>
                            {availableFields.slice(0, 8).map(f => (
                              <Badge 
                                key={f} 
                                size="xs" 
                                variant="outline" 
                                style={{ cursor: 'pointer', textTransform: 'none' }} 
                                onClick={() => {
                                  const current = selectedNode.data.url || '';
                                  updateNodeConfig(selectedNode.id, { url: current + `{{${f}}}` });
                                }}
                              >
                                {f}
                              </Badge>
                            ))}
                          </Group>
                        )}
                      </Stack>
                    }
                  />
                  <TextInput
                    label="Response JSON Path"
                    placeholder="e.g. data.profile.name (Use '.' for root)"
                    value={selectedNode.data.responsePath || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { responsePath: e.target.value })}
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
                    onChange={(val) => updateNodeConfig(selectedNode.id, { headers: val })}
                    formatOnBlur
                    minRows={4}
                  />
                  <JsonInput
                    label="Query Params (JSON)"
                    placeholder='{"id": "{{id}}", "ref": "hermod"}'
                    value={selectedNode.data.queryParams || ''}
                    onChange={(val) => updateNodeConfig(selectedNode.id, { queryParams: val })}
                    formatOnBlur
                    minRows={4}
                  />
                  {selectedNode.data.method !== 'GET' && (
                    <JsonInput
                      label="Request Body (JSON)"
                      placeholder='{"id": "{{user_id}}", "query": "..."}'
                      value={selectedNode.data.body || ''}
                      onChange={(val) => updateNodeConfig(selectedNode.id, { body: val })}
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
                    onChange={(val) => updateNodeConfig(selectedNode.id, { authType: val || '' })}
                  />
                  {selectedNode.data.authType === 'basic' && (
                    <Group grow>
                      <TextInput
                        label="Username"
                        value={selectedNode.data.username || ''}
                        onChange={(e) => updateNodeConfig(selectedNode.id, { username: e.target.value })}
                      />
                      <PasswordInput
                        label="Password"
                        value={selectedNode.data.password || ''}
                        onChange={(e) => updateNodeConfig(selectedNode.id, { password: e.target.value })}
                      />
                    </Group>
                  )}
                  {selectedNode.data.authType === 'bearer' && (
                    <PasswordInput
                      label="Token"
                      value={selectedNode.data.token || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { token: e.target.value })}
                    />
                  )}
                  <Group grow>
                    <TextInput
                      label="Default Value"
                      placeholder="Value if lookup fails"
                      value={selectedNode.data.defaultValue || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { defaultValue: e.target.value })}
                      description="Used if API call fails or returns no data."
                    />
                    <TextInput
                      label="Timeout"
                      placeholder="10s"
                      value={selectedNode.data.timeout || ''}
                      onChange={(e) => updateNodeConfig(selectedNode.id, { timeout: e.target.value })}
                    />
                  </Group>
                  <Group grow>
                    <TextInput
                       label="Cache TTL"
                       placeholder="e.g. 5m, 1h"
                       value={selectedNode.data.ttl || ''}
                       onChange={(e) => updateNodeConfig(selectedNode.id, { ttl: e.target.value })}
                    />
                    <NumberInput
                      label="Max Retries"
                      value={selectedNode.data.maxRetries || 0}
                      onChange={(val) => updateNodeConfig(selectedNode.id, { maxRetries: val })}
                    />
                  </Group>
                  <TextInput
                    label="Retry Delay"
                    placeholder="1s"
                    value={selectedNode.data.retryDelay || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { retryDelay: e.target.value })}
                  />
                </Stack>
              </Tabs.Panel>
            </Tabs>
          )}

          {(transType === 'ai_enrichment' || transType === 'ai_mapper') && (
            <Stack gap="md">
              <Select
                label="Provider"
                data={[
                  { value: 'openai', label: 'OpenAI' },
                  { value: 'ollama', label: 'Ollama (Local)' },
                ]}
                value={selectedNode.data.provider || 'openai'}
                onChange={(val) => updateNodeConfig(selectedNode.id, { provider: val || 'openai' })}
              />
              <TextInput
                label="Endpoint"
                placeholder="Auto-detected if empty"
                value={selectedNode.data.endpoint || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { endpoint: e.target.value })}
              />
              <PasswordInput
                label="API Key"
                placeholder="Required for OpenAI"
                value={selectedNode.data.apiKey || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { apiKey: e.target.value })}
              />
              <TextInput
                label="Model"
                placeholder="gpt-3.5-turbo, llama2, etc."
                value={selectedNode.data.model || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { model: e.target.value })}
              />
              {transType === 'ai_enrichment' && (
                <Textarea
                  label="Prompt"
                  placeholder="How should the AI process the data?"
                  minRows={3}
                  value={selectedNode.data.prompt || ''}
                  onChange={(e) => updateNodeConfig(selectedNode.id, { prompt: e.target.value })}
                />
              )}
              {transType === 'ai_mapper' && (
                <>
                  <Textarea
                    label="Target Schema"
                    placeholder='{ "type": "object", "properties": { ... } }'
                    minRows={5}
                    value={selectedNode.data.targetSchema || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { targetSchema: e.target.value })}
                  />
                  <TextInput
                    label="Hints"
                    placeholder="Optional hints for mapping"
                    value={selectedNode.data.hints || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { hints: e.target.value })}
                  />
                </>
              )}
              <TextInput
                label="Target Field"
                placeholder="Where to store the result (empty to merge JSON)"
                value={selectedNode.data.targetField || ''}
                onChange={(e) => updateNodeConfig(selectedNode.id, { targetField: e.target.value })}
              />
            </Stack>
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
              {renderSetFieldEditor()}
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
              {renderSetFieldEditor()}
              <Divider label="Raw JSON" labelPosition="center" mt="md" />
              <JsonInput 
                label="Config (JSON)" 
                placeholder='{"column.user.name": "lower(source.user.name)"}' 
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
              />
              <Alert color="blue" py="xs" mt="md">
                <Text size="xs" fw={700}>Supported operations:</Text>
                <Grid gutter="xs">
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
          <Group grow>
            <Select 
              label="On Error"
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
          </Group>
        </Stack>
            </ScrollArea>
          </Box>
        </Stack>
      </Card>
    </Grid.Col>

      {/* Column 3: Live Preview */}
      <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
        <PreviewPanel
          title="3. LIVE PREVIEW"
          loading={testing || (previewMutation as any)?.isPending}
          error={previewError || ((previewMutation as any)?.error?.message ?? null)}
          result={previewResult || (previewMutation as any)?.data}
          onRun={runPreview}
        />
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
