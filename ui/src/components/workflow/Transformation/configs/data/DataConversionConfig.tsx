import { ActionIcon, Autocomplete, Box, Button, Group, Select, Stack, Text, TextInput } from '@mantine/core';
import { IconPlus, IconTrash } from '@tabler/icons-react';

interface DataConversionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

interface ConversionRow {
  field?: string;
  targetType?: string;
  format?: string;
  separator?: string;
  elementType?: string;
  targetField?: string;
  errorBehavior?: string;
}

// Target types offered for a conversion. Every value must be one convertScalar
// or toArray accepts (pkg/comm/transformer/core/conversion.go), or the editor
// offers a conversion the pipeline rejects at runtime.
const TARGET_TYPES = [
  { value: 'int', label: 'Integer' },
  { value: 'float', label: 'Float' },
  { value: 'string', label: 'String' },
  { value: 'bool', label: 'Boolean' },
  { value: 'date', label: 'Date' },
  { value: 'array', label: 'Array' },
  { value: 'jsonb', label: 'JSON / JSONB' },
  { value: 'uuid', label: 'UUID' },
];

// Element types offered for an array conversion. Every entry other than the
// "leave as-is" sentinel must be a type convertScalar accepts.
const ELEMENT_TYPES = [
  { value: 'auto', label: 'Leave as-is' },
  { value: 'string', label: 'String' },
  { value: 'int', label: 'Integer' },
  { value: 'float', label: 'Float' },
  { value: 'bool', label: 'Boolean' },
  { value: 'uuid', label: 'UUID' },
];

const ERROR_BEHAVIORS = [
  { value: 'fail', label: 'Fail (Error output)' },
  { value: 'null', label: 'Set to NULL' },
  { value: 'keep', label: 'Keep original' },
];

// A row with no behaviour of its own inherits the node's, so the control needs
// a value for "inherit" that is not one of the three behaviours.
const INHERIT = 'inherit';

// The keys the node used before it held a list of rows. The row list is
// authoritative once it exists -- parseConversions keys off the list being
// present, not off it having rows in it -- so these are cleared on the first
// edit rather than left behind to contradict it.
const LEGACY_ROW_KEYS = ['field', 'targetType', 'format', 'separator', 'elementType', 'targetField'];

const BLANK_ROW: ConversionRow = { field: '', targetType: 'string' };

// A config stored before the node held rows is shown as a single row, so an
// existing node opens looking like what it does rather than looking empty.
function legacyRow(config: any): ConversionRow | null {
  if (!config.field && !config.targetType) return null;
  return {
    field: config.field || '',
    targetType: config.targetType || 'string',
    format: config.format,
    separator: config.separator,
    elementType: config.elementType,
    targetField: config.targetField,
  };
}

