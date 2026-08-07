import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, Group, Modal, Stack, Text, TextInput, Alert, Code } from '@mantine/core';
import { IconAlertTriangle } from '@tabler/icons-react';

export interface ConfirmOptions {
  /** Short, specific title. "Delete worker", not "Are you sure?". */
  title: string;
  /** What is about to happen, in plain language. */
  message: ReactNode;
  /** Label for the confirming button. Use a verb: "Delete", "Truncate". */
  confirmLabel?: string;
  cancelLabel?: string;
  /** Renders the action in red and shows a warning icon. */
  danger?: boolean;
  /**
   * When set, the user must type this exact string to enable the confirm
   * button. Reserve it for actions that destroy data irreversibly — it is the
   * difference between a reflexive click and a deliberate one.
   */
  confirmText?: string;
  /** Extra consequence spelled out in a warning callout. */
  consequence?: ReactNode;
}

type Resolver = (confirmed: boolean) => void;

const ConfirmContext = createContext<((opts: ConfirmOptions) => Promise<boolean>) | null>(null);

/**
 * Application-wide confirmation dialogs.
 *
 * Replaces native `window.confirm`, which browsers allow users to suppress
 * permanently ("Prevent this page from creating additional dialogs"). Once
 * suppressed, every guarded action — including truncating a table — proceeds
 * with no confirmation at all. A React dialog cannot be silenced, matches the
 * theme, and can show the consequence and require typed acknowledgement.
 */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  const [typed, setTyped] = useState('');
  const resolverRef = useRef<Resolver | null>(null);

  const confirm = useCallback((options: ConfirmOptions) => {
    setTyped('');
    setOpts(options);
    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve;
    });
  }, []);

  const settle = useCallback((result: boolean) => {
    resolverRef.current?.(result);
    resolverRef.current = null;
    setOpts(null);
    setTyped('');
  }, []);

  const needsTyping = Boolean(opts?.confirmText);
  const canConfirm = !needsTyping || typed.trim() === opts?.confirmText;

  const value = useMemo(() => confirm, [confirm]);

  return (
    <ConfirmContext.Provider value={value}>
      {children}
      <Modal
        opened={opts !== null}
        // Closing by any means is a cancel, never an accidental confirm.
        onClose={() => settle(false)}
        title={<Text fw={600}>{opts?.title}</Text>}
        centered
        radius="md"
        zIndex={2100}
      >
        {opts && (
          <Stack gap="md">
            <Text size="sm">{opts.message}</Text>

            {opts.consequence && (
              <Alert
                icon={<IconAlertTriangle size="1.1rem" />}
                color={opts.danger ? 'red' : 'yellow'}
                variant="light"
              >
                <Text size="sm">{opts.consequence}</Text>
              </Alert>
            )}

            {needsTyping && (
              <TextInput
                label={<>Type <Code>{opts.confirmText}</Code> to confirm</>}
                value={typed}
                onChange={(e) => setTyped(e.currentTarget.value)}
                placeholder={opts.confirmText}
                autoComplete="off"
                data-autofocus
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && canConfirm) settle(true);
                }}
              />
            )}

            <Group justify="flex-end" gap="sm">
              <Button variant="default" onClick={() => settle(false)}>
                {opts.cancelLabel ?? 'Cancel'}
              </Button>
              <Button
                color={opts.danger ? 'red' : undefined}
                disabled={!canConfirm}
                onClick={() => settle(true)}
              >
                {opts.confirmLabel ?? 'Confirm'}
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </ConfirmContext.Provider>
  );
}

/**
 * Returns an async `confirm(options)` that resolves to true only if the user
 * explicitly confirms. Drop-in shape for `window.confirm`, but awaited:
 *
 *   if (!(await confirm({ title: 'Delete user', message: '…' }))) return;
 */
export function useConfirm() {
  const ctx = useContext(ConfirmContext);
  if (!ctx) {
    throw new Error('useConfirm must be used within a ConfirmProvider');
  }
  return ctx;
}
