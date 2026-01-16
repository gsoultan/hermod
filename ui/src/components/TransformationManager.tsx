import { useState } from 'react';
import { Button, Group, TextInput, Stack, Divider, Text, Paper, ActionIcon, Checkbox, Box, Accordion, List, ThemeIcon, Select, Drawer, Tooltip, Textarea, JsonInput, Autocomplete, Menu, Badge, Code, ScrollArea, Modal, Timeline, Grid } from '@mantine/core';
import { IconTrash, IconPlus, IconInfoCircle, IconArrowsDiff, IconFilter, IconWand, IconWorld, IconDatabase, IconFilterCode, IconTableImport, IconChevronUp, IconChevronDown, IconSearch, IconClick, IconEyeOff, IconDeviceAnalytics, IconAlertTriangle, IconCircleCheck, IconCode, IconCheck } from '@tabler/icons-react';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '../api';

interface TransformationManagerProps {
  transformations: any[];
  onChange: (transformations: any[]) => void;
  title: string;
  sampleMessage?: string;
  onSampleMessageChange?: (value: string) => void;
}

const updateConfigKey = (config: any, oldKey: string, newKey: string, value: any) => {
  const newConfig: any = {};
  Object.keys(config).forEach(key => {
    if (key === oldKey) {
      newConfig[newKey] = value;
    } else {
      newConfig[key] = config[key];
    }
  });
  return newConfig;
};

const flattenObject = (obj: any, prefix = ''): Record<string, any> => {
  if (!obj || typeof obj !== 'object') return {};
  return Object.keys(obj).reduce((acc: any, k: string) => {
    const pre = prefix.length ? prefix + '.' : '';
    if (obj[k] !== null && typeof obj[k] === 'object' && !Array.isArray(obj[k])) {
      Object.assign(acc, flattenObject(obj[k], pre + k));
    } else {
      acc[pre + k] = obj[k];
    }
    return acc;
  }, {});
};

