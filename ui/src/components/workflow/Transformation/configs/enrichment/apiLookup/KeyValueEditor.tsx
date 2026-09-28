import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ActionIcon, Autocomplete, Button, Group, SegmentedControl, Stack, Text, TextInput, Textarea } from '@mantine/core';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { InsertFieldButton } from './InsertFieldButton';
import { fieldToken, formatJsonText, jsonObjectFromRows, repeatedNames, rowsFromJsonObject, type NameValue } from './jsonText';

type Row = NameValue & { id: number };

const MONO = { input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-xs)' } };

/**
 * Edits a headers or query-params object -- stored as JSON object text, which
 * is what the backend parses -- as name/value rows, with the JSON itself one
 * click away.
 *
 * Rows are local state keyed by a stable id, never by their name: a row whose
 * name is the key unmounts its own input mid-keystroke. Every edit commits the
 * rows at once rather than on blur, because blur fires before the click that
 * caused it. JSON that is half-typed is held, not applied; JSON the rows cannot
 * represent without changing the request opens as JSON and stays exactly as
 * written.
 */
export function KeyValueEditor({
  value,
  onChange,
  noun,
  jsonLabel,
  jsonPlaceholder,
  emptyHint,
  keySuggestions,
  caseInsensitiveKeys = false,
  fieldPaths = [],
}: {
  value: string;
  onChange: (next: string) => void;
  /** One row, capitalised: "Header", "Query param". */
  noun: string;
  jsonLabel: string;
  jsonPlaceholder?: string;
  emptyHint: ReactNode;
  keySuggestions?: string[];
  /** Header names are compared the way HTTP compares them; query params are not. */
  caseInsensitiveKeys?: boolean;
  fieldPaths?: string[];
}) {
  const nextId = useRef(0);
  const withIds = (rows: NameValue[]): Row[] => rows.map((r) => ({ ...r, id: nextId.current++ }));

  const [rows, setRows] = useState<Row[]>(() => withIds(rowsFromJsonObject(value) ?? []));
  const [mode, setMode] = useState<'rows' | 'json'>(() => (rowsFromJsonObject(value) === null ? 'json' : 'rows'));
  const [draft, setDraft] = useState(value);
  const [draftError, setDraftError] = useState<string | null>(null);

  // What this editor last wrote. A value that differs came from somewhere else
  // -- a preset, an import, the other mode -- and replaces what is on screen.
  const ownCommit = useRef(value);
  useEffect(() => {
    if (value === ownCommit.current) return;
    ownCommit.current = value;
    setDraft(value);
    setDraftError(null);
    const parsed = rowsFromJsonObject(value);
    if (parsed === null) {
      setMode('json');
    } else {
      setRows(withIds(parsed));
    }
    // withIds only reads a ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const commit = (text: string) => {
    ownCommit.current = text;
    if (text !== value) onChange(text);
  };

  const commitRows = (next: Row[]) => {
    setRows(next);
    commit(jsonObjectFromRows(next));
  };

  const setRow = (index: number, patch: Partial<NameValue>) =>
    commitRows(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));

  const onDraft = (text: string) => {
    setDraft(text);
    if (!text.trim()) {
      setDraftError(null);
      commit('');
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch {
      setDraftError('Not valid JSON yet, so this is not being applied.');
      return;
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setDraftError('This has to be a JSON object, like {"name": "value"}. It is not being applied.');
      return;
    }
    setDraftError(null);
    commit(text);
  };

  const rowsPossible = rowsFromJsonObject(mode === 'json' ? draft : value) !== null;
  const switchMode = (next: string) => {
    if (next === 'rows') {
      const parsed = rowsFromJsonObject(draft);
      if (parsed === null) return;
      setRows(withIds(parsed));
    } else {
      setDraft(value);
      setDraftError(null);
    }
    setMode(next as 'rows' | 'json');
  };

  const repeated = repeatedNames(rows, caseInsensitiveKeys);
  const lower = noun.toLowerCase();

  return (
    <Stack gap="xs">
      <Group justify="space-between" gap="xs">
        <SegmentedControl
          size="xs"
          value={mode}
          onChange={switchMode}
          data={[
            { label: 'Rows', value: 'rows', disabled: mode === 'json' && !rowsPossible },
            { label: 'JSON', value: 'json' },
          ]}
        />
        {mode === 'json' && (
          <Button
            size="compact-xs"
            variant="default"
            disabled={formatJsonText(draft) === null}
            onClick={() => {
              const formatted = formatJsonText(draft);
              if (formatted !== null) onDraft(formatted);
            }}
          >
            Format
          </Button>
        )}
      </Group>

      {mode === 'rows' ? (
        <>
          {rows.length === 0 && (
            <Text size="xs" c="dimmed">
              {emptyHint}
            </Text>
          )}
          {rows.map((row, i) => {
            const nameLabel = `${noun} ${i + 1} name`;
            const valueLabel = `${noun} ${i + 1} value`;
            return (
              <Group key={row.id} gap="xs" wrap="nowrap" align="flex-start">
                {keySuggestions ? (
                  <Autocomplete
                    aria-label={nameLabel}
                    placeholder="Name"
                    data={keySuggestions}
                    value={row.key}
                    onChange={(next) => setRow(i, { key: next })}
                    styles={MONO}
                    style={{ flex: '0 0 38%', minWidth: 0 }}
                  />
                ) : (
                  <TextInput
                    aria-label={nameLabel}
                    placeholder="Name"
                    value={row.key}
                    onChange={(event) => {
                      const next = event.currentTarget.value;
                      setRow(i, { key: next });
                    }}
                    styles={MONO}
                    style={{ flex: '0 0 38%', minWidth: 0 }}
                  />
                )}
                <TextInput
                  aria-label={valueLabel}
                  placeholder="Value, or a {{ }} field"
                  value={row.value}
                  onChange={(event) => {
                    const next = event.currentTarget.value;
                    setRow(i, { value: next });
                  }}
                  styles={MONO}
                  style={{ flex: 1, minWidth: 0 }}
                  rightSection={
                    fieldPaths.length > 0 ? (
                      <InsertFieldButton
                        compact
                        label={`Insert field into ${lower} ${i + 1} value`}
                        fieldPaths={fieldPaths}
                        onPick={(path) => setRow(i, { value: row.value + fieldToken(path) })}
                      />
                    ) : undefined
                  }
                />
                <ActionIcon
                  aria-label={`Remove ${lower} ${i + 1}`}
                  variant="subtle"
                  color="red"
                  mt={4}
                  onClick={() => commitRows(rows.filter((_, j) => j !== i))}
                >
                  <IconTrash size="0.9rem" />
                </ActionIcon>
              </Group>
            );
          })}
          {repeated.length > 0 && (
            <Text size="xs" c="orange">
              {repeated.map((name) => `"${name}"`).join(', ')} {repeated.length === 1 ? 'is' : 'are'} set more than
              once. Only the last one is sent.
            </Text>
          )}
          <Group>
            <Button
              size="compact-sm"
              variant="light"
              leftSection={<IconPlus size="0.9rem" />}
              onClick={() => setRows([...rows, ...withIds([{ key: '', value: '' }])])}
            >
              Add {lower}
            </Button>
          </Group>
        </>
      ) : (
        <>
          {!rowsPossible && !draftError && draft.trim() !== '' && (
            <Text size="xs" c="dimmed">
              These can&apos;t be shown as rows: a value is an object, an array, null or a very large number, and
              rows would change what is sent. Edit them here as JSON.
            </Text>
          )}
          <Textarea
            label={jsonLabel}
            placeholder={jsonPlaceholder}
            value={draft}
            onChange={(event) => onDraft(event.currentTarget.value)}
            error={draftError}
            autosize
            minRows={6}
            maxRows={18}
            spellCheck={false}
            styles={MONO}
          />
        </>
      )}
    </Stack>
  );
}
