import {
  TextInput,
  Textarea,
  Alert,
  Text,
  Stack,
  Card,
  Group,
  rem,
  ThemeIcon,
  Paper,
} from '@mantine/core';
import { IconInfoCircle, IconPuzzle, IconCpu, IconActivity } from '@tabler/icons-react';

interface WasmConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function WasmConfig({ config, updateNodeConfig, nodeId }: WasmConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="indigo"
        variant="light"
        radius="md"
        title="WebAssembly Transformation"
      >
        <Text size="sm">
          Run high-performance transformations using WebAssembly. Your WASM module should follow
          WASI standards for I/O.
        </Text>
      </Alert>

      {config.pluginID && (
        <Card
          withBorder
          radius="md"
          p="md"
          bg="var(--mantine-color-indigo-0)"
          className="dark:bg-indigo-9/20"
        >
          <Group gap="md">
            <ThemeIcon color="indigo" variant="filled" size="lg" radius="md">
              <IconPuzzle size={rem(24)} />
            </ThemeIcon>
            <Stack gap={0}>
              <Text size="sm" fw={700}>
                Marketplace Plugin: {config.label}
              </Text>
              <Text size="xs" c="dimmed">
                Using installed binary for <code>{config.pluginID}</code>. Automatic updates managed
                by Hermod.
              </Text>
            </Stack>
          </Group>
        </Card>
      )}

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="blue" radius="md">
              <IconCpu size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Execution Settings
            </Text>
          </Group>

          <TextInput
            label="Entrypoint Function"
            placeholder="transform"
            value={config.function || 'transform'}
            onChange={(e) => updateNodeConfig(nodeId, { function: e.target.value })}
            size="sm"
            description="The exported function name in the WASM module."
            leftSection={<IconActivity size={rem(16)} />}
          />

          {!config.pluginID && (
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                WASM Binary Source
              </Text>
              <Textarea
                placeholder="Paste Base64 encoded binary or URL (AGFzbQEAAAAB...)"
                value={config.wasmBytes || ''}
                onChange={(e) => updateNodeConfig(nodeId, { wasmBytes: e.target.value })}
                minRows={10}
                autosize
                styles={{ input: { fontFamily: 'monospace', fontSize: rem(12) } }}
              />
              <Text size="10px" c="dimmed">
                For large binaries, providing a secure URL is recommended.
              </Text>
            </Stack>
          )}
        </Stack>
      </Card>

      <Paper
        withBorder
        p="sm"
        radius="md"
        bg="var(--mantine-color-gray-0)"
        className="dark:bg-dark-7"
      >
        <Group gap="xs">
          <IconInfoCircle size={rem(16)} className="text-gray-400" />
          <Text size="xs" c="dimmed">
            WASM modules receive payload via stdin (JSON) and should output result to stdout (JSON).
          </Text>
        </Group>
      </Paper>
    </Stack>
  );
}
