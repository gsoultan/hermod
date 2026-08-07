import { Badge, Button, Checkbox, Group, Paper, Stack, Text, TextInput, Title } from '@mantine/core';
import { IconActivity } from '@tabler/icons-react';

import type { SettingsController } from './useSettingsController';

/** The "observability" tab of Settings. State lives in useSettingsController. */
export function ObservabilityTab({ ctx }: { ctx: SettingsController }) {
  const {
    handleSaveOtlp,
    otlpEndpoint,
    otlpInsecure,
    otlpServiceName,
    saveOtlpMutation,
    setOtlpEndpoint,
    setOtlpInsecure,
    setOtlpServiceName,
  } = ctx;

  return (
    <>
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconActivity size="1.2rem" color="blue" />
                <Title order={4}>OpenTelemetry (OTLP) Export</Title>
                <Badge variant="dot" color="blue">Enterprise</Badge>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Export internal metrics and message traces to an OTLP-compatible backend.
                </Text>
                <TextInput
                  label="OTLP Endpoint"
                  placeholder="http://localhost:4318"
                  value={otlpEndpoint}
                  onChange={(e) => setOtlpEndpoint(e.currentTarget.value)}
                  description="HTTP endpoint for OTLP collector. Use 4318 for HTTP/JSON."
                />
                <TextInput
                  label="Service Name"
                  placeholder="hermod"
                  value={otlpServiceName}
                  onChange={(e) => setOtlpServiceName(e.currentTarget.value)}
                />
                <Checkbox
                  label="Insecure (Disable TLS)"
                  checked={otlpInsecure}
                  onChange={(e: any) => setOtlpInsecure(e.currentTarget.checked)}
                />
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSaveOtlp} loading={saveOtlpMutation.isPending}>Update OTLP Config</Button>
                </Group>
              </Stack>
            </Paper>
          </Stack>
    </>
  );
}
