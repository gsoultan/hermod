import { Badge, Code, Group, Paper, SegmentedControl, Stack, Text } from '@mantine/core';
import { IconEdit } from '@tabler/icons-react';
import { Suspense, lazy, useState } from 'react';
import { JsonObjectInput } from '@/components/common/JsonObjectInput';
import { useColumnFields } from '@/components/common/useColumnFields';

const SetFieldEditor = lazy(() =>
  import('./SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);

const MONO_XS = {
  input: {
    fontFamily: 'var(--mantine-font-family-monospace)',
    fontSize: 'var(--mantine-font-size-xs)',
    lineHeight: 1.55,
  },
};

const VIEWS = [
  { label: 'Visual', value: 'visual' },
  { label: 'JSON', value: 'json' },
];

/**
 * How a value is written, as the engine reads it (evaluator.EvaluateAdvancedExpression).
 * The panel used to say "Use {{.field}}", a spelling no value resolved, while
 * every other hint in the drawer said `source.`. Both are read now.
 */
const SYNTAX: Array<[string, string]> = [
  ['source.after.status', 'copies a field'],
  ['lower(source.name)', 'calls a function'],
  ['Bearer {{source.after.token}}', 'builds text'],
];

function ValueSyntax() {
  return (
    <Paper withBorder radius="md" p="xs">
      <Stack gap={4}>
        <Text size="xs" fw={600}>
          Writing a value
        </Text>
        {SYNTAX.map(([example, meaning]) => (
          <Group key={example} gap="xs" wrap="nowrap" align="baseline">
            <Code style={{ whiteSpace: 'nowrap', flexShrink: 0 }}>{example}</Code>
            <Text size="xs" c="dimmed">
              {meaning}
            </Text>
          </Group>
        ))}
        <Text size="xs" c="dimmed">
          In a JSON value, text starting with <Code>source.</Code> or holding <Code>{'{{ }}'}</Code> is
          read; other text is kept as written.
        </Text>
      </Stack>
    </Paper>
  );
}

interface ColumnFieldsEditorProps {
  config: any;
  nodeId: string;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  availableFields: any[];
  transType: string;
  addField: (path?: string, value?: string) => void;
  title: string;
  summary: string;
  jsonLabel: string;
  jsonPlaceholder: string;
}

/**
 * The `column.*` keys of a `set` or `advanced` node, as rows or as JSON -- one
 * at a time.
 *
 * Both used to be on screen together, each a live copy of the other, and the
 * JSON one was two lines tall. Each now has the column to itself: the rows get
 * full-width inputs, and the JSON grows with its content and opens in a larger
 * editor.
 */
export function ColumnFieldsEditor({
  config,
  nodeId,
  updateNodeConfig,
  availableFields,
  transType,
  addField,
  title,
  summary,
  jsonLabel,
  jsonPlaceholder,
}: ColumnFieldsEditorProps) {
  const { columnFields, replaceColumnFields } = useColumnFields(config, nodeId, updateNodeConfig);
  const [view, setView] = useState('visual');
  const count = Object.keys(columnFields).length;

  return (
    <Stack gap="sm">
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
        <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
          <Group gap={6} wrap="nowrap">
            <IconEdit size="1rem" color="var(--mantine-color-indigo-6)" />
            <Text size="sm" fw={600}>
              {title}
            </Text>
            <Badge size="xs" variant="light" color="gray">
              {count}
            </Badge>
          </Group>
          <Text size="xs" c="dimmed">
            {summary}
          </Text>
        </Stack>
        <SegmentedControl
          aria-label="Editor view"
          size="xs"
          data={VIEWS}
          value={view}
          onChange={setView}
          style={{ flexShrink: 0 }}
        />
      </Group>

      <ValueSyntax />

      {view === 'visual' ? (
        <Suspense fallback={<Text size="xs" p="md">Loading editor…</Text>}>
          <SetFieldEditor
            selectedNode={{ id: nodeId, data: config }}
            updateNodeConfig={updateNodeConfig}
            availableFields={availableFields}
            transType={transType}
            addField={addField}
          />
        </Suspense>
      ) : (
        <JsonObjectInput
          label={jsonLabel}
          description='Keys are "column." plus the target path, as in "column.user.id". Values are written the same way as in a row.'
          placeholder={jsonPlaceholder}
          value={columnFields}
          onChange={replaceColumnFields}
          toolbar
          minRows={14}
          maxRows={32}
          styles={MONO_XS}
        />
      )}
    </Stack>
  );
}
