import { TextInput, Textarea, Alert, Text, Stack } from '@mantine/core';
import { IconInfoCircle, IconPuzzle } from '@tabler/icons-react';

interface WasmConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function WasmConfig({ config, updateNodeConfig, nodeId }: WasmConfigProps) {
  return (
    <Stack gap="xs">
      {config.pluginID && (
        <Alert icon={<IconPuzzle size="1rem" />} color="indigo" mb="sm">
          <Text size="sm" fw={700}>Marketplace Plugin: {config.label}</Text>
          <Text size="xs">Using installed WASM binary for plugin <code>{config.pluginID}</code>. No manual upload or URL needed.</Text>
        </Alert>
      )}
      <TextInput
        label="WASM Function Name"
        placeholder="transform"
        value={config.function || 'transform'}
        onChange={(e) => updateNodeConfig(nodeId, { function: e.target.value })}
        mb="sm"
      />
      {!config.pluginID && (
        <Textarea
          label="WASM Binary (Base64 or URL)"
          placeholder="AGFzbQEAAAAB..."
          value={config.wasmBytes || ''}
          onChange={(e) => updateNodeConfig(nodeId, { wasmBytes: e.target.value })}
          minRows={10}
          autosize
          styles={{ input: { fontFamily: 'monospace' } }}
        />
      )}
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" py="xs">
        <Text size="xs">WebAssembly module should use WASI for I/O (JSON via stdin/stdout) and export the specified function.</Text>
      </Alert>
    </Stack>
  );
}
