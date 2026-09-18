import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Stack,
  Text,
  Autocomplete,
  rem,
  Paper,
  Box,
  Tooltip,
} from '@mantine/core';
import { useMemo, useState } from 'react';
import {
  IconBracketsContain,
  IconPlus,
  IconTrash,
  IconCirclePlus,
  IconEdit,
} from '@tabler/icons-react';
import { TemplateField } from '../../shared/TemplateField';
import {
  listColumnFields,
  removeColumnField,
  renameColumnField,
} from './columnFields';

interface SetFieldEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: any[];
  incomingPayload?: any;
  transType: string;
  onAddFromSource: (path: string) => void;
  addField: (path?: string, value?: string) => void;
}

export function SetFieldEditor({
  selectedNode,
  updateNodeConfig,
  availableFields = [],
  incomingPayload,
  transType,
  onAddFromSource,
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

  const updateFieldValue = (fullKey: string, newValue: any) => {
    updateNodeConfig(selectedNode.id, { [fullKey]: newValue });
  };

  const removeField = (fullKey: string) => {
    commit(removeColumnField(selectedNode.data, fullKey));
  };

  const isAdvanced = transType === 'advanced';

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start">
        <Stack gap={0}>
          <Group gap="xs">
            <IconEdit size={rem(18)} className="text-indigo-500" />
            <Text size="sm" fw={600}>
              {isAdvanced ? 'Transformation Rules' : 'Field Mappings'}
            </Text>
          </Group>
          <Text size="xs" c="dimmed">
            {isAdvanced
              ? 'Define complex transformations using expressions.'
              : 'Set or update fields in the outgoing payload.'}
          </Text>
        </Stack>

        {incomingPayload && (
          <Box>
            <Text size="xs" fw={700} c="dimmed" mb={4} tt="uppercase" ta="right">
              Quick add from source
            </Text>
            <Group gap={4} justify="flex-end">
              {fieldPaths.slice(0, 5).map((f) => (
                <Badge
                  key={f}
                  size="xs"
                  variant="light"
                  color="blue"
                  className="hover:scale-105 transition-transform cursor-pointer"
                  onClick={() => onAddFromSource(f)}
                  leftSection={<IconPlus size={rem(10)} />}
                  styles={{ label: { textTransform: 'none' } }}
                >
                  {f}
                </Badge>
              ))}
            </Group>
          </Box>
        )}
      </Group>

      {fields.length === 0 ? (
        <Paper
          withBorder
          p="xl"
          radius="md"
          bg="var(--mantine-color-gray-0)"
          className="dark:bg-dark-7"
          style={{ borderStyle: 'dashed', textAlign: 'center' }}
        >
          <Stack gap="xs" align="center">
            <IconCirclePlus size={rem(32)} className="text-gray-400" />
            <Text size="xs" c="dimmed">
              No fields defined yet. Click "Add Field" or use the quick-add badges above to start.
            </Text>
          </Stack>
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
            return (
            <Paper key={index} withBorder p="xs" radius="md" className="hover:border-indigo-200 transition-colors">
              <Group grow gap="xs" align="flex-start">
                <Box style={{ flex: 1.5 }}>
                  <Text size="xs" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Target Path
                  </Text>
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
                    styles={{ input: { fontFamily: 'monospace' } }}
                  />
                </Box>
                <Box style={{ flex: 3 }}>
                  <Text size="xs" fw={700} c="dimmed" mb={2} tt="uppercase" ml={2}>
                    Value / Expression
                  </Text>
                  <TemplateField
                    aria-label="Value or expression"
                    placeholder="Value or expression (e.g. source.name, lower(source.name))"
                    value={String(field.value || '')}
                    onChange={(val) => updateFieldValue(field.fullKey, val)}
                    availableFields={availableFields}
                    buildToken={(p) => `source.${p}`}
                    multiline={isAdvanced}
                  />
                  <Text size="xs" c="dimmed" mt={2}>
                    Use functions and source paths. Click {"{x}"} to insert fields.
                  </Text>
                </Box>
                <Box style={{ flex: 'none', alignSelf: 'center' }}>
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
                </Box>
              </Group>
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


