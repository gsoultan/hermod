import { Modal, Stack, Text, TagsInput, Alert, Group, Button } from '@mantine/core';
import type { FC } from 'react';
import { IconInfoCircle } from '@tabler/icons-react';
interface SnapshotModalProps {
  opened: boolean;
  onClose: () => void;
  source: any;
  selectedSnapshotTables: string[];
  setSelectedSnapshotTables: (tables: string[]) => void;
  snapshotMutation: any;
}

export const SnapshotModal: FC<SnapshotModalProps> = ({
  opened,
  onClose,
  source,
  selectedSnapshotTables,
  setSelectedSnapshotTables,
  snapshotMutation
}) => {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Run Initial Snapshot"
      size="md"
    >
      <Stack>
        <Text size="sm">
          Select the tables you want to include in the initial snapshot. 
          By default, all configured tables are selected.
        </Text>
        <TagsInput 
          label="Tables to Snapshot" 
          placeholder="Select tables" 
          data={((source && source.config && typeof source.config.tables === 'string') ? source.config.tables : '')
            .split(',')
            .map((t: string) => t.trim())
            .filter(Boolean)}
          value={selectedSnapshotTables}
          onChange={setSelectedSnapshotTables}
          clearable
        />
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
          The snapshot will run in the background. It will not affect the continuous CDC process.
        </Alert>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button 
            color="orange" 
            onClick={() => snapshotMutation.mutate({ sourceId: (source && source.id) || '', tables: selectedSnapshotTables })}
            loading={snapshotMutation.isPending}
            disabled={selectedSnapshotTables.length === 0}
          >
            Start Snapshot
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};


