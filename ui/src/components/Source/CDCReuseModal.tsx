import { Modal, Stack, Text, Alert, Code, Group, Button } from '@mantine/core';
import type { FC } from 'react';

interface CDCReuseModalProps {
  cdcReusePrompt: null | {
    slot?: { name: string; exists: boolean; active?: boolean; hermod_in_use: boolean };
    publication?: { name: string; exists: boolean; hermod_in_use: boolean };
  };
  onClose: () => void;
  onAccept: () => void;
}

export const CDCReuseModal: FC<CDCReuseModalProps> = ({
  cdcReusePrompt,
  onClose,
  onAccept
}) => {
  return (
    <Modal
      opened={!!cdcReusePrompt}
      onClose={onClose}
      title="Reuse existing CDC objects?"
    >
      <Stack>
        <Text size="sm">
          We found CDC objects in your database matching the provided names. You can reuse them or cancel and change the names.
        </Text>
        {cdcReusePrompt?.slot?.exists && (
          <Alert color={cdcReusePrompt.slot.active ? 'yellow' : 'blue'} title="Replication Slot">
            Name: <Code>{cdcReusePrompt.slot.name}</Code><br />
            Active: {String(!!cdcReusePrompt.slot.active)}<br />
            Referenced by Hermod: {String(!!cdcReusePrompt.slot.hermod_in_use)}
          </Alert>
        )}
        {cdcReusePrompt?.publication?.exists && (
          <Alert color="blue" title="Publication">
            Name: <Code>{cdcReusePrompt.publication.name}</Code><br />
            Referenced by Hermod: {String(!!cdcReusePrompt.publication.hermod_in_use)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button
            color="blue"
            onClick={onAccept}
          >
            Use existing
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