export function DataConversionConfig({ config, updateNodeConfig, nodeId, fieldPaths }: DataConversionConfigProps) {
  const storedRows: ConversionRow[] | null = Array.isArray(config.conversions) ? config.conversions : null;
  const rows: ConversionRow[] = storedRows ?? (legacyRow(config) ? [legacyRow(config) as ConversionRow] : [BLANK_ROW]);

  const commit = (next: ConversionRow[]) => {
    const cleared: Record<string, undefined> = {};
    for (const key of LEGACY_ROW_KEYS) cleared[key] = undefined;
    updateNodeConfig(nodeId, { conversions: next, ...cleared });
  };

  const updateRow = (index: number, patch: Partial<ConversionRow>) => {
    commit(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  };

  const addRow = () => commit([...rows, { ...BLANK_ROW }]);

  const removeRow = (index: number) => commit(rows.filter((_, i) => i !== index));

  return (
    <Stack gap="xs">
      <Text size="xs" c="dimmed">
        Each row converts one field to one type. Rows are applied in the order shown, and every row
        reads the message as it arrived — so a row is never affected by the row above it.
      </Text>

      {rows.map((row, i) => {
        const targetType = row.targetType || 'string';
        // The separator is what splits a scalar into a list and what joins a
        // list back into a scalar, so it belongs to both directions.
        const usesSeparator = targetType === 'array' || targetType === 'string';

        return (
          <Box
            key={i}
            data-testid={`conversion-row-${i}`}
            p="xs"
            style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}
          >
            <Stack gap="xs">
              <Group gap="xs" align="flex-end" wrap="nowrap">
                <Autocomplete
                  label="Field"
                  placeholder="amount"
                  data={fieldPaths || []}
                  value={row.field || ''}
                  onChange={(val) => updateRow(i, { field: val })}
                  required
                  description="Field or expression to convert (e.g. amount, lower(source.status))."
                  style={{ flex: 1 }}
                />
                <Select
                  label="Target Type"
                  data={TARGET_TYPES}
                  value={targetType}
                  onChange={(val) => updateRow(i, { targetType: val || 'string' })}
                  style={{ flex: 1 }}
                />
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={`Remove conversion ${i + 1}`}
                  onClick={() => removeRow(i)}
                  mb={4}
                >
                  <IconTrash size="1rem" />
                </ActionIcon>
              </Group>

              {targetType === 'date' && (
                <TextInput
                  label="Date Format"
                  placeholder="2006-01-02"
                  value={row.format || ''}
                  onChange={(e) => updateRow(i, { format: e.currentTarget.value })}
                  description="Go date format (e.g. 2006-01-02)"
                />
              )}

              {targetType === 'jsonb' && (
                <Text size="xs" c="dimmed">
                  Renders the value as JSON text, ready for a <code>json</code> or <code>jsonb</code>{' '}
                  column — an object or list reaches most database drivers as an unsupported type
                  otherwise. Text that already holds a JSON object or array is passed through unchanged,
                  so it is not double-encoded; any other value becomes a JSON string. Text that opens
                  like JSON but does not parse follows On Error rather than being stored as-is.
                </Text>
              )}

              {targetType === 'uuid' && (
                <Text size="xs" c="dimmed">
                  Validates the value and normalises it to lower-case hyphenated form. Accepts hyphenated,
                  bare hex, braced and <code>urn:uuid</code> forms, and the raw 16 bytes a uuid column
                  decodes to. A value that is not a UUID follows On Error.
                </Text>
              )}

              {usesSeparator && (
                <TextInput
                  label="Separator"
                  placeholder=","
                  value={row.separator || ''}
                  onChange={(e) => updateRow(i, { separator: e.currentTarget.value })}
                  description={
                    targetType === 'array'
                      ? 'Splits a text value into elements. Defaults to a comma. A value that is already a list, or a JSON array, is used as-is.'
                      : 'Joins a list into text. Defaults to a comma. Ignored for values that are not lists.'
                  }
                />
              )}

              {targetType === 'array' && (
                <Select
                  label="Element Type"
                  data={ELEMENT_TYPES}
                  value={row.elementType || 'auto'}
                  onChange={(val) => updateRow(i, { elementType: val === 'auto' || !val ? '' : val })}
                  description="Coerce every element. Needed when the column is typed: splitting text yields strings, and a string does not match an integer or uuid column."
                />
              )}

              <Group grow gap="xs" align="flex-start">
                <TextInput
                  label="Target Field (Optional)"
                  placeholder="Defaults to source field"
                  value={row.targetField || ''}
                  onChange={(e) => updateRow(i, { targetField: e.currentTarget.value })}
                />
                <Select
                  label="On Error"
                  data={[{ value: INHERIT, label: 'Use node default' }, ...ERROR_BEHAVIORS]}
                  value={row.errorBehavior || INHERIT}
                  onChange={(val) => updateRow(i, { errorBehavior: !val || val === INHERIT ? '' : val })}
                  description="Overrides the node default for this field alone."
                />
              </Group>
            </Stack>
          </Box>
        );
      })}

      <Button variant="light" size="xs" leftSection={<IconPlus size="0.9rem" />} onClick={addRow}>
        Add Conversion
      </Button>

      <Select
        label="On Error (default)"
        description="Applied to every row that does not override it."
        data={ERROR_BEHAVIORS}
        value={config.errorBehavior || 'fail'}
        onChange={(val) => updateNodeConfig(nodeId, { errorBehavior: val || 'fail' })}
      />
    </Stack>
  );
}
