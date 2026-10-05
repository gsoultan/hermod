import { Button, Code, Group, Paper, Stack, Text, Tooltip } from '@mantine/core';
import { IconTrash } from '@tabler/icons-react';
import type { SqlIntent, SqlTemplate } from './templates';

interface TemplatesPanelProps {
  intent: SqlIntent;
  templates: SqlTemplate[];
  keywords: string[];
  /** The table the templates are written for, when one is open in Schema. */
  table: string | null;
  /** Whether using a template would discard something. */
  hasStatement: boolean;
  onUseTemplate: (sql: string) => void;
  onInsert: (text: string) => void;
  onClear: () => void;
}

/**
 * Whole statements to start from, and single keywords to insert.
 *
 * A template replaces the statement rather than inserting into it -- a second
 * INSERT in the middle of the first is never what was meant -- so the button
 * says so, and the builder keeps the old text to put back.
 */
export function TemplatesPanel({
  intent, templates, keywords, table, hasStatement, onUseTemplate, onInsert, onClear,
}: TemplatesPanelProps) {
  return (
    <Stack gap="md">
      <Stack gap="xs">
        <Text size="xs" fw={700} c="dimmed" tt="uppercase">Statement templates</Text>
        <Text size="xs" c="dimmed">
          {table
            ? <>Written for <b>{table}</b>, with a variable per column.</>
            : 'Open a table in Schema and these are written for it, with a variable per column.'}
        </Text>
        {templates.map((t) => (
          <Paper key={t.id} withBorder p="xs" radius="md" data-testid={`sql-template-${t.id}`}>
            <Stack gap={6}>
              <Group justify="space-between" wrap="nowrap" align="flex-start">
                <Stack gap={0} style={{ minWidth: 0 }}>
                  <Text size="xs" fw={600}>{t.label}</Text>
                  <Text size="xs" c="dimmed">{t.description}</Text>
                </Stack>
                <Button
                  size="compact-xs"
                  variant="light"
                  style={{ flex: 'none' }}
                  aria-label={`Use the "${t.label}" template`}
                  onClick={() => onUseTemplate(t.sql)}
                >
                  {hasStatement ? 'Replace' : 'Use'}
                </Button>
              </Group>
              <Code block style={{ fontSize: 'var(--mantine-font-size-xs)', whiteSpace: 'pre', overflowX: 'auto' }}>
                {t.sql}
              </Code>
            </Stack>
          </Paper>
        ))}
      </Stack>

      <Stack gap="xs">
        <Text size="xs" fw={700} c="dimmed" tt="uppercase">Quick insert</Text>
        <Group gap={6}>
          {keywords.map((kw) => (
            <Button key={kw} size="compact-xs" variant="default" onClick={() => onInsert(kw)}>
              {kw}
            </Button>
          ))}
          {/* Only a batch query has a last value to resume from. */}
          {intent === 'read' && (
            <Tooltip label="Insert dynamic last value variable">
              <Button size="compact-xs" variant="light" color="orange" onClick={() => onInsert('{{.last_value}}')}>
                {'{{.last_value}}'}
              </Button>
            </Tooltip>
          )}
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            leftSection={<IconTrash size={12} />}
            onClick={onClear}
          >
            Clear
          </Button>
        </Group>
      </Stack>
    </Stack>
  );
}
