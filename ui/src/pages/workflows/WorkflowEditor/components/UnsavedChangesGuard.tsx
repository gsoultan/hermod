import { useState } from 'react';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import { useBlocker } from '@tanstack/react-router';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { hasUnsavedChanges } from '../store/persistedSnapshot';

// Read at the moment of leaving rather than subscribed to: the snapshot
// serialises the whole canvas, which is too much to redo on every drag frame.
const isDirty = () => hasUnsavedChanges(useWorkflowStore.getState());

interface UnsavedChangesGuardProps {
  /** Saves the workflow; rejects when the save failed. */
  onSave: () => Promise<unknown>;
  /** The workflow is running, so saving restarts it. */
  restartsOnSave?: boolean;
}

/**
 * Asks before the editor is left with unsaved changes: Save, Discard or
 * Cancel for in-app navigation, and the browser's own leave-page prompt for
 * closing or reloading the tab.
 */
export function UnsavedChangesGuard({ onSave, restartsOnSave }: UnsavedChangesGuardProps) {
  const blocker = useBlocker({ shouldBlockFn: isDirty, enableBeforeUnload: isDirty, withResolver: true });
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await onSave();
      blocker.proceed?.();
    } catch {
      // The save reported its own error. Stay, so nothing is lost.
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      opened={blocker.status === 'blocked'}
      onClose={() => { if (!saving) blocker.reset?.(); }}
      title={<Text fw={600}>Save changes to this workflow?</Text>}
      centered
      radius="md"
    >
      <Stack gap="md">
        <Text size="sm">
          You have unsaved changes. If you leave without saving, they are lost.
          {restartsOnSave && ' Saving restarts the running workflow to apply them.'}
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="default" onClick={() => blocker.reset?.()} disabled={saving}>Cancel</Button>
          <Button color="red" variant="light" onClick={() => blocker.proceed?.()} disabled={saving}>Discard</Button>
          <Button onClick={save} loading={saving}>Save</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
