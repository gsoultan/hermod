import {
  ActionIcon,
  Autocomplete,
  Button,
  Group,
  Paper,
  SegmentedControl,
  Stack,
  Tooltip,
  rem,
} from '@mantine/core';
import { useMemo, useState } from 'react';
import { IconBracketsContain, IconPlus, IconTrash } from '@tabler/icons-react';
import { EmptyState } from '@/components/common/EmptyState';
import { JsonObjectInput } from '@/components/common/JsonObjectInput';
import { TemplateField } from '@/components/shared/TemplateField';
import {
  isJsonColumnValue,
  listColumnFields,
  removeColumnField,
  renameColumnField,
  toExpressionText,
  toJsonColumnValue,
  type JsonColumnValue,
} from './columnFields';

const MONO = { input: { fontFamily: 'var(--mantine-font-family-monospace)' } };
const MONO_XS = {
  input: {
    fontFamily: 'var(--mantine-font-family-monospace)',
    fontSize: 'var(--mantine-font-size-xs)',
    lineHeight: 1.55,
  },
};

const VALUE_TYPES = [
  { label: 'Expression', value: 'expression' },
  { label: 'JSON', value: 'json' },
];

interface SetFieldEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: any[];
  transType: string;
  addField: (path?: string, value?: string) => void;
}

/**
 * The rows of a `set` / `advanced` node: one target path and one value each.
 *
 * The path and the value each get the row's full width. They used to share a
 * `Group grow`, which caps every child at an equal share, so in the drawer's
 * middle column both were cut to a dozen characters. A value is an expression
 * or a JSON document; the second is edited as JSON rather than shown as
 * `String(value)`, which printed "[object Object]" and replaced the object with
 * that text on the next keystroke.
 */
