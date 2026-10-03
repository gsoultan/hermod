import { Chip, Fieldset, Group, Input, Radio, Select, Stack, Text } from '@mantine/core';
import { KeyValueEditor } from '../Transformation/configs/enrichment/apiLookup/KeyValueEditor';
import { rowsFromJsonObject } from '../Transformation/configs/enrichment/apiLookup/jsonText';
import { destinationColumns, rowColumns, sendsColumn, toggleColumn } from './fcmData';

type DataMode = 'none' | 'fields' | 'envelope';

const MODES: Array<{ value: DataMode; label: string; description: string }> = [
  {
    value: 'none',
    label: 'Selected fields',
    description: 'Only the keys you choose below. Recommended: small, and the app gets exactly what it reads.',
  },
  {
    value: 'fields',
    label: 'All fields',
    description: 'Every column of the row becomes its own key. The column holding the device token is left out.',
  },
  {
    value: 'envelope',
    label: 'Whole row as JSON',
    description:
      'One key, payload, holding the row as JSON text, beside id, operation, table and schema. For apps built for that shape; a wide row will not fit.',
  },
];

/**
 * What the app's code is handed alongside — or instead of — the notification.
 *
 * The section used to open on three choices named after the implementation and
 * then a separate list of "values to add", and people could not tell what any
 * of it was for or which half to fill in. It now says what data is first, and
 * asks one question: which fields does the app get. Ticking a column of the
 * incoming row adds it; the list underneath is the same set of keys, for a
 * value that is not a column — a deep link, a constant.
 *
 * data_mode keeps its stored values; only what they are called changed.
 */
export function FcmDataSection({
  config,
  updateConfig,
  availableFields,
}: {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  availableFields: Array<{ path: string; type?: string }>;
}) {
  // No data_mode is the backend's default and is shown as what it is.
  const mode: DataMode = config.data_mode || 'envelope';
  const dataJson: string = config.data_json || '';
  const fieldPaths = availableFields.map((field) => field.path);
  // The column a message is addressed by is a capability, not content: the
  // backend keeps it out of the data it builds, and the chips do not offer it.
  const withheld = destinationColumns([config.device_token || '', config.topic || '', config.condition || '']);
  const columns = mode === 'none' ? rowColumns(availableFields).filter((column) => !withheld.includes(column)) : [];
  // JSON the rows cannot represent is left exactly as typed, and the chips
  // that would rewrite it are switched off.
  const chipsUsable = rowsFromJsonObject(dataJson) !== null;

  const toggle = (column: string, on: boolean) => {
    const next = toggleColumn(dataJson, column, on);
    if (next !== null) updateConfig('data_json', next);
  };

  return (
    <Fieldset legend="App data" radius="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Hidden key–value pairs for your app's code, never shown to people. Use them to open the right screen — an
          order number, a deep link. Optional: a notification alone needs none.
        </Text>

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

        {columns.length > 0 && (
          <Input.Wrapper
            label="Fields from the incoming row"
            description="Tick a field to send it under its own name."
          >
            <Group gap={6} mt={6}>
              {columns.map((column) => (
                <Chip
                  key={column}
                  size="xs"
                  variant="outline"
                  checked={sendsColumn(dataJson, column)}
                  onChange={(on) => toggle(column, on)}
                  disabled={!chipsUsable}
                  styles={{ label: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
                >
                  {column}
                </Chip>
              ))}
            </Group>
          </Input.Wrapper>
        )}

        <Input.Wrapper
          label={mode === 'none' ? 'Keys sent to the app' : 'Extra keys'}
          description={
            mode === 'none'
              ? 'Each key is text, a {{ }} field, or both.'
              : 'Sent on top of the choice above, and replace a key of the same name.'
          }
        >
          <KeyValueEditor
            value={dataJson}
            onChange={(next) => updateConfig('data_json', next)}
            noun="Key"
            jsonLabel="Keys as JSON"
            jsonPlaceholder='{"deeplink":"app://orders/{{.id}}"}'
            emptyHint={
              mode === 'none'
                ? 'Nothing is sent yet. A deep link is the usual first key: name it deeplink and give it app://orders/{{.id}}.'
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
    </Fieldset>
  );
}
