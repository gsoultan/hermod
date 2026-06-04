import { Code, Textarea, Alert, Text, Stack } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface LuaConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function LuaConfig({ config, updateNodeConfig, nodeId }: LuaConfigProps) {
  return (
    <Stack gap="xs">
      <Code block mb="xs">
{`-- Lua Script Example
function transform(msg)
  msg.data["new_field"] = "from lua"
  return msg
end`}
      </Code>
      <Textarea 
        label="Lua Script" 
        placeholder="function transform(msg) ... end" 
        value={config.script || ''} 
        onChange={(e: any) => updateNodeConfig(nodeId, { script: e.target.value })} 
        minRows={15}
        autosize
        styles={{ input: { fontFamily: 'monospace' } }}
      />
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" py="xs">
        <Text size="xs">Lua scripts must define a `transform(msg)` function that returns the modified message.</Text>
      </Alert>
    </Stack>
  );
}
