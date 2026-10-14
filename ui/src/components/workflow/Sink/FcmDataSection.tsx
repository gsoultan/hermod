import { useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  Input,
  Paper,
  Radio,
  ScrollArea,
  Select,
  Stack,
  Text,
  TextInput,
} from '@mantine/core';
import { IconInfoCircle, IconLock, IconSearch } from '@tabler/icons-react';
import { KeyValueEditor } from '../Transformation/configs/enrichment/apiLookup/KeyValueEditor';
import { rowsFromJsonObject } from '../Transformation/configs/enrichment/apiLookup/jsonText';
import { destinationColumns, rowFields, sendsColumn, setColumns, toggleColumn, type RowField } from './fcmData';

type DataMode = 'none' | 'fields' | 'envelope';

const MODES: Array<{ value: DataMode; label: string; description: string }> = [
  {
    value: 'none',
    label: 'Selected fields',
    description: 'Only the fields you tick. Recommended: small, and the app gets exactly what it reads.',
  },
  {
    value: 'fields',
    label: 'All fields',
    description: 'Every column of the row becomes its own key.',
  },
  {
    value: 'envelope',
    label: 'Whole row as JSON',
    description:
      'One key, payload, holding the row as JSON text, beside id, operation, table and schema. For apps built for that shape; a wide row will not fit.',
  },
];

/** Past this many fields the list gets a search box. */
const SEARCH_FROM = 8;

/** The checkbox for one column, or the reason it has none. */
function FieldRow({
  field,
  checked,
  withheld,
  disabled,
  onChange,
}: {
  field: RowField;
  checked: boolean;
  withheld: boolean;
  disabled: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    <Checkbox
      size="xs"
      checked={withheld ? false : checked}
      disabled={withheld || disabled}
      onChange={(e) => onChange(e.currentTarget.checked)}
      aria-label={field.name}
      label={
        <Group gap={6} wrap="nowrap">
          <Text span size="sm" ff="monospace" style={{ wordBreak: 'break-all' }}>
            {field.name}
          </Text>
          <Text span size="xs" c="dimmed">
            {field.composite ? `${field.type}, sent as JSON` : field.type}
          </Text>
        </Group>
      }
      description={
        withheld ? (
          <Group gap={4} wrap="nowrap" component="span">
            <IconLock size="0.8rem" aria-hidden />
            <span>Addresses the message, so it is not sent: a token lets anyone who holds it push to that device.</span>
          </Group>
        ) : undefined
      }
    />
  );
}

/**
 * The incoming row's columns, each one tick away from being sent.
 *
 * It replaces a row of chips that stopped at thirty, skipped object and list
 * columns and columns the editor only knew inside a change event, and silently
 * left out the device token column. People read every one of those as fields
 * going missing. Now every column is listed; the one the message is addressed
 * by is shown switched off, with the reason beside it.
 */
function FieldPicker({
  fields,
  withheld,
  dataJson,
  onChange,
}: {
  fields: RowField[];
  withheld: string[];
  dataJson: string;
  onChange: (next: string) => void;
}) {
  const [query, setQuery] = useState('');
  // JSON the rows cannot represent is left exactly as typed, and the list that
  // would rewrite it is switched off.
  const usable = rowsFromJsonObject(dataJson) !== null;
  const offered = fields.filter((field) => !withheld.includes(field.name));
  const sent = offered.filter((field) => sendsColumn(dataJson, field));
  const needle = query.trim().toLowerCase();
  const shown = needle ? fields.filter((field) => field.name.toLowerCase().includes(needle)) : fields;

  const apply = (next: string | null) => {
    if (next !== null) onChange(next);
  };

  if (fields.length === 0) {
    return (
      <Alert variant="light" color="gray" icon={<IconInfoCircle size="1rem" />} title="No fields to pick from yet">
        <Text size="sm">
          The fields appear here once the editor has a row: run <b>Test</b> on the workflow, or capture a sample at the
          source. Meanwhile, add keys by hand below.
        </Text>
      </Alert>
    );
  }

  return (
    <Input.Wrapper
      label="Fields from the incoming row"
      description="Tick a field to send it under its own name. Objects and lists are sent as JSON text."
    >
      <Paper withBorder radius="md" mt={6}>
        <Group justify="space-between" gap="xs" px="sm" py={8} style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}>
          <Text size="sm" fw={500} aria-live="polite">
            {sent.length} of {offered.length} sent
          </Text>
          <Group gap={6}>
            <Button
              size="compact-xs"
              variant="subtle"
              disabled={!usable || sent.length === offered.length}
              onClick={() => apply(setColumns(dataJson, offered, true))}
            >
              Select all
            </Button>
            <Button
              size="compact-xs"
              variant="subtle"
              color="gray"
              disabled={!usable || sent.length === 0}
              onClick={() => apply(setColumns(dataJson, offered, false))}
            >
              Clear
            </Button>
          </Group>
        </Group>
        {fields.length > SEARCH_FROM && (
          <TextInput
            aria-label="Find a field"
            placeholder={`Find among ${fields.length} fields`}
            leftSection={<IconSearch size="0.9rem" />}
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            variant="unstyled"
            px="sm"
            style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
          />
        )}
        <ScrollArea.Autosize mah={260} type="auto">
          <Stack gap={10} p="sm">
            {shown.map((field) => (
              <FieldRow
                key={field.name}
                field={field}
                checked={sendsColumn(dataJson, field)}
                withheld={withheld.includes(field.name)}
                disabled={!usable}
                onChange={(on) => apply(toggleColumn(dataJson, field, on))}
              />
            ))}
            {shown.length === 0 && (
              <Text size="sm" c="dimmed">
                No field matches “{query}”.
              </Text>
            )}
          </Stack>
        </ScrollArea.Autosize>
      </Paper>
      {!usable && (
        <Text size="xs" c="dimmed" mt={4}>
          The keys below were written as JSON the list cannot edit, so it is read-only. Edit them below.
        </Text>
      )}
    </Input.Wrapper>
  );
}

