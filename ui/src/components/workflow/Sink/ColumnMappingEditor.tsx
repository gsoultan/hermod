import { Group, TextInput, ActionIcon, Stack, Select, Checkbox, Text, Button, Table, Autocomplete } from '@mantine/core';
import { IconPlus, IconTrash, IconWand } from '@tabler/icons-react';
import type { FC } from 'react';

export interface ColumnMapping {
  source_field: string;
  target_column: string;
  data_type?: string;
  is_primary_key?: boolean;
  is_nullable?: boolean;
  is_identity?: boolean;
}

interface ColumnMappingEditorProps {
  mappings: ColumnMapping[];
  availableFields: string[];
  onChange: (mappings: ColumnMapping[]) => void;
  onSmartMap?: () => void;
  onSmartMapFromSource?: () => void;
  loading?: boolean;
  loadingSource?: boolean;
  sinkType?: string;
}

const DATA_TYPES: Record<string, string[]> = {
  postgres: ['TEXT', 'INTEGER', 'BIGINT', 'BOOLEAN', 'TIMESTAMP', 'JSONB', 'NUMERIC', 'UUID', 'VECTOR'],
  yugabyte: ['TEXT', 'INTEGER', 'BIGINT', 'BOOLEAN', 'TIMESTAMP', 'JSONB', 'NUMERIC', 'UUID'],
  mysql: ['VARCHAR(255)', 'INT', 'BIGINT', 'TINYINT(1)', 'DATETIME', 'JSON', 'DECIMAL'],
  mariadb: ['VARCHAR(255)', 'INT', 'BIGINT', 'TINYINT(1)', 'DATETIME', 'JSON', 'DECIMAL'],
  mssql: ['NVARCHAR(MAX)', 'INT', 'BIGINT', 'BIT', 'DATETIME2', 'DECIMAL'],
  oracle: ['VARCHAR2(4000)', 'NUMBER', 'TIMESTAMP', 'CLOB'],
  sqlite: ['TEXT', 'INTEGER', 'REAL', 'BLOB'],
  clickhouse: ['String', 'Int32', 'Int64', 'Float64', 'DateTime', 'JSON', 'UInt8'],
  snowflake: ['VARCHAR', 'NUMBER', 'BOOLEAN', 'TIMESTAMP_NTZ', 'VARIANT'],
  cassandra: ['text', 'int', 'bigint', 'boolean', 'timestamp', 'uuid'],
  pgvector: ['TEXT', 'INTEGER', 'BIGINT', 'BOOLEAN', 'TIMESTAMP', 'JSONB', 'NUMERIC', 'UUID', 'VECTOR'],
  mongodb: ['string', 'int', 'long', 'double', 'decimal', 'bool', 'date', 'objectid', 'object', 'array', 'binData'],
};

export const ColumnMappingEditor: FC<ColumnMappingEditorProps> = ({
  mappings,
  availableFields,
  onChange,
  onSmartMap,
  onSmartMapFromSource,
  loading,
  loadingSource,
  sinkType,
}) => {
  const addMapping = () => {
    onChange([...mappings, { source_field: '', target_column: '', is_primary_key: false, is_nullable: true, is_identity: false }]);
  };

  const removeMapping = (index: number) => {
    onChange(mappings.filter((_, i) => i !== index));
  };

  const updateMapping = (index: number, field: keyof ColumnMapping, value: any) => {
    const newMappings = [...mappings];
    newMappings[index] = { ...newMappings[index], [field]: value };
    onChange(newMappings);
  };

  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Text size="sm" fw={500}>Column Mapping</Text>
        <Group gap="xs">
          {onSmartMapFromSource && (
            <Button 
              variant="light" 
              color="indigo"
              leftSection={<IconWand size={16} />} 
              size="xs" 
              onClick={onSmartMapFromSource}
              loading={loadingSource}
              title="Auto-map columns based on the upstream source schema"
            >
              Smart Map from Source
            </Button>
          )}
          {onSmartMap && (
            <Button 
              variant="light" 
              leftSection={<IconWand size={16} />} 
              size="xs" 
              onClick={onSmartMap}
              loading={loading}
              title="Auto-map columns based on the existing target table schema"
            >
              Smart Map from Sink
            </Button>
          )}
          <Button variant="light" leftSection={<IconPlus size={16} />} size="xs" onClick={addMapping}>
            Add Column
          </Button>
        </Group>
      </Group>

      {mappings.length > 0 ? (
        <Table withTableBorder withColumnBorders verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Source Field</Table.Th>
              <Table.Th>Target Column</Table.Th>
              <Table.Th style={{ width: '150px' }}>Data Type</Table.Th>
              <Table.Th style={{ width: '60px' }}>PK</Table.Th>
              <Table.Th style={{ width: '60px' }}>ID</Table.Th>
              <Table.Th style={{ width: '60px' }}>Null</Table.Th>
              <Table.Th style={{ width: '40px' }}></Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {mappings.map((mapping, index) => (
              <Table.Tr key={index}>
                <Table.Td>
                  <Select
                    placeholder="Source field"
                    data={availableFields || []}
                    value={mapping.source_field}
                    onChange={(val) => updateMapping(index, 'source_field', val)}
                    searchable
                    size="xs"
                  />
                </Table.Td>
                <Table.Td>
                  <TextInput
                    placeholder="Target column"
                    value={mapping.target_column}
                    onChange={(e) => updateMapping(index, 'target_column', e.target.value)}
                    size="xs"
                  />
                </Table.Td>
                <Table.Td>
                  <Autocomplete
                    placeholder="Type"
                    data={sinkType ? (DATA_TYPES[sinkType] || []) : []}
                    value={mapping.data_type || ''}
                    onChange={(val) => updateMapping(index, 'data_type', val)}
                    size="xs"
                  />
                </Table.Td>
                <Table.Td style={{ textAlign: 'center' }}>
                  <Checkbox
                    checked={mapping.is_primary_key || false}
                    onChange={(e) => updateMapping(index, 'is_primary_key', e.currentTarget.checked)}
                    size="xs"
                  />
                </Table.Td>
                <Table.Td style={{ textAlign: 'center' }}>
                  <Checkbox
                    checked={mapping.is_identity || false}
                    onChange={(e) => updateMapping(index, 'is_identity', e.currentTarget.checked)}
                    size="xs"
                    title="Auto-increment / Identity / Sequence"
                  />
                </Table.Td>
                <Table.Td style={{ textAlign: 'center' }}>
                  <Checkbox
                    checked={mapping.is_nullable ?? true}
                    onChange={(e) => updateMapping(index, 'is_nullable', e.currentTarget.checked)}
                    size="xs"
                  />
                </Table.Td>
                <Table.Td>
                  <ActionIcon color="red" variant="subtle" onClick={() => removeMapping(index)}>
                    <IconTrash size={16} />
                  </ActionIcon>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      ) : (
        <Text size="xs" c="dimmed" fs="italic">No columns mapped. Hermod will use (id, data) by default for auto-created tables.</Text>
      )}
    </Stack>
  );
};