export function SetFieldEditor({
  selectedNode,
  updateNodeConfig,
  availableFields = [],
  transType,
  addField,
}: SetFieldEditorProps) {
  const fieldPaths = useMemo(() =>
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  const fields = listColumnFields(selectedNode.data);

  /**
   * Paths typed into a row that have not been committed to the config, keyed by
   * the row's current config key.
   *
   * A row's identity *is* its `column.<path>` key, so a path another row already
   * holds cannot be written — the object would keep one of the two and the other
   * row, with its value, would vanish. The text stays on screen with the
   * collision named, and commits as soon as it is unique again.
   */
  const [draftPaths, setDraftPaths] = useState<Record<string, string>>({});

  const forgetDraft = (fullKey: string) =>
    setDraftPaths(({ [fullKey]: _dropped, ...rest }) => rest);

  /**
   * Commits a rebuilt config, applying any held rename it has made possible.
   *
   * A draft only exists because its path collided, so freeing that path — by
   * renaming or deleting the row that owned it — settles it. Doing that here,
   * in the same update, is what keeps a blocked rename from being stranded:
   * blur fires *before* the click that caused it, so a delete button cannot
   * flush the draft on its way past.
   */
  const commit = (next: Record<string, any>) => {
    let merged = next;
    const settled: string[] = [];
    for (const [fullKey, path] of Object.entries(draftPaths)) {
      if (!(fullKey in merged)) {
        // The row is gone, or was itself just renamed. Either way the draft has
        // nothing left to apply to.
        settled.push(fullKey);
        continue;
      }
      const renamed = renameColumnField(merged, fullKey, path);
      if (renamed) {
        merged = renamed;
        settled.push(fullKey);
      }
    }
    if (settled.length > 0) {
      setDraftPaths((d) => {
        const rest = { ...d };
        for (const k of settled) delete rest[k];
        return rest;
      });
    }
    updateNodeConfig(selectedNode.id, merged, true);
  };

  const updateFieldPath = (oldFullKey: string, newPath: string) => {
    const currentPath = oldFullKey.slice('column.'.length);
    if (newPath === currentPath) {
      forgetDraft(oldFullKey);
      return;
    }
    // Renames in place. Rebuilding as `{ ...others, [newKey]: value }` moved the
    // edited row to the bottom of the list on every keystroke, which — with rows
    // keyed by position — left the caret in whichever row had moved up into it.
    const next = renameColumnField(selectedNode.data, oldFullKey, newPath);
    if (!next) {
      setDraftPaths((d) => ({ ...d, [oldFullKey]: newPath }));
      return;
    }
    commit(next);
  };

  const updateFieldValue = (fullKey: string, newValue: unknown) => {
    updateNodeConfig(selectedNode.id, { [fullKey]: newValue });
  };

  // Switching keeps the value: JSON text becomes the document, a document
  // becomes its JSON text, and any other expression is kept inside the object.
  const setValueType = (fullKey: string, type: string) => {
    const current = selectedNode.data[fullKey];
    updateFieldValue(fullKey, type === 'json' ? toJsonColumnValue(current) : toExpressionText(current));
  };

  const removeField = (fullKey: string) => {
    commit(removeColumnField(selectedNode.data, fullKey));
  };

  const isAdvanced = transType === 'advanced';

  return (
    <Stack gap="xs">
      {fields.length === 0 ? (
        <Paper withBorder radius="md" style={{ borderStyle: 'dashed' }}>
          <EmptyState
            compact
            icon={<IconBracketsContain style={{ width: rem(20), height: rem(20) }} />}
            title={isAdvanced ? 'No rules yet' : 'No field mappings yet'}
            description="Add one below, or press + beside a field in Available Fields to copy it here."
          />
        </Paper>
      ) : (
        <Stack gap="xs">
          {/*
            Keyed by position, not by `field.fullKey`: the key changes on every
            keystroke in Target Path, and React would unmount the input the user
            is typing into. Position is stable because a rename no longer
            reorders the list.
          */}
          {fields.map((field, index) => {
            const draft = draftPaths[field.fullKey];
            const pathValue = draft ?? field.path;
            // Read from the config as it stands, not from the draft's mere
            // existence: delete or rename the row that owned the path and the
            // collision is over, so the message must not outlive it. `commit`
            // applies the held rename in that same update.
            const duplicate =
              draft !== undefined && `column.${draft}` in selectedNode.data
                ? `"${draft}" is already set by another row.`
                : undefined;
            const isJson = isJsonColumnValue(field.value);
            return (
              <Paper key={index} withBorder p="xs" radius="md">
                <Stack gap={6}>
                  {/* Wraps rather than squeezes: in a narrow drawer the type
                      switch and delete drop below the path, which keeps its
                      width instead of being cut to a dozen characters. */}
                  <Group gap="xs" wrap="wrap" align="flex-start">
                    <Autocomplete
                      aria-label="Target path"
                      placeholder="e.g. user.id"
                      data={fieldPaths}
                      size="xs"
                      leftSection={<IconBracketsContain size={rem(14)} />}
                      value={pathValue}
                      error={duplicate}
                      onChange={(val) => updateFieldPath(field.fullKey, val)}
                      onBlur={() => {
                        // Nothing is unmounted by a rename-in-place, so this
                        // cannot eat the click that caused the blur.
                        if (draft !== undefined) updateFieldPath(field.fullKey, draft);
                      }}
                      styles={MONO}
                      style={{ flex: '1 1 12rem', minWidth: 0 }}
                    />
                    <Group gap="xs" wrap="nowrap" ml="auto">
                      <SegmentedControl
                        aria-label="Value type"
                        size="xs"
                        data={VALUE_TYPES}
                        value={isJson ? 'json' : 'expression'}
                        onChange={(type) => setValueType(field.fullKey, type)}
                      />
                      <Tooltip label="Remove field">
                        <ActionIcon
                          aria-label="Remove field"
                          color="red"
                          variant="subtle"
                          onClick={() => removeField(field.fullKey)}
                        >
                          <IconTrash size={rem(16)} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Group>
                  {isJson ? (
                    <JsonObjectInput<JsonColumnValue>
                      aria-label="JSON value"
                      allowArray
                      value={field.value as JsonColumnValue}
                      onChange={(next) => updateFieldValue(field.fullKey, next)}
                      minRows={6}
                      maxRows={24}
                      styles={MONO_XS}
                      toolbar
                    />
                  ) : (
                    <TemplateField
                      aria-label="Value or expression"
                      placeholder="e.g. source.name or lower(source.name)"
                      value={toExpressionText(field.value)}
                      onChange={(val) => updateFieldValue(field.fullKey, val)}
                      availableFields={availableFields}
                      buildToken={(p) => `source.${p}`}
                      buildSecretToken={(name) => `secret("${name}")`}
                      multiline
                    />
                  )}
                </Stack>
              </Paper>
            );
          })}
        </Stack>
      )}

      <Button
        size="xs"
        variant="light"
        fullWidth
        leftSection={<IconPlus size={rem(16)} />}
        onClick={() => addField()}
      >
        {isAdvanced ? 'Add Transformation Rule' : 'Add New Field Mapping'}
      </Button>
    </Stack>
  );
}
