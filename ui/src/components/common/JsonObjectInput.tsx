import { JsonInput } from '@mantine/core';
import { useEffect, useMemo, useRef, useState } from 'react';

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
 */
export function JsonObjectInput({
  value,
  onChange,
  label,
  description,
  placeholder,
  minRows = 10,
  styles,
}: {
  /** Canonical object owned by the caller. */
  value: Record<string, unknown>;
  /** Called only with a parsed JSON object. */
  onChange: (next: Record<string, unknown>) => void;
  label?: string;
  description?: string;
  placeholder?: string;
  minRows?: number;
  styles?: Record<string, unknown>;
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

  // What this editor last pushed upstream. Used to tell "the value changed
  // because of me" from "the value changed because something else edited it".
  const ownCommit = useRef(serialised);

  useEffect(() => {
    if (serialised === ownCommit.current) return;
    ownCommit.current = serialised;
    setDraft(serialised);
    setError(null);
  }, [serialised]);

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

    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setError('Must be a JSON object, for example {"column.status": "active"}.');
      return;
    }

    setError(null);
    // Record the canonical form, not the draft: upstream will hand back the
    // canonical form and we must recognise it as our own.
    ownCommit.current = JSON.stringify(parsed, null, 2);
    onChange(parsed as Record<string, unknown>);
  };

  return (
    <JsonInput
      label={label}
      description={description}
      placeholder={placeholder}
      value={draft}
      onChange={handleChange}
      error={error}
      // Deliberately no formatOnBlur: it rewrites the text under the caret, and
      // the draft is the user's to format.
      minRows={minRows}
      styles={styles as any}
    />
  );
}
