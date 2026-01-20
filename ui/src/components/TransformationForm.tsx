import { useState, useEffect, useCallback } from 'react';
import { 
  TextInput, Select, Stack, Alert, Divider, Text, Group, ActionIcon, 
  Button, Code, List, Autocomplete, JsonInput, Badge, Grid,
  PasswordInput, NumberInput, Card, Tabs, ScrollArea, Box,
  Tooltip as MantineTooltip,
  Switch
} from '@mantine/core';
import { 
  IconTrash, IconPlus, IconInfoCircle, IconArrowRight, IconPlayerPlay,
  IconSearch, IconCopy, IconCheck, IconAlertCircle, IconFunction,
  IconVariable, IconDatabase, IconCloud, IconFilter, IconList,
  IconSettings, IconEye, IconCode, IconBracketsContain
} from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '../api';
import { getValByPath, simulateTransformation } from '../utils/transformationUtils';

interface TransformationFormProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  onRunSimulation?: (payload?: any) => void;
  availableFields: string[];
  incomingPayload?: any;
  sources?: any[];
  sinkSchema?: any;
}

export function TransformationForm({ selectedNode, updateNodeConfig, onRunSimulation, availableFields, incomingPayload, sources, sinkSchema }: TransformationFormProps) {
  if (!selectedNode) return null;

  const [testing, setTesting] = useState(false);
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [targetSchema, setTargetSchema] = useState<string[]>([]);
  const [loadingTarget, setLoadingTarget] = useState(false);

  const fetchTargetSchema = useCallback(async () => {
    if (!sinkSchema || !sinkSchema.config?.table) return;
    setLoadingTarget(true);
    try {
      const res = await apiFetch('/api/sinks/sample', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sink: sinkSchema,
          table: sinkSchema.config.table
        })
      });
      const data = await res.json();
      if (data.after) {
        const payload = JSON.parse(data.after);
        setTargetSchema(Object.keys(payload));
      }
    } catch (e) {
      console.error("Failed to fetch target schema", e);
    } finally {
      setLoadingTarget(false);
    }
  }, [sinkSchema]);

  useEffect(() => {
    if (sinkSchema) fetchTargetSchema();
  }, [sinkSchema, fetchTargetSchema]);

  const [previewResult, setPreviewResult] = useState<any>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);

  // console.log(previewResult, previewError);

  const runPreview = async () => {
    if (!incomingPayload) return;
    setTesting(true);
    setPreviewError(null);
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
        setPreviewError(data.error);
      } else {
        setPreviewResult(data);
        if (previewResult === 'force_use') console.log(previewError);
      }
    } catch (e: any) {
      setPreviewError(e.message);
    } finally {
      setTesting(false);
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedField(text);
    setTimeout(() => setCopiedField(null), 2000);
  };

  useEffect(() => {
    const timer = setTimeout(() => {
      runPreview();
    }, 1000);
    return () => clearTimeout(timer);
  }, [selectedNode.data, incomingPayload]);

  const transType = selectedNode.data.transType || selectedNode.type;

  const addField = (path: string = '', value: string = '') => {
    const fields = Object.entries(selectedNode.data)
      .filter(([k]) => k.startsWith('column.'));
    const fieldName = path || `new_field_${fields.length}`;
    updateNodeConfig(selectedNode.id, { [`column.${fieldName}`]: value });
  };

  const addFromSource = (path: string) => {
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
      copyToClipboard(path);
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
            <ActionIcon color="red" variant="subtle" onClick={() => removeEntry(oldVal)}>
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
            <ActionIcon color="red" variant="subtle" onClick={() => removeField(field.fullKey)} style={{ flex: 'none' }}>
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
                <ActionIcon color="red" variant="subtle" onClick={() => removeCase(index)} mb={2} style={{ flex: 'none' }}>
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
                        <ActionIcon color="red" variant="subtle" onClick={() => removeCaseCondition(index, condIdx)} style={{ flex: 'none' }}>
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

  const renderFilterEditor = () => {
    let conditions: any[] = [];
    try {
      conditions = typeof selectedNode.data.conditions === 'string' 
        ? JSON.parse(selectedNode.data.conditions) 
        : (selectedNode.data.conditions || []);
    } catch (e) {
      conditions = [];
    }

    if (conditions.length === 0 && selectedNode.data.field) {
      conditions.push({
        field: selectedNode.data.field,
        operator: selectedNode.data.operator || '=',
        value: selectedNode.data.value || ''
      });
    }

    const updateCondition = (index: number, field: string, value: string) => {
      const next = [...conditions];
      next[index] = { ...next[index], [field]: value };
      updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) });
    };

    const removeCondition = (index: number) => {
      const next = conditions.filter((_, i) => i !== index);
      updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) });
    };

    const addCondition = () => {
      const next = [...conditions, { field: '', operator: '=', value: '' }];
      updateNodeConfig(selectedNode.id, { conditions: JSON.stringify(next) });
    };

    return (
      <Stack gap="xs">
        <Text size="sm" fw={500}>Filter Conditions (AND)</Text>
        {conditions.map((cond, index) => (
          <Group key={index} grow gap="xs" align="flex-end" style={{ background: 'var(--mantine-color-gray-0)', padding: 8, borderRadius: 8 }}>
            <Stack gap={2}>
              <Text size="10px" c="dimmed">Field</Text>
              <Autocomplete 
                placeholder="e.g. status" 
                data={availableFields}
                size="xs"
                value={cond.field || ''} 
                onChange={(val) => updateCondition(index, 'field', val)} 
              />
            </Stack>
            <Stack gap={2}>
              <Text size="10px" c="dimmed">Operator</Text>
              <Select 
                data={['=', '!=', '>', '>=', '<', '<=', 'contains']} 
                size="xs"
                value={cond.operator || '='} 
                onChange={(val) => updateCondition(index, 'operator', val || '=')} 
              />
            </Stack>
            <Stack gap={2}>
              <Text size="10px" c="dimmed">Value</Text>
              <TextInput 
                placeholder="Value" 
                size="xs"
                value={cond.value || ''} 
                onChange={(e) => updateCondition(index, 'value', e.target.value)} 
              />
            </Stack>
            <ActionIcon color="red" variant="subtle" onClick={() => removeCondition(index)} mb={2}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Button 
          size="xs" 
          variant="light" 
          leftSection={<IconPlus size="1rem" />}
          onClick={addCondition}
        >
          Add Condition
        </Button>
      </Stack>
    );
  };

  const renderLivePreview = () => {
    if (!incomingPayload) return null;
    
    const data = selectedNode.data;
    const simResult = simulateTransformation(transType, data, incomingPayload);
    const { output, metadata } = simResult;
    const { isFiltered, filterReason } = metadata;
    
    const currentFieldValue = data.field ? getValByPath(incomingPayload, data.field) : undefined;

    return (
      <Card withBorder shadow="sm" radius="md" p="md" h="100%">
        <Stack gap="sm" h="100%">
          <Group justify="space-between">
            <Group gap="xs">
              <IconEye size="1.2rem" color="var(--mantine-color-blue-6)" />
              <Text size="sm" fw={700}>LIVE PREVIEW</Text>
            </Group>
            <Group gap="xs">
              {onRunSimulation && (
                <Button 
                  size="compact-xs" 
                  variant="light" 
                  color="green" 
                  leftSection={<IconPlayerPlay size="0.8rem" />}
                  onClick={() => onRunSimulation(incomingPayload)}
                >
                  Run Full Simulation
                </Button>
              )}
              {isFiltered ? (
                 <Badge variant="filled" color="red" leftSection={<IconFilter size="0.8rem" />}>Filtered</Badge>
              ) : (
                 <Badge variant="light" color="green" leftSection={<IconCheck size="0.8rem" />}>Active</Badge>
              )}
            </Group>
          </Group>

          <Divider />

          {(isFiltered || filterReason) && (
            <Alert 
              icon={isFiltered ? <IconAlertCircle size="1rem" /> : <IconInfoCircle size="1rem" />} 
              color={isFiltered ? "red" : "blue"} 
              title={isFiltered ? "Message would be filtered out" : "Node Result"}
            >
               {filterReason}
            </Alert>
          )}

          {data.field && (
            <Box p="xs" style={{ background: 'var(--mantine-color-blue-0)', borderRadius: 8 }}>
               <Text size="xs" fw={700} c="blue" mb={4}>RESOLVED FIELD: {data.field}</Text>
               <Code color="blue" block style={{ fontSize: '11px' }}>
                 {currentFieldValue !== undefined ? JSON.stringify(currentFieldValue, null, 2) : 'undefined (path not found)'}
               </Code>
            </Box>
          )}

          <Stack gap={4} style={{ flex: 1 }}>
            <Group justify="space-between">
              <Text size="xs" fw={700} c="dimmed">RESULTING PAYLOAD</Text>
              <ActionIcon size="xs" variant="subtle" onClick={() => copyToClipboard(JSON.stringify(output, null, 2))}>
                <IconCopy size="0.8rem" />
              </ActionIcon>
            </Group>
            <ScrollArea.Autosize mah={500} style={{ borderRadius: 8, background: 'var(--mantine-color-gray-0)', border: '1px solid var(--mantine-color-gray-2)' }}>
              <Code block style={{ fontSize: '11px' }}>
                {output ? JSON.stringify(output, null, 2) : '// Message filtered or empty'}
              </Code>
            </ScrollArea.Autosize>
          </Stack>
          
          <Text size="10px" c="dimmed" ta="center">
            Note: This is a client-side simulation. Results might slightly differ from the Go engine.
          </Text>
        </Stack>
      </Card>
    );
  };

  const renderFieldExplorer = () => {
    const [search, setSearch] = useState('');
    const filtered = availableFields.filter(f => f.toLowerCase().includes(search.toLowerCase()));

    return (
      <Stack gap="xs">
        <TextInput 
          placeholder="Search fields..." 
          size="xs" 
          leftSection={<IconSearch size="0.8rem" />}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <ScrollArea h={300} type="auto">
          <Stack gap={4}>
            {filtered.map(field => (
              <Group 
                key={field} 
                justify="space-between" 
                wrap="nowrap" 
                p={4} 
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData('text/plain', `source.${field}`);
                  e.dataTransfer.effectAllowed = 'copy';
                }}
                style={{ borderRadius: 4, background: 'var(--mantine-color-blue-0)', border: '1px dashed var(--mantine-color-blue-2)', cursor: 'grab' }}
              >
                <Box style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  <Text size="xs" fw={500} truncate>{field}</Text>
                  <Text size="10px" c="dimmed" truncate>
                    {JSON.stringify(getValByPath(incomingPayload, field))}
                  </Text>
                </Box>
                <Group gap={4}>
                  <MantineTooltip label="Copy path">
                    <ActionIcon size="xs" variant="subtle" onClick={() => copyToClipboard(field)}>
                      {copiedField === field ? <IconCheck size="0.8rem" color="green" /> : <IconCopy size="0.8rem" />}
                    </ActionIcon>
                  </MantineTooltip>
                  <MantineTooltip label="Add to config">
                    <ActionIcon 
                      size="xs" 
                      variant="subtle" 
                      color="blue"
                      onClick={() => addFromSource(field)}
                    >
                      <IconPlus size="0.8rem" />
                    </ActionIcon>
                  </MantineTooltip>
                </Group>
              </Group>
            ))}
          </Stack>
        </ScrollArea>
      </Stack>
    );
  };

  const renderTargetExplorer = () => {
    const [search, setSearch] = useState('');
    const filtered = targetSchema.filter(f => f.toLowerCase().includes(search.toLowerCase()));

    if (!sinkSchema) {
      return (
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light" py="xs">
          Connect this node to a database sink to see target schema.
        </Alert>
      );
    }

    const onDropToTarget = (e: any, column: string) => {
      e.preventDefault();
      e.currentTarget.style.background = 'var(--mantine-color-green-0)';
      const data = e.dataTransfer.getData('text/plain');
      if (data) {
        updateNodeConfig(selectedNode.id, { [`column.${column}`]: data });
        notifications.show({
          title: 'Field mapped',
          message: `Mapped ${data} to ${column}`,
          color: 'green'
        });
      }
    };

    return (
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="xs" fw={700} c="dimmed">{sinkSchema.config?.table?.toUpperCase() || 'TARGET TABLE'}</Text>
          {loadingTarget && <ActionIcon loading variant="transparent" />}
        </Group>
        <TextInput 
          placeholder="Filter target columns..." 
          size="xs" 
          leftSection={<IconSearch size="0.8rem" />}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <ScrollArea h={300} type="auto">
          <Stack gap={4}>
            {filtered.map(column => (
              <Box 
                key={column} 
                p={6} 
                onDragOver={(e) => { e.preventDefault(); e.currentTarget.style.background = 'var(--mantine-color-green-1)'; }}
                onDragLeave={(e) => { e.currentTarget.style.background = 'var(--mantine-color-green-0)'; }}
                onDrop={(e) => onDropToTarget(e, column)}
                style={{ borderRadius: 4, background: 'var(--mantine-color-green-0)', border: '1px dashed var(--mantine-color-green-3)', cursor: 'default' }}
              >
                <Group justify="space-between" wrap="nowrap">
                   <Box style={{ overflow: 'hidden' }}>
                      <Text size="xs" fw={700} c="green.9" truncate>{column}</Text>
                      <Text size="10px" c="dimmed" truncate>
                         {selectedNode.data[`column.${column}`] || 'Not mapped'}
                      </Text>
                   </Box>
                   {selectedNode.data[`column.${column}`] && (
                      <ActionIcon size="xs" variant="subtle" color="red" onClick={() => {
                         const newData = { ...selectedNode.data };
                         delete newData[`column.${column}`];
                         updateNodeConfig(selectedNode.id, newData, true);
                      }}>
                         <IconTrash size="0.8rem" />
                      </ActionIcon>
                   )}
                </Group>
              </Box>
            ))}
            {filtered.length === 0 && !loadingTarget && (
               <Text size="xs" c="dimmed" ta="center" py="xl">No columns found. Ensure the table exists.</Text>
            )}
          </Stack>
        </ScrollArea>
      </Stack>
    );
  };

  const renderFunctionLibrary = () => {
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

    const [search, setSearch] = useState('');
    const filtered = functions.filter(f => f.name.toLowerCase().includes(search.toLowerCase()) || f.desc.toLowerCase().includes(search.toLowerCase()));

    const addFunctionToConfig = (func: any) => {
      if (transType === 'advanced' || transType === 'set') {
        addField('', func.example);
      } else {
        copyToClipboard(func.example);
        notifications.show({ message: `Example "${func.example}" copied to clipboard.`, color: 'blue' });
      }
    };

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
              <Box key={f.name} p={6} style={{ borderRadius: 4, background: 'var(--mantine-color-orange-0)', cursor: 'pointer' }} onClick={() => addFunctionToConfig(f)}>
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
    <Grid gutter="md" style={{ minHeight: 'calc(100vh - 160px)' }}>
      {/* Column 1: Source Data */}
      <Grid.Col span={{ base: 12, md: 4 }}>
        <Stack gap="md" h="100%">
          <Group gap="xs">
            <IconDatabase size="1.2rem" color="var(--mantine-color-blue-6)" />
            <Text size="sm" fw={700}>1. SOURCE DATA / SAMPLE INPUT</Text>
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
            {renderFieldExplorer()}
          </Card>

          <Card withBorder padding="xs" radius="md">
            <Group justify="space-between" mb="xs">
              <Group gap="xs">
                <IconDatabase size="1rem" color="var(--mantine-color-green-6)" />
                <Text size="xs" fw={700}>TARGET COLUMNS</Text>
              </Group>
              <Badge size="xs" variant="light" color="green">{targetSchema.length}</Badge>
            </Group>
            {renderTargetExplorer()}
          </Card>

          {transType === 'advanced' && renderFunctionLibrary()}

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
      <Grid.Col span={{ base: 12, md: 4 }}>
        <Card withBorder shadow="sm" radius="md" p="md" h="100%">
          <Stack gap="md" h="100%">
            <Group justify="space-between">
              <Group gap="xs">
                <IconSettings size="1.2rem" color="var(--mantine-color-gray-7)" />
                <Text size="sm" fw={700}>2. TRANSFORM LOGIC</Text>
              </Group>
              <Badge variant="dot" color="gray" style={{ textTransform: 'uppercase' }}>{transType}</Badge>
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

          {transType === 'stateful' && (
            <>
              <Select 
                label="Operation" 
                data={['count', 'sum']} 
                value={selectedNode.data.operation || 'count'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { operation: val || 'count' })} 
              />
              <Autocomplete 
                label="Field" 
                placeholder="e.g. amount" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to aggregate. Supports nested objects and arrays."
              />
              <TextInput 
                label="Output Field" 
                placeholder="e.g. total_amount" 
                value={selectedNode.data.outputField || ''} 
                onChange={(e) => updateNodeConfig(selectedNode.id, { outputField: e.target.value })} 
              />
              <Alert icon={<IconInfoCircle size="1rem" />} color="cyan" py="xs">
                <Text size="xs">Stateful nodes maintain an internal state (e.g. a counter or a sum) across all messages.</Text>
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
              {renderFilterEditor()}
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

          {transType === 'mask' && (
            <>
              <Autocomplete 
                label="Field" 
                placeholder="e.g. email" 
                data={availableFields}
                value={selectedNode.data.field || ''} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { field: val })} 
                description="Field to mask. Supports nested objects and arrays."
              />
              <Select 
                label="Mask Type" 
                data={['all', 'partial', 'email']} 
                value={selectedNode.data.maskType || 'all'} 
                onChange={(val) => updateNodeConfig(selectedNode.id, { maskType: val || 'all' })} 
              />
            </>
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
                    data={(sources || [])
                      .filter((s: any) => ['postgres', 'mysql', 'mssql', 'sqlite', 'mariadb', 'oracle', 'db2', 'mongodb', 'yugabyte', 'clickhouse'].includes(s.type))
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
                </Stack>
              </Tabs.Panel>

              <Tabs.Panel value="advanced">
                <Stack gap="sm">
                  <TextInput
                    label="Where Clause (SQL or MongoDB JSON)"
                    placeholder="e.g. status = 'active' AND id = {{user_id}}"
                    value={selectedNode.data.whereClause || ''}
                    onChange={(e) => updateNodeConfig(selectedNode.id, { whereClause: e.target.value })}
                    description="Overrides Table/Key Column if provided. Supports {{field}} templates."
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
      <Grid.Col span={{ base: 12, md: 4 }}>
        {renderLivePreview()}
      </Grid.Col>
    </Grid>
  );
}
