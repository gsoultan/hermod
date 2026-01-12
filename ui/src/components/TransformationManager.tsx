import { Button, Group, TextInput, Stack, Divider, Text, Paper, ActionIcon, Checkbox, Box, Accordion, List, ThemeIcon, Select, Drawer, Tooltip } from '@mantine/core';
import { IconTrash, IconPlus, IconInfoCircle, IconArrowsDiff, IconFilter, IconTableAlias, IconWand, IconWorld, IconDatabase, IconFilterCode } from '@tabler/icons-react';
import { useDisclosure } from '@mantine/hooks';

interface TransformationManagerProps {
  transformations: any[];
  onChange: (transformations: any[]) => void;
  title: string;
}

export function TransformationManager({ transformations, onChange, title }: TransformationManagerProps) {
  const [opened, { open, close }] = useDisclosure(false);

  const addTransformation = (type: string) => {
    let config = {};
    if (type === 'rename_table') {
      config = { old_name: '', new_name: '' };
    } else if (type === 'filter_operation') {
      config = { create: 'true', update: 'true', delete: 'true', snapshot: 'true' };
    } else if (type === 'filter_data') {
      config = { field: '', operator: '=', value: '' };
    } else if (type === 'mapping') {
      config = {};
    } else if (type === 'advanced') {
      config = {};
    } else if (type === 'http') {
      config = { url: '', method: 'GET' };
    } else if (type === 'sql') {
      config = { driver: 'postgres', conn: '', query: '' };
    }
    
    onChange([...transformations, { type, config }]);
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
          
          <Accordion variant="separated">
            <Accordion.Item value="rename_table">
              <Accordion.Control icon={<IconTableAlias size="1rem" color="gray" />}>Rename Table</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Changes the table name in the message. Useful when the destination schema differs from the source.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="filter_operation">
              <Accordion.Control icon={<IconFilter size="1rem" color="orange" />}>Filter Operation</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Filters messages based on the operation type. Uncheck operations you want to ignore.</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="filter_data">
              <Accordion.Control icon={<IconFilterCode size="1rem" color="red" />}>Filter Data</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Filters messages based on field values in the payload.</Text>
                <List size="xs" mt="xs">
                  <List.Item><b>Field</b>: JSON path (e.g., <code>id</code>, <code>profile.age</code>)</List.Item>
                  <List.Item><b>Operator</b>: Comparison to apply</List.Item>
                  <List.Item><b>Value</b>: Value to compare against</List.Item>
                </List>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="mapping">
              <Accordion.Control icon={<IconArrowsDiff size="1rem" color="blue" />}>Field Mapping</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Rename or drop fields. If any mapping is defined, only mapped fields will be kept (strict mode).</Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="advanced">
              <Accordion.Control icon={<IconWand size="1rem" color="indigo" />}>Advanced Mapping</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Use expressions to create new fields or transform existing ones.</Text>
                <List size="xs" mt="xs">
                  <List.Item><code>source.field_name</code>: Value from source</List.Item>
                  <List.Item><code>system.now</code>: Current timestamp</List.Item>
                  <List.Item><code>system.uuid</code>: Generated UUID</List.Item>
                  <List.Item><code>const.value</code>: Literal value</List.Item>
                </List>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="enrichment">
              <Accordion.Control icon={<IconWorld size="1rem" color="teal" />}>Enrichment (HTTP/SQL)</Accordion.Control>
              <Accordion.Panel>
                <Text size="xs">Fetch additional data from external APIs or databases. Use <code>{`{field}`}</code> in HTTP URLs or <code>:field</code> in SQL queries to inject message data.</Text>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        </Stack>
      </Drawer>

      <Group align="flex-start" gap="md" wrap="nowrap">
        {/* Left Side Panel - Menu */}
        <Paper withBorder p="md" radius="md" style={{ width: '220px', flexShrink: 0, position: 'sticky', top: '20px' }}>
          <Stack gap="sm">
            <Group justify="space-between" mb="xs">
              <Text fw={700} size="sm">Components</Text>
              <Tooltip label="How to use">
                <ActionIcon variant="subtle" color="gray" size="sm" onClick={open}>
                  <IconInfoCircle size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            </Group>
            
            <Divider mb="xs" />
            
            <Text size="xs" fw={500} c="dimmed" mb={4}>Transform</Text>
            <Button fullWidth justify="flex-start" size="xs" variant="light" leftSection={<IconTableAlias size="0.9rem" />} onClick={() => addTransformation('rename_table')}>Rename Table</Button>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="orange" leftSection={<IconFilter size="0.9rem" />} onClick={() => addTransformation('filter_operation')}>Op Filter</Button>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="red" leftSection={<IconFilterCode size="0.9rem" />} onClick={() => addTransformation('filter_data')}>Data Filter</Button>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="blue" leftSection={<IconArrowsDiff size="0.9rem" />} onClick={() => addTransformation('mapping')}>Field Map</Button>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="indigo" leftSection={<IconWand size="0.9rem" />} onClick={() => addTransformation('advanced')}>Advanced</Button>
            
            <Text size="xs" fw={500} c="dimmed" mt="md" mb={4}>Enrichment</Text>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="teal" leftSection={<IconWorld size="0.9rem" />} onClick={() => addTransformation('http')}>HTTP Request</Button>
            <Button fullWidth justify="flex-start" size="xs" variant="light" color="cyan" leftSection={<IconDatabase size="0.9rem" />} onClick={() => addTransformation('sql')}>SQL Query</Button>
          </Stack>
        </Paper>

        {/* Right Side Panel - Transformation Stage */}
        <Stack style={{ flex: 1 }} gap="md">
          <Paper p="sm" withBorder bg="gray.0" radius="md">
            <Group justify="space-between">
              <Text fw={700} size="sm">{title || 'Transformation Stage'}</Text>
              {transformations.length > 0 && (
                <Text size="xs" c="dimmed">{transformations.length} step(s) configured</Text>
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

          {transformations.map((t, idx) => (
        <Paper key={idx} p="md" withBorder radius="md" shadow="xs">
          <Group justify="space-between" mb="sm">
            <Group gap="xs">
              <ThemeIcon size="sm" variant="light" color={
                t.type === 'advanced' ? 'indigo' : 
                t.type === 'mapping' ? 'blue' : 
                t.type === 'filter_operation' ? 'orange' : 
                t.type === 'filter_data' ? 'red' :
                t.type === 'http' ? 'teal' :
                t.type === 'sql' ? 'cyan' : 'gray'
              }>
                {t.type === 'advanced' && <IconWand size="0.8rem" />}
                {t.type === 'mapping' && <IconArrowsDiff size="0.8rem" />}
                {t.type === 'filter_operation' && <IconFilter size="0.8rem" />}
                {t.type === 'filter_data' && <IconFilterCode size="0.8rem" />}
                {t.type === 'rename_table' && <IconTableAlias size="0.8rem" />}
                {t.type === 'http' && <IconWorld size="0.8rem" />}
                {t.type === 'sql' && <IconDatabase size="0.8rem" />}
              </ThemeIcon>
              <Text size="xs" fw={700} style={{ textTransform: 'uppercase' }}>
                {t.type.replace('_', ' ')}
              </Text>
            </Group>
            <ActionIcon size="sm" color="red" variant="subtle" onClick={() => removeTransformation(idx)}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>

          {t.type === 'rename_table' && (
            <Group grow>
              <TextInput label="Old Name" placeholder="users" size="xs" value={t.config.old_name} onChange={(e) => updateTransformationConfig(idx, 'old_name', e.target.value)} />
              <TextInput label="New Name" placeholder="customers" size="xs" value={t.config.new_name} onChange={(e) => updateTransformationConfig(idx, 'new_name', e.target.value)} />
            </Group>
          )}

          {t.type === 'filter_operation' && (
            <Group gap="xl" justify="center" py="xs">
              <Checkbox label="Create" size="xs" checked={t.config.create === 'true'} onChange={(e) => updateTransformationConfig(idx, 'create', e.currentTarget.checked ? 'true' : 'false')} />
              <Checkbox label="Update" size="xs" checked={t.config.update === 'true'} onChange={(e) => updateTransformationConfig(idx, 'update', e.currentTarget.checked ? 'true' : 'false')} />
              <Checkbox label="Delete" size="xs" checked={t.config.delete === 'true'} onChange={(e) => updateTransformationConfig(idx, 'delete', e.currentTarget.checked ? 'true' : 'false')} />
              <Checkbox label="Snapshot" size="xs" checked={t.config.snapshot === 'true'} onChange={(e) => updateTransformationConfig(idx, 'snapshot', e.currentTarget.checked ? 'true' : 'false')} />
            </Group>
          )}

          {t.type === 'filter_data' && (
            <Group grow gap="xs">
              <TextInput label="Field Path" placeholder="e.g. status" size="xs" value={t.config.field} onChange={(e) => updateTransformationConfig(idx, 'field', e.target.value)} />
              <Select label="Operator" size="xs" data={[
                { value: '=', label: 'Equals (=)' },
                { value: '!=', label: 'Not Equals (!=)' },
                { value: '>', label: 'Greater Than (>)' },
                { value: '<', label: 'Less Than (<)' },
                { value: 'contains', label: 'Contains' }
              ]} value={t.config.operator} onChange={(val) => updateTransformationConfig(idx, 'operator', val || '=')} />
              <TextInput label="Value" placeholder="e.g. active" size="xs" value={t.config.value} onChange={(e) => updateTransformationConfig(idx, 'value', e.target.value)} />
            </Group>
          )}
          
          {t.type === 'http' && (
            <Stack gap="xs">
              <Group grow>
                <TextInput label="URL" placeholder="https://api.example.com/users/{id}" size="xs" value={t.config.url} onChange={(e) => updateTransformationConfig(idx, 'url', e.target.value)} />
                <Select label="Method" size="xs" data={['GET', 'POST', 'PUT', 'PATCH']} value={t.config.method} onChange={(val) => updateTransformationConfig(idx, 'method', val || 'GET')} />
              </Group>
              <Box>
                <Text size="xs" fw={500} mb={4}>Headers</Text>
                {Object.entries(t.config).filter(([k]) => k.startsWith('header.')).map(([k, v]) => (
                  <Group key={k} grow gap="xs" mb="xs">
                    <TextInput size="xs" placeholder="Header Name" value={k.replace('header.', '')} 
                      onChange={(e) => {
                        const newKey = `header.${e.target.value}`;
                        const next = [...transformations];
                        const newConfig = { ...next[idx].config };
                        delete newConfig[k];
                        newConfig[newKey] = v;
                        next[idx].config = newConfig;
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

          {(t.type === 'mapping' || t.type === 'advanced') && (
            <Stack gap="xs">
              <Box>
                <Group grow gap="xs" mb={4}>
                  <Text size="xs" fw={500} c="dimmed">Source / Target Field</Text>
                  <Text size="xs" fw={500} c="dimmed">{t.type === 'advanced' ? 'Expression' : 'Destination Field'}</Text>
                  <Box style={{ maxWidth: 30 }}></Box>
                </Group>
                {Object.entries(t.config).filter(([k]) => k.startsWith(t.type === 'mapping' ? 'map.' : 'column.')).map(([k, v]) => (
                  <Group key={k} grow gap="xs" mb="xs">
                    <TextInput 
                      size="xs" 
                      placeholder={t.type === 'mapping' ? "source_field" : "target_field"}
                      value={k.replace(t.type === 'mapping' ? 'map.' : 'column.', '')} 
                      onChange={(e) => {
                        const prefix = t.type === 'mapping' ? 'map.' : 'column.';
                        const newKey = `${prefix}${e.target.value}`;
                        const next = [...transformations];
                        const newConfig = { ...next[idx].config };
                        delete newConfig[k];
                        newConfig[newKey] = v;
                        next[idx].config = newConfig;
                        onChange(next);
                      }}
                    />
                    <TextInput 
                      size="xs" 
                      placeholder={t.type === 'mapping' ? "target_field" : "expression (e.g. source.name)"}
                      value={v as string} 
                      onChange={(e) => updateTransformationConfig(idx, k, e.target.value)} 
                    />
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
        </Paper>
      ))}

        </Stack>
      </Group>
    </Stack>
  );
}
