import { ActionIcon, Button, Group, JsonInput, Modal, Stack, Tooltip } from '@mantine/core';
import { IconArrowsMaximize, IconIndentIncrease } from '@tabler/icons-react';
import { useEffect, useMemo, useRef, useState } from 'react';

type JsonDocument = Record<string, unknown> | unknown[];

function isAcceptedDocument(parsed: unknown, allowArray: boolean): boolean {
  return parsed !== null && typeof parsed === 'object' && (allowArray || !Array.isArray(parsed));
}

/**
 * A JSON object editor that lets you finish typing.
 *
 * The raw-JSON panes in the transformation form were controlled directly off the
 * node config: `value` was re-serialised from `selectedNode.data` on every
 * render, and `onChange` parsed the text and wrote it straight back, swallowing
 * failures in an empty `catch`.
 *
 * Half-typed JSON is invalid JSON, so every keystroke between `{` and a complete
 * document failed to parse. Nothing committed, nothing said why, and the next
 * unrelated re-render — a preview finishing, say — replaced whatever the user
 * had typed with the last serialised config. When a keystroke *did* parse, the
 * value handed back was re-serialised with different whitespace than the user
 * had typed, which moved the caret to the end.
 *
 * Here the text being edited is local state. Upstream is only written to when
 * the document parses, and upstream only overwrites the draft when the change
 * came from somewhere other than this editor.
 *
 * It grows with its content: `minRows` alone does nothing on a textarea that is
 * not autosized, which left the Set Fields pane two lines tall. With `toolbar`
 * it also offers Format and a larger editor over the same draft, the way the
 * API lookup's request body does.
 */
export function JsonObjectInput<V extends JsonDocument = Record<string, unknown>>({
  value,
  onChange,
  label,
  'aria-label': ariaLabel,
  description,
  placeholder,
  minRows = 10,
  maxRows = 26,
  styles,
  allowArray = false,
  toolbar = false,
}: {
  /** Canonical document owned by the caller. */
  value: V;
  /** Called only with a parsed JSON object -- or array, with `allowArray`. */
  onChange: (next: V) => void;
  label?: string;
  /** Names the editor when no visible label is rendered, as in a list row. */
  'aria-label'?: string;
  description?: string;
  placeholder?: string;
  minRows?: number;
  maxRows?: number;
  styles?: Record<string, unknown>;
  /** Accept a top-level array as well as an object. */
  allowArray?: boolean;
  /** Offer Format, and a larger editor in a modal. */
  toolbar?: boolean;
}) {
  const serialised = useMemo(() => {
    try {
      return JSON.stringify(value ?? {}, null, 2);
    } catch {
      return '{}';
    }
  }, [value]);

  const [draft, setDraft] = useState(serialised);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);

  // What this editor last pushed upstream. Used to tell "the value changed
  // because of me" from "the value changed because something else edited it".
  const ownCommit = useRef(serialised);

  useEffect(() => {
    if (serialised === ownCommit.current) return;
    ownCommit.current = serialised;
    setDraft(serialised);
    setError(null);
  }, [serialised]);

  // The draft laid out, or null while it is not a document this editor takes.
  const formatted = useMemo(() => {
    try {
      const parsed: unknown = JSON.parse(draft);
      return isAcceptedDocument(parsed, allowArray) ? JSON.stringify(parsed, null, 2) : null;
    } catch {
      return null;
    }
  }, [draft, allowArray]);

  const handleChange = (next: string) => {
    setDraft(next);

    if (!next.trim()) {
      setError(null);
      return;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(next);
    } catch {
      // Not an error the user needs shouting about — they are mid-word — but
      // they do need to know the pane is not being applied yet.
      setError('Not valid JSON yet, so this is not being applied.');
      return;
    }

    if (!isAcceptedDocument(parsed, allowArray)) {
      setError(
        allowArray
          ? 'Must be a JSON object or array, for example {"session": "source.token"}.'
          : 'Must be a JSON object, for example {"column.status": "active"}.',
      );
      return;
    }

    setError(null);
    // Record the canonical form, not the draft: upstream will hand back the
    // canonical form and we must recognise it as our own.
    ownCommit.current = JSON.stringify(parsed, null, 2);
    onChange(parsed as V);
  };

  // Format changes only the layout of a draft that already committed, so
  // upstream has nothing new to hear.
  const format = () => {
    if (formatted !== null) setDraft(formatted);
  };

  const inputStyles = styles as any;

  return (
    <>
      <JsonInput
        label={label}
        aria-label={label ? undefined : ariaLabel}
        description={description}
        placeholder={placeholder}
        value={draft}
        onChange={handleChange}
        error={error}
        // Deliberately no formatOnBlur: it rewrites the text under the caret, and
        // the draft is the user's to format.
        autosize
        minRows={minRows}
        maxRows={maxRows}
        spellCheck={false}
        styles={inputStyles}
        rightSectionPointerEvents={toolbar ? 'all' : undefined}
        rightSectionProps={toolbar ? { style: { alignItems: 'flex-start', paddingTop: 6 } } : undefined}
        rightSection={
          toolbar ? (
            <Stack gap={2}>
              {/* Disabled while the editor is open: the button keeps hover and
                  focus behind the modal, and the tooltip stayed painted over it. */}
              <Tooltip label="Open a larger editor" withArrow position="left" disabled={expanded}>
                <ActionIcon
                  aria-label="Expand JSON editor"
                  variant="subtle"
                  color="gray"
                  onClick={() => setExpanded(true)}
                >
                  <IconArrowsMaximize size="0.9rem" />
                </ActionIcon>
              </Tooltip>
              <Tooltip label="Format JSON" withArrow position="left" disabled={expanded || formatted === null}>
                <ActionIcon
                  aria-label="Format"
                  variant="subtle"
                  color="gray"
                  disabled={formatted === null}
                  onClick={format}
                >
                  <IconIndentIncrease size="0.9rem" />
                </ActionIcon>
              </Tooltip>
            </Stack>
          ) : undefined
        }
      />

      {toolbar && (
        <Modal
          opened={expanded}
          onClose={() => setExpanded(false)}
          title={label ?? ariaLabel ?? 'JSON'}
          size="90%"
          centered
        >
          <Stack gap="xs">
            <Group>
              <Button size="compact-xs" variant="default" disabled={formatted === null} onClick={format}>
                Format
              </Button>
            </Group>
            <JsonInput
              aria-label={label ?? ariaLabel}
              description={description}
              value={draft}
              onChange={handleChange}
              error={error}
              autosize
              minRows={24}
              maxRows={40}
              spellCheck={false}
              styles={inputStyles}
              data-autofocus
            />
            <Group justify="flex-end">
              <Button onClick={() => setExpanded(false)}>Done</Button>
            </Group>
          </Stack>
        </Modal>
      )}
    </>
  );
}
