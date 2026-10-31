import { FormRow } from '@/components/common/FormRow';
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Group,
  Paper,
  Select,
  Stack,
  Text,
  ThemeIcon,
  Tooltip,
} from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { useContext, useMemo } from 'react';
import { IconArrowRight, IconGitBranch, IconPlus, IconTrash } from '@tabler/icons-react';
import { SecretNamesContext } from '@/components/shared/TemplateField';
import { ValueBuilder } from '@/components/shared/ValueBuilder';

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
          description="What every case is compared against: a field, or a function of fields, secrets and text."
          value={config.field || ''}
          mode="field"
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

        <FormRow>
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
        </FormRow>

        <OperandInput
          label="Compare to"
          value={c.value}
          mode="token"
          onChange={(value) => onChange({ value })}
          fieldPaths={fieldPaths}
        />
      </Stack>
    </Paper>
  );
}

interface OperandInputProps {
  label: string
  description?: string
  value: unknown
  mode: 'field' | 'token'
  onChange: (value: string) => void
  fieldPaths: string[]
  required?: boolean
}

// A case value's field is written {{path}}, the reference EvaluateConditions
// reads; inside a call every field is source.<path>.
const CASE_TOKENS = { field: (path: string) => `{{${path}}}` }

// One side of a comparison, built the way every value input builds one: text,
// a field, a secret, or a function of any of them, nested as deep as needed.
function OperandInput({ label, description, value, mode, onChange, fieldPaths, required = false }: OperandInputProps) {
  const secretNames = useContext(SecretNamesContext)
  return (
    <Stack gap={6}>
      <Box>
        <Text size="sm" fw={500}>
          {label}
          {required && <Text span c="red"> *</Text>}
        </Text>
        {description && <Text size="xs" c="dimmed">{description}</Text>}
      </Box>
      <ValueBuilder
        label={label}
        value={value}
        onChange={onChange}
        mode={mode}
        tokens={mode === 'token' ? CASE_TOKENS : undefined}
        fieldPaths={fieldPaths}
        secretNames={secretNames}
        emptyKind={mode === 'field' ? 'field' : 'text'}
      />
    </Stack>
  )
}
