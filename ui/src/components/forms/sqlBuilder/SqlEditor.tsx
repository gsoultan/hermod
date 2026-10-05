import { useRef, useState } from 'react';
import { Box } from '@mantine/core';

const MONO = 'JetBrains Mono, Menlo, Monaco, Courier New, monospace';
const FONT_SIZE = '13px';
const LINE_HEIGHT = 1.6;
const PAD_Y = 10;

interface SqlEditorProps {
  value: string;
  onChange: (value: string) => void;
  /** Cmd/Ctrl + Enter. */
  onRun: () => void;
  /** Reports the textarea when it takes focus, so inserts land at its caret. */
  onFocusEditor?: (el: HTMLTextAreaElement) => void;
  label: string;
  placeholder?: string;
  /** Starting height; the editor can be dragged taller. */
  height: number | string;
}

/**
 * A statement editor with a line-number gutter.
 *
 * Lines do not wrap. A wrapped line is one line of SQL drawn as several, so the
 * gutter could not number it honestly, and a statement that scrolls sideways
 * keeps the shape it was written in.
 */
export function SqlEditor({ value, onChange, onRun, onFocusEditor, label, placeholder, height }: SqlEditorProps) {
  const gutterRef = useRef<HTMLDivElement | null>(null);
  const [focused, setFocused] = useState(false);
  const lineCount = Math.max(1, value.split('\n').length);

  return (
    <Box
      data-testid="sql-editor"
      style={{
        display: 'flex',
        height,
        minHeight: 140,
        resize: 'vertical',
        overflow: 'hidden',
        borderRadius: 'var(--mantine-radius-md)',
        border: `1px solid ${focused ? 'var(--mantine-primary-color-filled)' : 'var(--mantine-color-default-border)'}`,
        background: 'var(--mantine-color-default-hover)',
      }}
    >
      <Box
        ref={gutterRef}
        aria-hidden
        style={{
          flex: 'none',
          minWidth: 44,
          padding: `${PAD_Y}px 8px`,
          overflow: 'hidden',
          textAlign: 'right',
          userSelect: 'none',
          fontFamily: MONO,
          fontSize: FONT_SIZE,
          lineHeight: LINE_HEIGHT,
          color: 'var(--mantine-color-dimmed)',
          borderRight: '1px solid var(--mantine-color-default-border)',
        }}
      >
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </Box>
      <textarea
        aria-label={label}
        placeholder={placeholder}
        value={value}
        wrap="off"
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        onChange={(e) => onChange(e.currentTarget.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            onRun();
          }
        }}
        onFocus={(e) => {
          setFocused(true);
          onFocusEditor?.(e.currentTarget);
        }}
        onBlur={() => setFocused(false)}
        onScroll={(e) => {
          if (gutterRef.current) gutterRef.current.scrollTop = e.currentTarget.scrollTop;
        }}
        style={{
          flex: 1,
          minWidth: 0,
          height: '100%',
          padding: `${PAD_Y}px 12px`,
          border: 0,
          outline: 0,
          resize: 'none',
          overflow: 'auto',
          whiteSpace: 'pre',
          fontFamily: MONO,
          fontSize: FONT_SIZE,
          lineHeight: LINE_HEIGHT,
          background: 'transparent',
          color: 'var(--mantine-color-text)',
        }}
      />
    </Box>
  );
}
