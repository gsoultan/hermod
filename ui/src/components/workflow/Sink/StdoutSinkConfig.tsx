import { Stack, Alert } from '@mantine/core';
import { IconTerminal2 } from '@tabler/icons-react';

/**
 * The stdout sink takes no configuration — `stdout.NewStdoutSink(formatter)`
 * reads nothing from the config map.
 *
 * It still needs a component. Without one the wizard fell back to the database
 * form, so "Stdout" asked for a host, a port and a table, and an empty panel
 * would have been only marginally less confusing than that. Say why it is
 * empty instead.
 */
export function StdoutSinkConfig() {
  return (
    <Stack gap="md">
      <Alert icon={<IconTerminal2 size="1rem" />} color="blue" variant="light" title="Nothing to configure">
        This sink writes each message to the worker's standard output in the format chosen on the
        next step. Where that output goes is a property of how the worker was started — a terminal,
        a log file, or the container runtime's log driver.
      </Alert>
    </Stack>
  );
}
