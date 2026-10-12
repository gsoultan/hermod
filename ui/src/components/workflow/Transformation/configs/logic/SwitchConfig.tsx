import {
  ActionIcon,
  Autocomplete,
  Badge,
  Box,
  Button,
  Code,
  Group,
  Paper,
  SegmentedControl,
  SimpleGrid,
  Select,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Tooltip,
} from '@mantine/core';
import { useMemo, useState } from 'react';
import { IconArrowRight, IconBraces, IconGitBranch, IconMathFunction, IconPlus, IconTrash } from '@tabler/icons-react';
import {
  OPERAND_FUNCTIONS,
  acceptsFunction,
  formatCaseValue,
  formatSwitchField,
  parseCaseValue,
  parseSwitchField,
  type Operand,
  type OperandSource,
} from './switchOperand';

interface SwitchConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
}

const OPERATORS = [
  { label: 'Equals (=)', value: '=' },
  { label: 'Not equals (!=)', value: '!=' },
  { label: 'Greater than (>)', value: '>' },
  { label: 'Greater or equal (>=)', value: '>=' },
  { label: 'Less than (<)', value: '<' },
  { label: 'Less or equal (<=)', value: '<=' },
  { label: 'Contains', value: 'contains' },
  { label: 'Not contains', value: 'not_contains' },
  { label: 'Regex match', value: 'regex' },
  { label: 'Not regex match', value: 'not_regex' },
];

export function SwitchConfig({ config, updateNodeConfig, nodeId, availableFields = [] }: SwitchConfigProps) {
  const fieldPaths = useMemo(
    () => (availableFields || []).map((f) => (typeof f === 'string' ? f : f.path)),
    [availableFields],
  );

  // `cases` arrives in either shape: this editor saves a plain array, while
  // the engine's tests, workflow bundles and anything built through the API
  // carry the JSON string form. MiscNodes.tsx and transformationUtils.ts both
  // already read either one; reading only the array here meant a
  // string-shaped workflow showed no cases, and the first edit saved that
  // emptiness over the user's configuration.
  const cases = useMemo(() => {
    if (Array.isArray(config.cases)) return config.cases;
    if (typeof config.cases === 'string' && config.cases.trim()) {
      try {
        const parsed = JSON.parse(config.cases);
        return Array.isArray(parsed) ? parsed : [];
      } catch {
        return [];
      }
    }
    return [];
  }, [config.cases]);

  const updateCase = (index: number, val: any) => {
    const newCases = [...cases];
    newCases[index] = { ...newCases[index], ...val };
    updateNodeConfig(nodeId, { cases: newCases });
  };

  const addCase = () => {
    updateNodeConfig(nodeId, { cases: [...cases, { label: `case_${cases.length + 1}`, value: '', operator: '=' }] });
  };

  const removeCase = (index: number) => {
    updateNodeConfig(nodeId, { cases: cases.filter((_: any, i: number) => i !== index) });
  };

  return (
    <Stack gap="lg">
      <Text size="sm" c="dimmed">
        Cases are checked top to bottom. The record follows the first case that matches, or the
        default branch when none do.
      </Text>

      <Paper withBorder radius="md" p="md">
        <OperandInput
          label="Switch field"
          description="The field every case is compared against, optionally passed through a function."
          value={config.field || ''}
          parse={parseSwitchField}
          format={formatSwitchField}
          onChange={(field) => updateNodeConfig(nodeId, { field })}
          fieldPaths={fieldPaths}
          required
        />
      </Paper>

      <Stack gap="sm">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <Text fw={600} size="sm">Cases</Text>
            <Badge size="sm" variant="light" color="gray">{cases.length}</Badge>
          </Group>
          <Button size="xs" variant="light" leftSection={<IconPlus size="0.9rem" />} onClick={addCase}>
            Add case
          </Button>
        </Group>

        {cases.length === 0 ? (
          <Paper withBorder radius="md" p="lg" style={{ borderStyle: 'dashed' }}>
            <Text size="sm" c="dimmed" ta="center">
              No cases yet. Every record goes to the default branch until you add one.
            </Text>
          </Paper>
        ) : (
          cases.map((c: any, i: number) => (
            <CaseCard
              key={i}
              index={i}
              value={c}
              fieldPaths={fieldPaths}
              onChange={(patch) => updateCase(i, patch)}
              onRemove={() => removeCase(i)}
            />
          ))
        )}

        <Paper radius="md" p="sm" bg="var(--mantine-color-default-hover)">
          <Group gap="xs" wrap="nowrap">
            <ThemeIcon size="sm" variant="light" color="gray">
              <IconGitBranch size="0.9rem" />
            </ThemeIcon>
            <Text size="sm" c="dimmed">No case matches</Text>
            <IconArrowRight size="0.9rem" color="var(--mantine-color-dimmed)" />
            <Badge variant="outline" color="gray">default</Badge>
          </Group>
        </Paper>
      </Stack>
    </Stack>
  );
}