export function TransformationManager({ transformations, onChange, title, sampleMessage, onSampleMessageChange }: TransformationManagerProps) {
  const [opened, { open, close }] = useDisclosure(false);
  const [pipelineModalOpened, { open: openPipelineModal, close: closePipelineModal }] = useDisclosure(false);
  const [mappingSearch, setMappingSearch] = useState<Record<number, string>>({});
  const [previews, setPreviews] = useState<Record<number, any>>({});
  const [loadingPreview, setLoadingPreview] = useState<Record<number, boolean>>({});
  const [showPreview, setShowPreview] = useState<Record<number, boolean>>({});
  const [pipelineResults, setPipelineResults] = useState<any[]>([]);
  const [loadingPipeline, setLoadingPipeline] = useState(false);
  const [paletteSearch, setPaletteSearch] = useState('');

  const testPipeline = async () => {
    if (!sampleMessage) {
      notifications.show({
        title: 'No sample message',
        message: 'Please provide a sample message first.',
        color: 'orange'
      });
      return;
    }

    setLoadingPipeline(true);
    openPipelineModal();
    try {
      const msg = JSON.parse(sampleMessage);
      const res = await apiFetch('/api/transformations/test-pipeline', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transformations: transformations.map(t => ({
            type: t.type,
            config: typeof t.config === 'string' ? JSON.parse(t.config) : t.config
          })),
          message: msg
        })
      });

      if (res.ok) {
        const results = await res.json();
        setPipelineResults(results);
      } else {
        const err = await res.json();
        notifications.show({
          title: 'Pipeline Error',
          message: err.error || 'Failed to test pipeline',
          color: 'red'
        });
        closePipelineModal();
      }
    } catch (e: any) {
      notifications.show({
        title: 'Error',
        message: e.message,
        color: 'red'
      });
      closePipelineModal();
    } finally {
      setLoadingPipeline(false);
    }
  };

  const testStep = async (idx: number) => {
    if (!sampleMessage) {
      notifications.show({
        title: 'No sample message',
        message: 'Please provide a sample message first.',
        color: 'orange'
      });
      return;
    }
    
    setLoadingPreview(prev => ({ ...prev, [idx]: true }));
    try {
      const msg = JSON.parse(sampleMessage);
      const transformation = transformations[idx];
      
      const res = await apiFetch('/api/transformations/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transformation: {
            type: transformation.type,
            config: typeof transformation.config === 'string' ? JSON.parse(transformation.config) : transformation.config
          },
          message: msg
        })
      });
      
      if (res.ok) {
        const result = await res.json();
        setPreviews(prev => ({ ...prev, [idx]: result }));
        setShowPreview(prev => ({ ...prev, [idx]: true }));
      } else {
        const err = await res.json();
        notifications.show({
          title: 'Transformation Error',
          message: err.error || 'Failed to test transformation',
          color: 'red'
        });
      }
    } catch (e: any) {
      notifications.show({
        title: 'Error',
        message: e.message,
        color: 'red'
      });
    } finally {
      setLoadingPreview(prev => ({ ...prev, [idx]: false }));
    }
  };

  const { fieldPaths, flattenedSampleData } = (() => {
    if (!sampleMessage) return { fieldPaths: [], flattenedSampleData: null };
    try {
      const msg = JSON.parse(sampleMessage);
      const afterStr = msg.after;
      const beforeStr = msg.before;
      let data: any = null;
      if (afterStr) {
        try {
          const parsed = typeof afterStr === 'string' ? JSON.parse(afterStr) : afterStr;
          if (parsed && typeof parsed === 'object') data = parsed;
        } catch (e) {}
      }
      if (!data && beforeStr) {
        try {
          const parsed = typeof beforeStr === 'string' ? JSON.parse(beforeStr) : beforeStr;
          if (parsed && typeof parsed === 'object') data = parsed;
        } catch (e) {}
      }
      if (!data) return { fieldPaths: [], flattenedSampleData: null };
      const flattened = flattenObject(data);
      const systemFields: Record<string, string> = {
        'system.table': msg.table || '',
        'system.schema': msg.schema || '',
        'system.operation': msg.operation || '',
        'system.id': msg.id || ''
      };
      return { 
        fieldPaths: [...Object.keys(flattened), ...Object.keys(systemFields)],
        flattenedSampleData: { ...flattened, ...systemFields }
      };
    } catch (e) {
      return { fieldPaths: [], flattenedSampleData: null };
    }
  })();

  const moveTransformation = (index: number, direction: 'up' | 'down') => {
    const next = [...transformations];
    const newIndex = direction === 'up' ? index - 1 : index + 1;
    if (newIndex < 0 || newIndex >= next.length) return;
    const temp = next[index];
    next[index] = next[newIndex];
    next[newIndex] = temp;
    onChange(next);
  };

  const importFromSample = (idx: number) => {
    if (!sampleMessage) {
      notifications.show({
        title: 'No sample message',
        message: 'Please provide a sample message in the playground below first.',
        color: 'orange'
      });
      return;
    }
    try {
      const msg = JSON.parse(sampleMessage);
      const afterStr = msg.after;
      const beforeStr = msg.before;
      
      let data: any = null;
      
      // Try parsing 'after' first
      if (afterStr) {
        try {
          const parsed = typeof afterStr === 'string' ? JSON.parse(afterStr) : afterStr;
          if (parsed && typeof parsed === 'object' && Object.keys(parsed).length > 0) {
            data = parsed;
          }
        } catch (e) {
          console.warn("Failed to parse 'after' payload", e);
        }
      }
      
      // If 'after' is empty or invalid, try 'before'
      if (!data && beforeStr) {
        try {
          const parsed = typeof beforeStr === 'string' ? JSON.parse(beforeStr) : beforeStr;
          if (parsed && typeof parsed === 'object' && Object.keys(parsed).length > 0) {
            data = parsed;
          }
        } catch (e) {
          console.warn("Failed to parse 'before' payload", e);
        }
      }

      if (!data) {
        notifications.show({
          title: 'No data to import',
          message: 'The sample message does not contain a valid "after" or "before" JSON payload.',
          color: 'orange'
        });
        return;
      }

      const flattened = flattenObject(data);
      const next = [...transformations];
      const type = next[idx].type;
      const prefix = type === 'mapping' ? 'map.' : 'column.';
      const newConfig = { ...next[idx].config };
      
      let importedCount = 0;
      Object.keys(flattened).forEach(key => {
        const configKey = `${prefix}${key}`;
        if (!(configKey in newConfig)) {
          newConfig[configKey] = type === 'mapping' ? key : `source.${key}`;
          importedCount++;
        }
      });
      
      if (importedCount === 0) {
        notifications.show({
          title: 'No new fields',
          message: 'All fields from the sample are already present in the configuration.',
          color: 'blue'
        });
        return;
      }

      next[idx].config = newConfig;
      onChange(next);
      notifications.show({
        title: 'Import Successful',
        message: `Imported ${importedCount} fields from the sample message.`,
        color: 'green'
      });
    } catch (e) {
      console.error("Failed to parse sample message", e);
      notifications.show({
        title: 'Import Error',
        message: 'Failed to parse the sample message. Make sure it is valid JSON.',
        color: 'red'
      });
    }
  };

  const clearFields = (idx: number) => {
    const next = [...transformations];
    const type = next[idx].type;
    const prefix = type === 'mapping' ? 'map.' : 'column.';
    const newConfig = { ...next[idx].config };
    
    Object.keys(newConfig).forEach(k => {
      if (k.startsWith(prefix)) {
        delete newConfig[k];
      }
    });
    
    next[idx].config = newConfig;
    onChange(next);
  };

  const removeSubConfig = (idx: number, prefix: string) => {
    const next = [...transformations];
    const newConfig = { ...next[idx].config };
    Object.keys(newConfig).forEach(k => {
      if (k.startsWith(prefix + '.')) {
        delete newConfig[k];
      }
    });
    next[idx].config = newConfig;
    onChange(next);
  };

  const getTransformationInfo = (type: string) => {
    switch (type) {
      case 'filter_data':
        return {
          title: 'Message Filter',
          description: 'Filter by operation type and/or data content.',
          icon: <IconFilter size="0.9rem" />,
          color: 'red',
          isFilter: true
        };
      case 'mapping':
        return {
          title: 'Field Mapping',
          description: 'Rename or drop specific fields.',
          icon: <IconArrowsDiff size="0.9rem" />,
          color: 'blue',
          isFilter: false
        };
      case 'advanced':
        return {
          title: 'Advanced Mapper',
          description: 'Complex expressions and system fields.',
          icon: <IconWand size="0.9rem" />,
          color: 'indigo',
          isFilter: false
        };
      case 'http':
        return {
          title: 'HTTP Enrichment',
          description: 'Fetch data from an external API.',
          icon: <IconWorld size="0.9rem" />,
          color: 'teal',
          isFilter: false
        };
      case 'sql':
        return {
          title: 'SQL Enrichment',
          description: 'Query an external database.',
          icon: <IconDatabase size="0.9rem" />,
          color: 'cyan',
          isFilter: false
        };
      case 'lua':
        return {
          title: 'Lua Script',
          description: 'Custom logic using Lua programming.',
          icon: <IconFilterCode size="0.9rem" />,
          color: 'violet',
          isFilter: false // Lua can filter, but it's primarily a modifier
        };
      case 'schema':
        return {
          title: 'JSON Schema',
          description: 'Validate message against a schema.',
          icon: <IconInfoCircle size="0.9rem" />,
          color: 'yellow',
          isFilter: true
        };
      case 'validator':
        return {
          title: 'Data Validator',
          description: 'Ensure data quality with rules.',
          icon: <IconWand size="0.9rem" />,
          color: 'pink',
          isFilter: true
        };
      default:
        return {
          title: type,
          description: '',
          icon: <IconInfoCircle size="0.9rem" />,
          color: 'gray',
          isFilter: false
        };
    }
  };

  const addTransformation = (type: string) => {
    let config = {};
    if (type === 'filter_data') {
      config = { 
        create: 'true', update: 'true', delete: 'true', snapshot: 'true',
        field: '', operator: '=', value: '' 
      };
    } else if (type === 'mapping') {
      config = {};
    } else if (type === 'advanced') {
      config = {};
    } else if (type === 'http') {
      config = { url: '', method: 'GET', max_retries: '0' };
    } else if (type === 'sql') {
      config = { driver: 'postgres', conn: '', query: '' };
    } else if (type === 'lua') {
      config = { script: '-- msg.after = json.encode(after)\nreturn msg' };
    } else if (type === 'schema') {
      config = { schema: '{\n  "type": "object"\n}' };
    } else if (type === 'validator') {
      config = { 'rule.0.field': '', 'rule.0.type': 'not_null', 'rule.0.severity': 'fail' };
    }
    
    onChange([...transformations, { type, config, on_failure: 'fail' }]);
  };

  const removeTransformation = (index: number) => {
    const next = [...transformations];
    next.splice(index, 1);
    onChange(next);
  };

  const updateTransformationConfig = (index: number, configKey: string, value: string) => {
    const next = [...transformations];
    next[index] = {
      ...next[index],
      config: { ...next[index].config, [configKey]: value }
    };
    onChange(next);
  };

  return (
    <Stack gap="md">
      <Drawer
        opened={opened}
        onClose={close}
        title="Transformation Instructions"
        position="right"
        size="md"
      >
        <Stack gap="md">
          <Text size="sm">Transformations allow you to modify or filter messages as they flow from source to sinks.</Text>

          <Paper withBorder p="xs" radius="md" bg="blue.0">
            <Group gap="xs" wrap="nowrap" align="flex-start">
              <IconInfoCircle size="1.2rem" color="blue" style={{ marginTop: '2px' }} />
              <Stack gap={4}>
                <Text size="sm" fw={700} c="blue">Universal JSON Format</Text>
                <Text size="xs" c="blue.9">
                  Hermod treats all data as <b>Flat JSON</b>. Whether your source is a CDC database (Postgres/MySQL) or a generic JSON source (RabbitMQ/CSV), you can access fields directly by their names (e.g. <code>status</code>).
                </Text>
                <Text size="xs" c="blue.9">
                  For CDC sources, <code>before</code> state is available as a nested object (e.g. <code>before.status</code>).
                </Text>
              </Stack>
            </Group>
          </Paper>
          
          <Accordion variant="separated">

            <Accordion.Item value="filter">
              <Accordion.Control icon={<IconFilter size="1rem" color="red" />}>Message Filter</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs" mb="xs">Keeps messages only if they meet both operation and data criteria. If any condition is false, the message is dropped.</Text>
                <List size="xs">
                  <List.Item><b>Operations</b>: Check the operation types (Create, Update, Delete, Snapshot) you want to allow. Unchecked operations are dropped.</List.Item>
                  <List.Item><b>Field Path</b>: The JSON field to check (e.g., <code>status</code> or <code>user.profile.age</code>). Supports dot notation. Use <code>system.operation</code> to filter by operation type.</List.Item>
                  <List.Item><b>Operator</b>: Comparison method (Equals, GT, Regex, Exists, etc.).</List.Item>
                  <List.Item><b>Value</b>: The expected value.</List.Item>
                </List>
                <Text size="xs" mt="sm" color="dimmed" fs="italic">Example: Allow only 'Create' operations where 'status == completed'.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="schema">
              <Accordion.Control icon={<IconCode size="1rem" color="orange" />}>JSON Schema</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Validates the message payload against a standard JSON Schema. Useful for ensuring data quality before it reaches your sinks.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="validator">
              <Accordion.Control icon={<IconCheck size="1rem" color="green" />}>Data Validator</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">A simpler alternative to JSON Schema. Define multiple rules for fields (type checks, ranges, regex, non-null) and decide whether to skip, warn, or fail on violation.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="mapping">
              <Accordion.Control icon={<IconArrowsDiff size="1rem" color="blue" />}>Field Mapping</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs" mb="xs">Simple field renaming and selection.</Text>
                <List size="xs">
                  <List.Item><b>Strict Mode</b>: If enabled, only specified fields are kept. Original data and CDC metadata (like <code>before</code> state) are cleared, producing a clean <b>New Object</b>. If disabled, all original fields are preserved.</List.Item>
                  <List.Item><b>Source Field</b>: The name of the field in the incoming message. Supports dot notation for nested data (e.g., <code>user.id</code>).</List.Item>
                  <List.Item><b>Target Field</b>: The new name for the field. You can also use dot notation to create nested structures in the output (e.g., <code>profile.uid</code>).</List.Item>
                </List>
                <Text size="xs" mt="sm" color="dimmed" fs="italic">Tip: Use "Import from Sample" to quickly populate fields from your test message.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="advanced">
              <Accordion.Control icon={<IconWand size="1rem" color="indigo" />}>Advanced Mapping</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs" mb="xs">Powerful expression-based mapper.</Text>
                <List size="xs">
                  <List.Item><b>Strict Mode</b>: If enabled, only fields defined in the mapping are produced, clearing any original data or CDC metadata (<b>New Object</b>). If disabled, original fields are kept.</List.Item>
                  <List.Item><code>path</code>: Extract value from source (supports dot notation, e.g., <code>user.name</code> or <code>source.user.name</code>).</List.Item>
                  <List.Item><code>system.now</code>: Current timestamp (RFC3339).</List.Item>
                  <List.Item><code>system.uuid</code>: New random UUID.</List.Item>
                  <List.Item><code>const.value</code>: Inject a constant value (e.g., <code>const.true</code>, <code>const.100</code>).</List.Item>
                  <List.Item><b>Functions</b>: <code>upper(arg)</code>, <code>lower(arg)</code>, <code>trim(arg)</code>, <code>concat(a, b, ...)</code>, <code>substring(str, start, len)</code>, <code>replace(str, old, new)</code>, <code>abs(n)</code>, <code>coalesce(v1, v2, ...)</code>, <code>default(val, def)</code>.</List.Item>
                  <List.Item><b>Date/Time</b>: <code>date_format(val, layout)</code>, <code>date_parse(val, layout)</code>, <code>unix_now()</code>.</List.Item>
                  <List.Item><b>Encoding</b>: <code>base64_encode(v)</code>, <code>base64_decode(v)</code>, <code>url_encode(v)</code>, <code>url_decode(v)</code>.</List.Item>
                  <List.Item><b>Math</b>: <code>add(a, b, ...)</code>, <code>sub(a, b)</code>, <code>mul(a, b, ...)</code>, <code>div(a, b)</code>, <code>round(n)</code>, <code>floor(n)</code>, <code>ceil(n)</code>, <code>min(a, b, ...)</code>, <code>max(a, b, ...)</code>.</List.Item>
                  <List.Item><b>Logic</b>: <code>if(cond, then, else)</code>, <code>and(a, b, ...)</code>, <code>or(a, b, ...)</code>, <code>not(a)</code>, <code>eq(a, b)</code>, <code>neq(a, b)</code>, <code>gt(a, b)</code>, <code>lt(a, b)</code>, <code>gte(a, b)</code>, <code>lte(a, b)</code>.</List.Item>
                  <List.Item><b>Security & Misc</b>: <code>sha256(str)</code>, <code>md5(str)</code>, <code>split(str, sep)</code>, <code>join(arr, sep)</code>.</List.Item>
                  <List.Item><b>Type Casting</b>: <code>to_string(v)</code>, <code>to_int(v)</code>, <code>to_float(v)</code>.</List.Item>
                  <List.Item><i>Literal</i>: Any string without a prefix is treated as a literal value.</List.Item>
                </List>
                <Text size="xs" mt="sm" color="dimmed" fs="italic">Example: column.status: if(gt(source.price, const.100), expensive, cheap)</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="enrichment">
              <Accordion.Control icon={<IconWorld size="1rem" color="teal" />}>Enrichment (HTTP/SQL)</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Fetch additional data from external APIs or databases. Use <code>{`{field}`}</code> in HTTP URLs or <code>:field</code> in SQL queries to inject message data.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="lua">
              <Accordion.Control icon={<IconFilterCode size="1rem" color="blue" />}>Lua Script</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Execute custom Lua scripts for highly complex transformations that can't be achieved with standard mappers. Access and modify <code>msg.after</code>, <code>msg.table</code>, etc. directly.</Text>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        </Stack>
      </Drawer>

      <Group align="flex-start" gap="md" wrap="nowrap">
        {/* Left Side Panel - Menu */}
        <Paper withBorder p="md" radius="md" style={{ width: '240px', flexShrink: 0, position: 'sticky', top: '20px' }}>
          <Stack gap="xs">
            <Group justify="space-between" mb="xs">
              <Text fw={700} size="sm">Pipeline Palette</Text>
              <Tooltip label="How to use">
                <ActionIcon variant="subtle" color="gray" size="sm" onClick={open}>
                  <IconInfoCircle size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            </Group>
            
            <Divider mb="sm" />
            
            <TextInput 
              placeholder="Search components..." 
              size="xs" 
              mb="md"
              leftSection={<IconSearch size="0.8rem" />}
              value={paletteSearch}
              onChange={(e) => setPaletteSearch(e.currentTarget.value)}
            />
            
            {[
              { label: 'Filter & Routing', items: ['filter_data', 'schema', 'validator'] },
              { label: 'Transform & Map', items: ['mapping', 'advanced'] },
              { label: 'Enrichment', items: ['http', 'sql'] },
              { label: 'Custom Logic', items: ['lua'] }
            ].map((cat, catIdx) => {
              const filteredItems = cat.items.filter(type => {
                const info = getTransformationInfo(type);
                return info.title.toLowerCase().includes(paletteSearch.toLowerCase()) || 
                       info.description.toLowerCase().includes(paletteSearch.toLowerCase());
              });

              if (filteredItems.length === 0) return null;

              return (
                <Box key={catIdx} mb="sm">
                  <Text size="xs" fw={700} c="dimmed" mb={8} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{cat.label}</Text>
                  <Stack gap={6}>
                    {filteredItems.map(type => {
                      const info = getTransformationInfo(type);
                      return (
                        <Button 
                          key={type}
                          fullWidth 
                          justify="flex-start" 
                          size="xs" 
                          variant="light" 
                          color={info.color} 
                          leftSection={info.icon} 
                          onClick={() => addTransformation(type)}
                          styles={{
                            inner: { justifyContent: 'flex-start' },
                            label: { fontSize: '12px' }
                          }}
                        >
                          {info.title}
                        </Button>
                      );
                    })}
                  </Stack>
                </Box>
              );
            })}

            <Box mt="md">
              <Text size="xs" fw={700} c="dimmed" mb={8} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Quick Templates</Text>
              <Stack gap={6}>
                <Button 
                  variant="outline" 
                  color="gray" 
                  size="xs" 
                  fullWidth 
                  justify="flex-start"
                  leftSection={<IconWand size="1rem" />}
                  onClick={() => {
                    const next = [...transformations, {
                      type: 'advanced',
                      config: { 'column.email': 'trim(lower(source.email))' },
                      on_failure: 'fail'
                    }];
                    onChange(next);
                  }}
                >
                  Normalize Email
                </Button>
                <Button 
                  variant="outline" 
                  color="gray" 
                  size="xs" 
                  fullWidth 
                  justify="flex-start"
                  leftSection={<IconWand size="1rem" />}
                  onClick={() => {
                    const next = [...transformations, {
                      type: 'advanced',
                      config: { 'column.id': 'system.uuid', 'column.created_at': 'system.now' },
                      on_failure: 'fail'
                    }];
                    onChange(next);
                  }}
                >
                  Standard Metadata
                </Button>
              </Stack>
            </Box>
          </Stack>
        </Paper>

        {/* Right Side Panel - Transformation Stage */}
        <Stack style={{ flex: 1 }} gap="md">
          <Paper p="sm" withBorder bg="gray.0" radius="md">
            <Group justify="space-between">
              <Stack gap={0}>
                <Text fw={700} size="sm">{title || 'Transformation Stage'}</Text>
                {transformations.length > 0 && (
                  <Text size="xs" c="dimmed">{transformations.length} step(s) configured</Text>
                )}
              </Stack>
              {transformations.length > 0 && (
                <Button 
                  size="xs" 
                  variant="filled" 
                  color="indigo" 
                  leftSection={<IconDeviceAnalytics size="1rem" />}
                  onClick={testPipeline}
                  loading={loadingPipeline}
                >
                  Visualize Pipeline
                </Button>
              )}
            </Group>
          </Paper>

          {transformations.length === 0 && (
            <Paper withBorder p="xl" radius="md" style={{ borderStyle: 'dashed', backgroundColor: 'transparent' }}>
              <Stack align="center" gap="xs" py="xl">
                <IconWand size="2rem" color="gray" style={{ opacity: 0.5 }} />
                <Text size="sm" c="dimmed">No transformations added to this stage.</Text>
                <Text size="xs" c="dimmed">Select a component from the left to begin.</Text>
              </Stack>
            </Paper>
          )}

          {transformations.map((t, idx) => {
            const info = getTransformationInfo(t.type);
            return (
              <Box key={idx} style={{ position: 'relative' }}>
                {/* Connecting Line */}
                {idx < transformations.length - 1 && (
                  <>
                    <Box 
                      style={{ 
                        position: 'absolute', 
                        left: '32px', 
                        top: '40px', 
                        bottom: '-20px', 
                        width: '2px', 
                        zIndex: 0,
                        borderLeft: '2px dashed var(--mantine-color-gray-4)'
                      }} 
                    />
                    <Box
                      style={{
                        position: 'absolute',
                        left: '26px',
                        bottom: '-25px',
                        zIndex: 1,
                        color: 'var(--mantine-color-gray-4)'
                      }}
                    >
                      <IconChevronDown size="14" stroke={3} />
                    </Box>
                  </>
                )}

                <Paper p="md" withBorder radius="md" shadow="sm" mb={idx < transformations.length - 1 ? "lg" : 0} style={{ position: 'relative', zIndex: 1, backgroundColor: 'white' }}>
                  <Group justify="space-between" mb="md">
                    <Group gap="sm">
                      <Badge variant="light" color="gray" size="lg" radius="xl" style={{ width: 32, height: 32, padding: 0 }}>
                        {idx + 1}
                      </Badge>
                      <ThemeIcon size="lg" variant="light" radius="md" color={info.color}>
                        {info.icon}
                      </ThemeIcon>
                      <Box>
                        <Group gap="xs">
                          <Text size="sm" fw={700}>
                            {info.title}
                          </Text>
                          {info.isFilter ? (
                            <Badge color="red" variant="outline" size="xs">Filter</Badge>
                          ) : (
                            <Badge color="blue" variant="outline" size="xs">Modifier</Badge>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed">{info.description}</Text>
                      </Box>
                    </Group>
                    <Group gap={4}>
                      <Tooltip label="Test Step">
                        <ActionIcon 
                          size="md" 
                          variant="light" 
                          color="blue" 
                          onClick={() => testStep(idx)}
                          loading={loadingPreview[idx]}
                        >
                          <IconWand size="1.2rem" />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label="Move Up">
                        <ActionIcon size="md" variant="subtle" color="gray" disabled={idx === 0} onClick={() => moveTransformation(idx, 'up')}>
                          <IconChevronUp size="1.2rem" />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label="Move Down">
                        <ActionIcon size="md" variant="subtle" color="gray" disabled={idx === transformations.length - 1} onClick={() => moveTransformation(idx, 'down')}>
                          <IconChevronDown size="1.2rem" />
                        </ActionIcon>
                      </Tooltip>
                      <Divider orientation="vertical" mx={4} h={20} />
                      <ActionIcon size="md" color="red" variant="subtle" onClick={() => removeTransformation(idx)}>
                        <IconTrash size="1.2rem" />
                      </ActionIcon>
                    </Group>
                  </Group>

                  <Box pl={44}>

                    {(t.type === 'filter_data' || t.type === 'filter_operation') && (
                      <Stack gap="xs">
                        <Group gap="xl" py="xs">
                          <Checkbox label="Create" size="xs" checked={t.config.create === 'true'} onChange={(e) => updateTransformationConfig(idx, 'create', e.currentTarget.checked ? 'true' : 'false')} />
                          <Checkbox label="Update" size="xs" checked={t.config.update === 'true'} onChange={(e) => updateTransformationConfig(idx, 'update', e.currentTarget.checked ? 'true' : 'false')} />
                          <Checkbox label="Delete" size="xs" checked={t.config.delete === 'true'} onChange={(e) => updateTransformationConfig(idx, 'delete', e.currentTarget.checked ? 'true' : 'false')} />
                          <Checkbox label="Snapshot" size="xs" checked={t.config.snapshot === 'true'} onChange={(e) => updateTransformationConfig(idx, 'snapshot', e.currentTarget.checked ? 'true' : 'false')} />
                        </Group>

                        <Divider variant="dashed" label="Data Condition (Optional)" labelPosition="center" />

                        <Group grow gap="xs">
                          <Autocomplete 
                            label="Field Path" 
                            placeholder="Select or type field path" 
                            size="xs" 
                            data={fieldPaths} 
                            value={t.config.field} 
                            onChange={(val) => updateTransformationConfig(idx, 'field', val)} 
                            rightSection={fieldPaths.length > 0 && (
                              <Menu shadow="md" width={200}>
                                <Menu.Target>
                                  <ActionIcon size="sm" variant="subtle" color="gray">
                                    <IconClick size="1rem" />
                                  </ActionIcon>
                                </Menu.Target>
                                <Menu.Dropdown>
                                  <Menu.Label>Available Fields</Menu.Label>
                                  <Box style={{ maxHeight: '200px', overflowY: 'auto' }}>
                                    {fieldPaths.map(path => (
                                      <Menu.Item key={path} onClick={() => updateTransformationConfig(idx, 'field', path)}>
                                        {path}
                                      </Menu.Item>
                                    ))}
                                  </Box>
                                </Menu.Dropdown>
                              </Menu>
                            )}
                          />
                          <Select label="Operator" size="xs" data={[
                            { value: '=', label: 'Equals (=)' },
                            { value: '!=', label: 'Not Equals (!=)' },
                            { value: '>', label: 'Greater Than (>)' },
                            { value: '<', label: 'Less Than (<)' },
                            { value: 'contains', label: 'Contains' },
                            { value: 'starts_with', label: 'Starts With' },
                            { value: 'ends_with', label: 'Ends With' },
                            { value: 'regex', label: 'Regex Match' },
                            { value: 'exists', label: 'Exists' },
                            { value: 'not_exists', label: 'Not Exists' },
                            { value: 'is_null', label: 'Is Null' },
                            { value: 'is_not_null', label: 'Is Not Null' },
                          ]} value={t.config.operator || '='} onChange={(val) => updateTransformationConfig(idx, 'operator', val || '=')} />
                          <Autocomplete 
                            label="Value" 
                            placeholder="Expected value" 
                            size="xs" 
                            value={t.config.value} 
                            data={t.config.field && flattenedSampleData && (flattenedSampleData as any)[t.config.field] !== undefined ? [String((flattenedSampleData as any)[t.config.field])] : []}
                            onChange={(val) => updateTransformationConfig(idx, 'value', val)} 
                            disabled={['exists', 'not_exists', 'is_null', 'is_not_null'].includes(t.config.operator)}
                          />
                        </Group>
                        <Text size="10px" c="dimmed" fs="italic">
                          Tip: Message will pass if <b>both</b> operation and data condition match. Leave field empty to filter by operation only.
                        </Text>
                      </Stack>
                    )}
          
          {t.type === 'http' && (
            <Stack gap="xs">
              <Group grow>
                <TextInput label="URL" placeholder="https://api.example.com/users/{id}" size="xs" value={t.config.url} onChange={(e) => updateTransformationConfig(idx, 'url', e.target.value)} />
                <Select label="Method" size="xs" data={['GET', 'POST', 'PUT', 'PATCH']} value={t.config.method} onChange={(val) => updateTransformationConfig(idx, 'method', val || 'GET')} />
                <TextInput label="Max Retries" size="xs" type="number" value={t.config.max_retries} onChange={(e) => updateTransformationConfig(idx, 'max_retries', e.target.value)} />
              </Group>
              <Box>
                <Text size="xs" fw={500} mb={4}>Headers</Text>
                {Object.entries(t.config).filter(([k]) => k.startsWith('header.')).map(([k, v], fieldIdx) => (
                  <Group key={fieldIdx} grow gap="xs" mb="xs">
                    <TextInput size="xs" placeholder="Header Name" value={k.replace('header.', '')} 
                      onChange={(e) => {
                        const newKey = `header.${e.target.value}`;
                        const next = [...transformations];
                        next[idx].config = updateConfigKey(next[idx].config, k, newKey, v);
                        onChange(next);
                      }}
                    />
                    <TextInput size="xs" placeholder="Value" value={v as string} onChange={(e) => updateTransformationConfig(idx, k, e.target.value)} />
                    <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                      const next = [...transformations];
                      const newConfig = { ...next[idx].config };
                      delete newConfig[k];
                      next[idx].config = newConfig;
                      onChange(next);
                    }}>
                      <IconTrash size="0.8rem" />
                    </ActionIcon>
                  </Group>
                ))}
                <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size="0.8rem" />} onClick={() => updateTransformationConfig(idx, 'header.Authorization', 'Bearer ')}>Add Header</Button>
              </Box>
            </Stack>
          )}

          {t.type === 'sql' && (
            <Stack gap="xs">
              <Group grow>
                <Select label="Driver" size="xs" data={['postgres', 'mysql', 'sqlite', 'sqlserver']} value={t.config.driver} onChange={(val) => updateTransformationConfig(idx, 'driver', val || 'postgres')} />
                <TextInput label="Connection String" placeholder="postgres://user:pass@host:port/db" size="xs" value={t.config.conn} onChange={(e) => updateTransformationConfig(idx, 'conn', e.target.value)} />
              </Group>
              <TextInput label="Query" placeholder="SELECT * FROM profiles WHERE user_id = :id" size="xs" value={t.config.query} onChange={(e) => updateTransformationConfig(idx, 'query', e.target.value)} />
              <Text size="xs" c="dimmed">Use <code>:field_name</code> to inject values from the message.</Text>
            </Stack>
          )}

          {t.type === 'lua' && (
            <Textarea 
              label="Lua Script" 
              placeholder="return msg" 
              size="xs" 
              minRows={4}
              value={t.config.script} 
              onChange={(e) => updateTransformationConfig(idx, 'script', e.target.value)} 
            />
          )}

          {t.type === 'schema' && (
            <JsonInput 
              label="JSON Schema" 
              placeholder='{"type": "object"}' 
              size="xs" 
              minRows={4}
              value={t.config.schema} 
              onChange={(val) => updateTransformationConfig(idx, 'schema', val)}
              validationError="Invalid JSON"
              formatOnBlur
            />
          )}

          {t.type === 'validator' && (
            <Stack gap="xs">
              {Object.keys(t.config).filter(k => k.endsWith('.field')).map((k, rIdx) => {
                const prefix = k.replace('.field', '');
                return (
                  <Paper withBorder p="xs" key={rIdx}>
                    <Stack gap="xs">
                      <Group grow align="flex-end">
                        <Autocomplete
                          label="Field"
                          placeholder="e.g. email"
                          size="xs"
                          data={fieldPaths}
                          value={t.config[`${prefix}.field`]}
                          onChange={(val) => updateTransformationConfig(idx, `${prefix}.field`, val)}
                        />
                        <Select
                          label="Type"
                          size="xs"
                          data={[
                            { value: 'not_null', label: 'Not Null' },
                            { value: 'type', label: 'Type Check' },
                            { value: 'regex', label: 'Regex Match' },
                            { value: 'min', label: 'Min Value' },
                            { value: 'max', label: 'Max Value' },
                            { value: 'min_len', label: 'Min Length' },
                            { value: 'max_len', label: 'Max Length' },
                            { value: 'in', label: 'In List' },
                          ]}
                          value={t.config[`${prefix}.type`]}
                          onChange={(val) => updateTransformationConfig(idx, `${prefix}.type`, val || 'not_null')}
                        />
                        <Select
                          label="Severity"
                          size="xs"
                          data={[
                            { value: 'fail', label: 'Fail Pipeline' },
                            { value: 'warn', label: 'Warn Only' },
                            { value: 'skip', label: 'Skip Message' },
                          ]}
                          value={t.config[`${prefix}.severity`]}
                          onChange={(val) => updateTransformationConfig(idx, `${prefix}.severity`, val || 'fail')}
                        />
                        <ActionIcon color="red" variant="subtle" onClick={() => removeSubConfig(idx, prefix)}>
                          <IconTrash size="1rem" />
                        </ActionIcon>
                      </Group>
                      {t.config[`${prefix}.type`] === 'type' && (
                        <Select
                          label="Expected Type"
                          size="xs"
                          data={['string', 'number', 'boolean']}
                          value={t.config[`${prefix}.config`]}
                          onChange={(val) => updateTransformationConfig(idx, `${prefix}.config`, val || '')}
                        />
                      )}
                      {t.config[`${prefix}.type`] === 'regex' && (
                        <TextInput
                          label="Regex Pattern"
                          placeholder="^[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}$"
                          size="xs"
                          value={t.config[`${prefix}.config`]}
                          onChange={(e) => updateTransformationConfig(idx, `${prefix}.config`, e.target.value)}
                        />
                      )}
                      {['min', 'max', 'min_len', 'max_len'].includes(t.config[`${prefix}.type`]) && (
                        <TextInput
                          label="Limit Value"
                          placeholder="e.g. 10"
                          size="xs"
                          type="number"
                          value={t.config[`${prefix}.config`]}
                          onChange={(e) => updateTransformationConfig(idx, `${prefix}.config`, e.target.value)}
                        />
                      )}
                      {t.config[`${prefix}.type`] === 'in' && (
                        <TextInput
                          label="Allowed Values (comma separated)"
                          placeholder="e.g. active,pending,closed"
                          size="xs"
                          value={t.config[`${prefix}.config`]}
                          onChange={(e) => updateTransformationConfig(idx, `${prefix}.config`, e.target.value)}
                        />
                      )}
                    </Stack>
                  </Paper>
                );
              })}
              <Button
                leftSection={<IconPlus size="1rem" />}
                variant="subtle"
                size="xs"
                onClick={() => {
                  const currentRules = Object.keys(t.config).filter(k => k.endsWith('.field'));
                  let maxIdx = -1;
                  currentRules.forEach(rk => {
                    const match = rk.match(/rule\.(\d+)\.field/);
                    if (match) {
                      const val = parseInt(match[1]);
                      if (val > maxIdx) maxIdx = val;
                    }
                  });
                  const nextIdx = maxIdx + 1;
                  const next = [...transformations];
                  next[idx].config = {
                    ...next[idx].config,
                    [`rule.${nextIdx}.field`]: '',
                    [`rule.${nextIdx}.type`]: 'not_null',
                    [`rule.${nextIdx}.severity`]: 'fail'
                  };
                  onChange(next);
                }}
              >
                Add Rule
              </Button>
            </Stack>
          )}

          {(t.type === 'mapping' || t.type === 'advanced') && (
            <Stack gap="xs">
              <Group justify="space-between" mb="xs">
                <Checkbox 
                  label="Strict Mode (Keep only mapped fields)" 
                  size="xs" 
                  checked={t.config.strict !== 'false'} 
                  onChange={(e) => updateTransformationConfig(idx, 'strict', e.currentTarget.checked ? 'true' : 'false')} 
                />
                <Group gap="xs">
                  <Tooltip label="Automatically add fields from the sample message">
                    <Button 
                      size="compact-xs" 
                      variant="light" 
                      color="blue"
                      leftSection={<IconTableImport size="1rem" />}
                      onClick={() => importFromSample(idx)}
                    >
                      Import from Sample
                    </Button>
                  </Tooltip>
                  <Tooltip label="Remove all mapped fields">
                    <Button 
                      size="compact-xs" 
                      variant="light" 
                      color="red"
                      leftSection={<IconTrash size="1rem" />}
                      onClick={() => {
                        if (window.confirm('Are you sure you want to clear all fields?')) {
                          clearFields(idx);
                        }
                      }}
                    >
                      Clear All
                    </Button>
                  </Tooltip>
                </Group>
              </Group>
              <TextInput 
                placeholder="Search fields..." 
                size="xs" 
                leftSection={<IconSearch size="0.8rem" />} 
                value={mappingSearch[idx] || ''}
                onChange={(e) => setMappingSearch({ ...mappingSearch, [idx]: e.target.value })}
              />

              <Box>
                <Group grow gap="xs" mb={4}>
                  <Text size="xs" fw={500} c="dimmed">Source / Target Field</Text>
                  <Text size="xs" fw={500} c="dimmed">{t.type === 'advanced' ? 'Expression' : 'Destination Field'}</Text>
                  <Box style={{ maxWidth: 30 }}></Box>
                </Group>
                {Object.entries(t.config)
                  .filter(([k]) => {
                    const prefix = t.type === 'mapping' ? 'map.' : 'column.';
                    if (!k.startsWith(prefix)) return false;
                    const searchTerm = mappingSearch[idx]?.toLowerCase() || '';
                    if (!searchTerm) return true;
                    const fieldName = k.replace(prefix, '').toLowerCase();
                    const value = String(t.config[k]).toLowerCase();
                    return fieldName.includes(searchTerm) || value.includes(searchTerm);
                  })
                  .map(([k, v], fieldIdx) => (
                  <Group key={fieldIdx} grow gap="xs" mb="xs">
                    {t.type === 'mapping' ? (
                      <Autocomplete 
                        size="xs" 
                        placeholder="source_field"
                        data={fieldPaths}
                        value={k.replace('map.', '')} 
                        onChange={(val) => {
                          const newKey = `map.${val}`;
                          const next = [...transformations];
                          next[idx].config = updateConfigKey(next[idx].config, k, newKey, v);
                          onChange(next);
                        }}
                        rightSection={fieldPaths.length > 0 && (
                          <Menu shadow="md" width={200}>
                            <Menu.Target>
                              <ActionIcon size="xs" variant="subtle" color="gray">
                                <IconClick size="0.8rem" />
                              </ActionIcon>
                            </Menu.Target>
                            <Menu.Dropdown>
                              <Menu.Label>Available Fields</Menu.Label>
                              <Box style={{ maxHeight: '200px', overflowY: 'auto' }}>
                                {fieldPaths.map(path => (
                                  <Menu.Item key={path} onClick={() => {
                                    const newKey = `map.${path}`;
                                    const next = [...transformations];
                                    next[idx].config = updateConfigKey(next[idx].config, k, newKey, v);
                                    onChange(next);
                                  }}>
                                    {path}
                                  </Menu.Item>
                                ))}
                              </Box>
                            </Menu.Dropdown>
                          </Menu>
                        )}
                      />
                    ) : (
                      <TextInput 
                        size="xs" 
                        placeholder="target_field"
                        value={k.replace('column.', '')} 
                        onChange={(e) => {
                          const newKey = `column.${e.target.value}`;
                          const next = [...transformations];
                          next[idx].config = updateConfigKey(next[idx].config, k, newKey, v);
                          onChange(next);
                        }}
                      />
                    )}
                    
                    {t.type === 'advanced' ? (
                      <Autocomplete 
                        size="xs" 
                        placeholder="expression (e.g. upper(source.name))"
                        data={[
                          ...fieldPaths.map(p => `source.${p}`), 
                          'system.now', 'system.uuid', 'system.space', 'system.tab', 'system.newline', 'system.comma', 'system.semicolon',
                          'upper()', 'lower()', 'trim()', 'concat()', 'substring()', 'replace()', 'abs()', 'coalesce()', 'default()',
                          'date_format()', 'date_parse()', 'unix_now()',
                          'base64_encode()', 'base64_decode()', 'url_encode()', 'url_decode()',
                          'to_string()', 'to_int()', 'to_float()',
                          'add()', 'sub()', 'mul()', 'div()', 'round()', 'floor()', 'ceil()', 'min()', 'max()',
                          'if()', 'and()', 'or()', 'not()', 'eq()', 'neq()', 'gt()', 'lt()', 'gte()', 'lte()',
                          'sha256()', 'md5()', 'split()', 'join()',
                          'const.'
                        ]}
                        value={v as string} 
                        onChange={(val) => updateTransformationConfig(idx, k, val)} 
                        rightSection={fieldPaths.length > 0 && (
                          <Menu shadow="md" width={200}>
                            <Menu.Target>
                              <ActionIcon size="xs" variant="subtle" color="gray">
                                <IconClick size="0.8rem" />
                              </ActionIcon>
                            </Menu.Target>
                            <Menu.Dropdown>
                              <Menu.Label>Source Fields</Menu.Label>
                              <Box style={{ maxHeight: '200px', overflowY: 'auto' }}>
                                {fieldPaths.map(path => (
                                  <Menu.Item key={path} onClick={() => updateTransformationConfig(idx, k, (v || '') + `source.${path}`)}>
                                    source.{path}
                                  </Menu.Item>
                                ))}
                              </Box>
                              <Menu.Divider />
                              <Menu.Label>Functions</Menu.Label>
                              <Box style={{ maxHeight: '200px', overflowY: 'auto' }}>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'upper()')}>upper(arg)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'lower()')}>lower(arg)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'trim()')}>trim(arg)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'concat()')}>concat(a, b, ...)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'substring()')}>substring(s, start, len)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'to_string()')}>to_string(v)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'coalesce()')}>coalesce(v1, v2, ...)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'default()')}>default(val, def)</Menu.Item>
                                <Menu.Divider />
                                <Menu.Label>Date & Time</Menu.Label>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'date_format(,"2006-01-02")')}>date_format(val, layout)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'unix_now()')}>unix_now()</Menu.Item>
                                <Menu.Divider />
                                <Menu.Label>Encoding</Menu.Label>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'base64_encode()')}>base64_encode(v)</Menu.Item>
                                <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'url_encode()')}>url_encode(v)</Menu.Item>
                              </Box>
                              <Menu.Divider />
                              <Menu.Label>System</Menu.Label>
                              <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'system.now')}>system.now</Menu.Item>
                              <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'system.uuid')}>system.uuid</Menu.Item>
                              <Menu.Item onClick={() => updateTransformationConfig(idx, k, 'system.space')}>system.space</Menu.Item>
                            </Menu.Dropdown>
                          </Menu>
                        )}
                      />
                    ) : (
                      <TextInput 
                        size="xs" 
                        placeholder="target_field"
                        value={v as string} 
                        onChange={(e) => updateTransformationConfig(idx, k, e.target.value)} 
                      />
                    )}

                    <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                      const next = [...transformations];
                      const newConfig = { ...next[idx].config };
                      delete newConfig[k];
                      next[idx].config = newConfig;
                      onChange(next);
                    }}>
                      <IconTrash size="0.8rem" />
                    </ActionIcon>
                  </Group>
                ))}
              </Box>
              <Button 
                size="compact-xs" 
                variant="subtle" 
                leftSection={<IconPlus size="0.8rem" />}
                onClick={() => {
                  const next = [...transformations];
                  const prefix = t.type === 'mapping' ? 'map.' : 'column.';
                  let i = 1;
                  while (`${prefix}field${i}` in next[idx].config) i++;
                  next[idx].config = { ...next[idx].config, [`${prefix}field${i}`]: '' };
                  onChange(next);
                }}
              >
                Add Field
              </Button>
            </Stack>
          )}

          <Divider my="sm" variant="dashed" />
          <Group grow align="flex-end">
            <Select
              label="On Failure"
              size="xs"
              data={[
                { value: 'fail', label: 'Fail Pipeline (Stop)' },
                { value: 'skip', label: 'Skip Message (Continue)' }
              ]}
              value={t.on_failure || 'fail'}
              onChange={(val) => {
                const next = [...transformations];
                next[idx] = { ...next[idx], on_failure: val || 'fail' };
                onChange(next);
              }}
            />
            <Autocomplete
              label="Execution Condition (Optional)"
              placeholder="e.g. eq(source.status, const.active)"
              size="xs"
              value={t.execute_if || ''}
              data={['eq(source.operation, const.create)', 'neq(source.table, const.users)', 'exists(source.id)']}
              onChange={(val) => {
                const next = [...transformations];
                next[idx] = { ...next[idx], execute_if: val };
                onChange(next);
              }}
              description="Expression that must evaluate to true for this step to run."
            />
          </Group>

          {showPreview[idx] && previews[idx] && (
            <Paper withBorder p="xs" mt="md" bg="gray.0">
              <Group justify="space-between" mb="xs">
                <Text size="xs" fw={700}>Preview Result</Text>
                <ActionIcon size="xs" variant="subtle" color="gray" onClick={() => setShowPreview(prev => ({ ...prev, [idx]: false }))}>
                  <IconEyeOff size="0.8rem" />
                </ActionIcon>
              </Group>
              {previews[idx].filtered ? (
                <Badge color="red" variant="filled">MESSAGE FILTERED (DROPPED)</Badge>
              ) : (
                <Stack gap={4}>
                   <Group gap="xs">
                      <Text size="10px" fw={700}>Table:</Text>
                      <Code>{previews[idx].table}</Code>
                   </Group>
                   <Text size="10px" fw={700}>Payload (After):</Text>
                   <ScrollArea h={100}>
                    <Code block style={{ whiteSpace: 'pre-wrap' }}>
                      {(() => {
                        try {
                          return JSON.stringify(JSON.parse(previews[idx].after), null, 2);
                        } catch (e) {
                          return previews[idx].after;
                        }
                      })()}
                    </Code>
                   </ScrollArea>
                </Stack>
              )}
            </Paper>
          )}
                  </Box>
                </Paper>
              </Box>
            );
          })}

          {/* Test Playground Section */}
          <Paper withBorder p="md" radius="md" mt="xl" bg="gray.0" style={{ borderStyle: 'dashed' }}>
            <Stack gap="sm">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconWand size="1.2rem" color="var(--mantine-color-blue-filled)" />
                  <Text fw={700} size="sm">Test Playground</Text>
                </Group>
                <Badge variant="dot" color="blue">Sample Message</Badge>
              </Group>
              <Text size="xs" c="dimmed">
                Use this sample message to test your transformations and auto-import fields.
              </Text>
              <JsonInput
                placeholder="Paste a sample message here..."
                label="Sample Message (JSON)"
                validationError="Invalid JSON"
                formatOnBlur
                autosize
                minRows={4}
                maxRows={12}
                value={sampleMessage || ''}
                onChange={(val) => onSampleMessageChange?.(val)}
              />
            </Stack>
          </Paper>

        </Stack>
      </Group>

      <Modal 
        opened={pipelineModalOpened} 
        onClose={closePipelineModal} 
        title="Pipeline Flow Visualization" 
        size="90%"
        scrollAreaComponent={ScrollArea.Autosize}
      >
        <Stack gap="xl">
          <Timeline active={pipelineResults.length} bulletSize={32} lineWidth={2}>
            <Timeline.Item 
              bullet={<ThemeIcon size="lg" radius="xl" color="gray"><IconDatabase size="1rem" /></ThemeIcon>} 
              title="Input Message"
            >
              <Paper withBorder p="xs" mt="sm">
                <ScrollArea h={150}>
                  <Code block color="gray.1" style={{ fontSize: '11px' }}>{sampleMessage}</Code>
                </ScrollArea>
              </Paper>
            </Timeline.Item>

            {transformations.map((t, idx) => {
              const info = getTransformationInfo(t.type);
              const result = pipelineResults[idx];
              const isFiltered = result?.filtered;
              const hasError = !result && !isFiltered;

              return (
                <Timeline.Item
                  key={idx}
                  bullet={
                    <ThemeIcon 
                      size="lg" 
                      radius="xl" 
                      color={isFiltered ? 'red' : hasError ? 'orange' : info.color}
                    >
                      {isFiltered ? <IconEyeOff size="1rem" /> : hasError ? <IconAlertTriangle size="1rem" /> : info.icon}
                    </ThemeIcon>
                  }
                  title={
                    <Group gap="xs">
                      <Text size="sm" fw={700}>{info.title}</Text>
                      {isFiltered && <Badge color="red" size="xs">Filtered Out</Badge>}
                      {!isFiltered && result && <Badge color="green" size="xs">Processed</Badge>}
                    </Group>
                  }
                >
                  <Text size="xs" c="dimmed">{info.description}</Text>
                  
                  {!isFiltered && result && (
                    <Box mt="xs">
                      <Grid gutter="xs">
                        <Grid.Col span={6}>
                          <Text size="10px" fw={700} c="dimmed" mb={4}>INPUT TO STEP</Text>
                          <Paper withBorder p="xs" bg="gray.0">
                            <ScrollArea h={150}>
                              <Code block style={{ fontSize: '10px', whiteSpace: 'pre-wrap' }}>
                                {(() => {
                                  const prevResult = idx === 0 ? sampleMessage : JSON.stringify(pipelineResults[idx-1], null, 2);
                                  try {
                                    return JSON.stringify(JSON.parse(prevResult || '{}'), null, 2);
                                  } catch (e) {
                                    return prevResult;
                                  }
                                })()}
                              </Code>
                            </ScrollArea>
                          </Paper>
                        </Grid.Col>
                        <Grid.Col span={6}>
                          <Text size="10px" fw={700} c="blue" mb={4}>OUTPUT FROM STEP</Text>
                          <Paper withBorder p="xs" bg="blue.0" style={{ borderColor: 'var(--mantine-color-blue-2)' }}>
                            <ScrollArea h={150}>
                              <Code block color="blue.0" style={{ fontSize: '10px', whiteSpace: 'pre-wrap' }}>
                                {JSON.stringify(result, null, 2)}
                              </Code>
                            </ScrollArea>
                          </Paper>
                        </Grid.Col>
                      </Grid>
                    </Box>
                  )}

                  {isFiltered && (
                    <Paper withBorder p="xs" mt="xs" bg="red.0">
                      <Text size="xs" c="red.7">This transformation filtered out the message. Subsequent steps were skipped.</Text>
                    </Paper>
                  )}
                </Timeline.Item>
              );
            })}

            {pipelineResults.length > 0 && !pipelineResults[pipelineResults.length-1]?.filtered && (
              <Timeline.Item 
                bullet={<ThemeIcon size="lg" radius="xl" color="green"><IconCircleCheck size="1rem" /></ThemeIcon>} 
                title="Final Output"
              >
                <Paper withBorder p="xs" mt="sm" bg="green.0">
                  <ScrollArea h={200}>
                    <Code block color="green.0" style={{ fontSize: '11px' }}>
                      {JSON.stringify(pipelineResults[pipelineResults.length-1], null, 2)}
                    </Code>
                  </ScrollArea>
                </Paper>
              </Timeline.Item>
            )}
          </Timeline>
        </Stack>
      </Modal>
    </Stack>
  );
}
