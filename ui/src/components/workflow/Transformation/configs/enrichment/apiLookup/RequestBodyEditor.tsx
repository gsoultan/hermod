import { useMemo, useRef, useState, type RefObject } from 'react';
import { ActionIcon, Alert, Button, Group, Modal, Stack, Text, Textarea, Tooltip } from '@mantine/core';
import { IconArrowsMaximize, IconInfoCircle } from '@tabler/icons-react';
import { InsertFieldButton } from './InsertFieldButton';
import { formatJsonText, insertBodyToken } from './jsonText';

const MONO = {
  input: {
    fontFamily: 'var(--mantine-font-family-monospace)',
    fontSize: 'var(--mantine-font-size-xs)',
    lineHeight: 1.55,
  },
};

const BODY_RULES =
  'Quote every {{ }}. A field holding an object or array (jsonb) is sent as JSON, not as text, and text is escaped. ' +
  'Sent as application/json unless you set one in the headers.';

/**
 * The request body: big enough to read a real request in, with fields picked
 * rather than typed, and a larger editor one click away.
 *
 * It is a plain textarea, not a JsonInput with formatOnBlur: a blur that
 * rewrites the value re-renders the form between mousedown and mouseup, and the
 * click that caused the blur -- Test API Call, right above -- never happens.
 */
export function RequestBodyEditor({
  value,
  onChange,
  method,
  fieldPaths = [],
}: {
  value: string;
  onChange: (next: string) => void;
  method: string;
  fieldPaths?: string[];
}) {
  const inline = useRef<HTMLTextAreaElement>(null);
  const expandedRef = useRef<HTMLTextAreaElement>(null);
  const [expanded, setExpanded] = useState(false);
  const formatted = useMemo(() => formatJsonText(value), [value]);
  const isJson = value.trim() === '' || formatted !== null;

  // The textarea keeps its selection after it loses focus to the picker, so the
  // token lands where the operator left the cursor.
  const insertAt = (ref: RefObject<HTMLTextAreaElement | null>) => (path: string) => {
    const el = ref.current;
    const start = el?.selectionStart ?? value.length;
    const end = el?.selectionEnd ?? start;
    const { text, caret } = insertBodyToken(value, start, end, path);
    onChange(text);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(caret, caret);
    });
  };

  const toolbar = (ref: RefObject<HTMLTextAreaElement | null>) => (
    <Group justify="space-between" gap="xs" wrap="nowrap">
      <Group gap="xs" wrap="nowrap">
        {fieldPaths.length > 0 && (
          <InsertFieldButton label="Insert field into body" fieldPaths={fieldPaths} onPick={insertAt(ref)} />
        )}
        <Button
          size="compact-xs"
          variant="default"
          disabled={formatted === null}
          onClick={() => formatted !== null && onChange(formatted)}
        >
          Format
        </Button>
      </Group>
      <Text size="xs" c={isJson ? 'dimmed' : 'orange'} ta="right">
        {value.trim() === '' ? 'No body' : isJson ? 'Valid JSON' : 'Not JSON until its {{ }} are filled in'}
      </Text>
    </Group>
  );

  return (
    <Stack gap="xs">
      {method === 'GET' && value.trim() !== '' && (
        <Alert color="orange" variant="light" p="xs" icon={<IconInfoCircle size="1rem" />}>
          <Text size="xs">
            This body is still sent with this GET request. Most APIs ignore a GET body and some refuse the request:
            use POST, or clear the body.
          </Text>
        </Alert>
      )}
      {toolbar(inline)}
      <Textarea
        ref={inline}
        label="Request Body (JSON)"
        description={BODY_RULES}
        placeholder={'{\n  "id": "{{.after.user_id}}"\n}'}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        autosize
        minRows={12}
        maxRows={26}
        spellCheck={false}
        styles={MONO}
        rightSectionPointerEvents="all"
        rightSection={
          // Disabled while the editor is open: the button keeps hover and focus
          // behind the modal, and the tooltip was left painted over it.
          <Tooltip label="Open a larger editor" withArrow position="left" disabled={expanded}>
            <ActionIcon aria-label="Expand body editor" variant="subtle" color="gray" onClick={() => setExpanded(true)}>
              <IconArrowsMaximize size="0.9rem" />
            </ActionIcon>
          </Tooltip>
        }
        rightSectionProps={{ style: { alignItems: 'flex-start', paddingTop: 6 } }}
      />

      <Modal opened={expanded} onClose={() => setExpanded(false)} title="Request body" size="90%" centered>
        <Stack gap="xs">
          {toolbar(expandedRef)}
          <Textarea
            ref={expandedRef}
            aria-label="Request body"
            description={BODY_RULES}
            value={value}
            onChange={(event) => onChange(event.currentTarget.value)}
            autosize
            minRows={24}
            maxRows={40}
            spellCheck={false}
            styles={MONO}
            data-autofocus
          />
          <Group justify="flex-end">
            <Button onClick={() => setExpanded(false)}>Done</Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
