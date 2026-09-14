import { TextInput, Stack, Alert } from '@mantine/core';
import { IconFile, IconInfoCircle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface FileSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The file sink — `file.NewFileSink(filename, formatter)` and nothing else.
 * One key, but it needs a form of its own: without one the wizard fell back to
 * the database form, which asks for a host and a table.
 */
export function FileSinkConfig({ config, updateConfig }: FileSinkConfigProps) {
  return (
    <Stack gap="md">
      <FormRow cols={1}>
        <TextInput
          label="Filename"
          placeholder="/var/lib/hermod/events.jsonl"
          value={config.filename || ''}
          onChange={(e) => updateConfig('filename', e.currentTarget.value)}
          description="Messages are appended in the format chosen on the next step. The path is on the worker's filesystem, not yours."
          leftSection={<IconFile size="1rem" />}
          required
        />
      </FormRow>

      <Alert icon={<IconInfoCircle size="1rem" />} color="gray" variant="light">
        The file is opened on first write and appended to. Nothing rotates it — point this at a
        path something else truncates, or expect it to grow for as long as the workflow runs.
      </Alert>
    </Stack>
  );
}