interface CaseCardProps {
  index: number;
  value: any;
  fieldPaths: string[];
  onChange: (patch: any) => void;
  onRemove: () => void;
}

function CaseCard({ index, value: c, fieldPaths, onChange, onRemove }: CaseCardProps) {
  const n = index + 1;

  return (
    <Paper component="fieldset" withBorder radius="md" p="md" m={0} miw={0} aria-label={`Case ${n}`}>
      <Stack gap="md">
        <Group justify="space-between" align="center" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <Badge size="md" circle variant="light">{n}</Badge>
            <Text size="sm" fw={600}>Case {n}</Text>
          </Group>
          <Tooltip label="Remove case">
            <ActionIcon aria-label={`Remove case ${n}`} color="red" variant="subtle" onClick={onRemove}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Tooltip>
        </Group>

        <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="sm">
          <TextInput
            label="Branch Label"
            placeholder="e.g. high_value"
            value={c.label || ''}
            onChange={(e) => onChange({ label: e.currentTarget.value })}
          />
          <Select
            label="Operator"
            value={c.operator || '='}
            onChange={(val) => onChange({ operator: val || '=' })}
            data={OPERATORS}
            allowDeselect={false}
          />
        </SimpleGrid>

        <OperandInput
          label="Compare to"
          value={c.value}
          parse={parseCaseValue}
          format={formatCaseValue}
          onChange={(value) => onChange({ value })}
          fieldPaths={fieldPaths}
          allowValue
        />
      </Stack>
    </Paper>
  );
}

const FUNCTION_OPTIONS = ['Text', 'Number'].map((group) => ({
  group,
  items: OPERAND_FUNCTIONS.filter((f) => f.group === group).map((f) => ({ value: f.value, label: f.label })),
}));

interface OperandInputProps {
  label: string;
  description?: string;
  value: unknown;
  parse: (value: unknown) => Operand;
  format: (op: Operand) => string;
  onChange: (value: string) => void;
  fieldPaths: string[];
  /** Offer a fixed value as well as a field. */
  allowValue?: boolean;
  required?: boolean;
}

// One side of a comparison: a field picked from the record or a fixed value,
// optionally passed through a function. The saved string is the only state
// the engine sees; `draft` holds the toggle and function while no field has
// been picked, since an empty value cannot record them.
function OperandInput({
  label,
  description,
  value,
  parse,
  format,
  onChange,
  fieldPaths,
  allowValue = false,
  required = false,
}: OperandInputProps) {
  const [draft, setDraft] = useState<{ fn: string; source: OperandSource }>({
    fn: '',
    source: allowValue ? 'value' : 'field',
  });
  const empty = value === undefined || value === null || value === '';
  const op: Operand = empty ? { ...draft, text: '' } : parse(value);
  const saved = empty ? '' : String(value);

  const commit = (next: Operand) => {
    setDraft({ fn: next.fn, source: next.source });
    onChange(format(next));
  };

  const fnDisabled = !acceptsFunction(op.text);

  return (
    <Stack gap={6}>
      <Group justify="space-between" align="center" wrap="nowrap" gap="xs">
        <Box>
          <Text size="sm" fw={500}>
            {label}
            {required && <Text span c="red"> *</Text>}
          </Text>
          {description && <Text size="xs" c="dimmed">{description}</Text>}
        </Box>
        {allowValue && (
          <SegmentedControl
            size="xs"
            value={op.source}
            onChange={(next) => next !== op.source && commit({ fn: op.fn, source: next as OperandSource, text: '' })}
            data={[
              { label: 'Value', value: 'value' },
              { label: 'Field', value: 'field' },
            ]}
          />
        )}
      </Group>

      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Tooltip label="A function cannot wrap a hand-written expression" disabled={!fnDisabled}>
          <Select
            aria-label={`${label} function`}
            placeholder="No function"
            data={FUNCTION_OPTIONS}
            value={op.fn || null}
            onChange={(fn) => commit({ ...op, fn: fn || '' })}
            leftSection={<IconMathFunction size="1rem" />}
            disabled={fnDisabled}
            clearable
            w={170}
            style={{ flexShrink: 0 }}
            comboboxProps={{ withinPortal: true }}
          />
        </Tooltip>
        <Box style={{ flex: 1, minWidth: 0 }}>
          {op.source === 'field' ? (
            <Autocomplete
              aria-label={allowValue ? `${label} field` : label}
              placeholder="Search fields"
              data={fieldPaths}
              value={op.text}
              onChange={(text) => commit({ ...op, text: text.trim() })}
              leftSection={<IconBraces size="1rem" />}
              limit={50}
            />
          ) : (
            <TextInput
              aria-label={label}
              placeholder="e.g. active, 100"
              value={op.text}
              onChange={(e) => commit({ ...op, text: e.currentTarget.value })}
            />
          )}
        </Box>
      </Group>

      {op.fn && saved && (
        <Text size="xs" c="dimmed">
          Saved as <Code>{saved}</Code>
        </Text>
      )}
    </Stack>
  );
}