/** What "All fields" sends, said in full so nothing looks lost. */
function AllFieldsSummary({ fields, withheld }: { fields: RowField[]; withheld: string[] }) {
  if (fields.length === 0) return null;
  const sent = fields.filter((field) => !withheld.includes(field.name));
  const left = fields.filter((field) => withheld.includes(field.name));
  return (
    <Paper withBorder radius="md" p="sm">
      <Text size="sm" fw={500} mb={6}>
        Sends {sent.length} {sent.length === 1 ? 'field' : 'fields'}
      </Text>
      <Group gap={6}>
        {sent.map((field) => (
          <Badge key={field.name} variant="light" color="gray" tt="none" ff="monospace" radius="sm">
            {field.name}
          </Badge>
        ))}
      </Group>
      {left.length > 0 && (
        <Group gap={4} mt={8} wrap="nowrap" align="flex-start">
          <IconLock size="0.9rem" aria-hidden style={{ marginTop: 2, flexShrink: 0 }} />
          <Text size="xs" c="dimmed">
            Not sent: {left.map((field) => field.name).join(', ')}. It addresses the message, and a token lets anyone
            who holds it push to that device. Add it below under a key of its own if the app needs it.
          </Text>
        </Group>
      )}
    </Paper>
  );
}

/**
 * What the app's code is handed alongside — or instead of — the notification.
 *
 * It says what data is first, and asks one question: which fields does the app
 * get. Ticking a column of the incoming row adds it; the list underneath is the
 * same set of keys, for a value that is not a column — a deep link, a constant.
 *
 * data_mode keeps its stored values; only what they are called changed.
 */
export function FcmDataSection({
  config,
  updateConfig,
  availableFields,
  error,
}: {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  availableFields: Array<{ path: string; type?: string }>;
  /** What the preview says is wrong with the data, to say beside it. */
  error?: string;
}) {
  // No data_mode is the backend's default and is shown as what it is.
  const mode: DataMode = config.data_mode || 'envelope';
  const dataJson: string = config.data_json || '';
  const fieldPaths = availableFields.map((field) => field.path);
  // The column a message is addressed by is a capability, not content: the
  // backend keeps it out of the data it builds.
  const withheld = destinationColumns([config.device_token || '', config.topic || '', config.condition || '']);
  const fields = rowFields(availableFields);

  return (
    <Stack gap="md">
      <Radio.Group
        label="What to send"
        value={mode}
        onChange={(v) => updateConfig('data_mode', v)}
        description="FCM accepts at most 4,096 bytes of data, names included, and sends every value as text."
      >
        <Stack gap="xs" mt="xs">
          {MODES.map((option) => (
            <Radio.Card key={option.value} value={option.value} radius="md" p="sm">
              <Group wrap="nowrap" align="flex-start" gap="sm">
                <Radio.Indicator mt={2} />
                <div>
                  <Text size="sm" fw={500}>
                    {option.label}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {option.description}
                  </Text>
                </div>
              </Group>
            </Radio.Card>
          ))}
        </Stack>
      </Radio.Group>

      {mode === 'none' && (
        <FieldPicker
          fields={fields}
          withheld={withheld}
          dataJson={dataJson}
          onChange={(next) => updateConfig('data_json', next)}
        />
      )}
      {mode === 'fields' && <AllFieldsSummary fields={fields} withheld={withheld} />}

      <Input.Wrapper
        id="fcm-field-data_json"
        label={mode === 'none' ? 'Keys sent to the app' : 'Extra keys'}
        description={
          mode === 'none' ? (
            <>
              Each key is text, a field such as <Code>{'{{.order_id}}'}</Code>, or both. The ticked fields are here too.
            </>
          ) : (
            'Sent on top of the choice above, and replace a key of the same name.'
          )
        }
        error={error}
      >
        <KeyValueEditor
          value={dataJson}
          onChange={(next) => updateConfig('data_json', next)}
          noun="Key"
          jsonLabel="Keys as JSON"
          jsonPlaceholder='{"deeplink":"app://orders/{{.id}}"}'
          emptyHint={
            mode === 'none'
              ? 'Nothing is sent yet. Tick fields above, or add a key: a deep link is the usual first one, deeplink = app://orders/{{.id}}.'
              : 'No extra keys.'
          }
          fieldPaths={fieldPaths}
        />
      </Input.Wrapper>

      <Select
        label="If the data does not fit"
        value={config.on_oversize || 'error'}
        onChange={(v) => updateConfig('on_oversize', v || 'error')}
        data={[
          { value: 'error', label: 'Fail the message' },
          { value: 'truncate', label: 'Shorten the largest values until it fits' },
          { value: 'drop', label: 'Send the notification without the data' },
        ]}
        allowDeselect={false}
        description="A row over the limit is the same size on every attempt, so a failed message stays failed. Shortening keeps id, operation, table and schema whole."
      />
    </Stack>
  );
}
